/*
 * kms.c — see kms.h.
 *
 * The legacy KMS calls (SetCrtc, PageFlip, SetCursor) and dumb buffers: every
 * KMS driver has them, from the oldest i915 to simpledrm on a firmware
 * framebuffer, and they are all a full-screen kiosk needs. Without a DRM
 * device, the fbdev framebuffer instead: the same single buffer drawn in
 * place that simpledrm gives.
 *
 * Every connected display is driven, side by side, as one desktop. Each has
 * its own buffers and its own thread, which copies what changed and flips on
 * that display's vertical blank: two displays at different refresh rates do
 * not hold each other back.
 */
#include "kms.h"

#include <errno.h>
#include <fcntl.h>
#include <glob.h>
#include <limits.h>
#include <linux/fb.h>
#include <poll.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/ioctl.h>
#include <sys/mman.h>
#include <time.h>
#include <unistd.h>

#include <drm_fourcc.h>
#include <xf86drm.h>
#include <xf86drmMode.h>

#define MIN(a, b) ((a) < (b) ? (a) : (b))
#define MAX(a, b) ((a) > (b) ? (a) : (b))

/* A rectangle, half-open: x0 <= x < x1, y0 <= y < y1. */
struct rect {
	int x0, y0, x1, y1;
};

static bool rect_empty(struct rect a)
{
	return a.x0 >= a.x1 || a.y0 >= a.y1;
}

static long rect_area(struct rect a)
{
	return rect_empty(a) ? 0 : (long)(a.x1 - a.x0) * (a.y1 - a.y0);
}

static struct rect rect_union(struct rect a, struct rect b)
{
	if (rect_empty(a))
		return b;
	if (rect_empty(b))
		return a;
	return (struct rect){ MIN(a.x0, b.x0), MIN(a.y0, b.y0), MAX(a.x1, b.x1), MAX(a.y1, b.y1) };
}

static struct rect rect_clip(struct rect a, struct rect b)
{
	struct rect r = { MAX(a.x0, b.x0), MAX(a.y0, b.y0), MIN(a.x1, b.x1), MIN(a.y1, b.y1) };
	return rect_empty(r) ? (struct rect){ 0 } : r;
}

static bool rect_touch(struct rect a, struct rect b)
{
	return a.x0 <= b.x1 && b.x0 <= a.x1 && a.y0 <= b.y1 && b.y0 <= a.y1;
}

static struct rect rect_move(struct rect a, int dx, int dy)
{
	return (struct rect){ a.x0 + dx, a.y0 + dy, a.x1 + dx, a.y1 + dy };
}

/* A region is a few rectangles that together cover everything added to it,
 * and possibly more: rectangles that touch merge into their bounding box, and
 * when there is no room left the pair that wastes the least area does. A
 * frame's changes are usually a handful of tiles next to each other. */
#define REGION_RECTS 16

struct region {
	int n;
	struct rect r[REGION_RECTS];
};

static void region_add(struct region* g, struct rect a)
{
	if (rect_empty(a))
		return;
	for (;;) {
		int i = 0;
		while (i < g->n && !rect_touch(g->r[i], a))
			i++;
		if (i == g->n)
			break;
		a = rect_union(a, g->r[i]);
		g->r[i] = g->r[--g->n];
	}
	if (g->n == REGION_RECTS) {
		int best = 0;
		long cost = LONG_MAX;
		for (int i = 0; i < g->n; i++) {
			long c = rect_area(rect_union(g->r[i], a)) - rect_area(g->r[i]);
			if (c < cost) {
				cost = c;
				best = i;
			}
		}
		a = rect_union(a, g->r[best]);
		g->r[best] = g->r[--g->n];
		region_add(g, a);
		return;
	}
	g->r[g->n++] = a;
}

struct buffer {
	uint32_t handle, fb, pitch;
	uint64_t size;
	uint8_t* map;
	struct region damage; /* what changed since this buffer was last drawn */
	struct rect pointer;  /* where the software pointer is drawn in it */
};

#define MAX_OUTPUTS 4
#define MAX_DEVICES 8

/* A DRM device, opened once and mastered for all its displays. */
struct device {
	struct kms* k;
	int fd;
	char driver[32];
	pthread_t events; /* delivers the device's page-flip events */
	bool events_running;
};

/* A display, and the part of the desktop it shows. */
struct output {
	struct kms* k;
	struct device* dev; /* NULL for the fbdev framebuffer */
	int fd;
	char name[48];
	uint32_t conn, crtc;
	drmModeModeInfo mode;
	int x, y, w, h, hz;

	struct buffer buf[2];
	int front;
	bool shown;   /* the CRTC scans out one of ours */
	bool flips;   /* page flips work; else one buffer, drawn in place */
	bool pending; /* a flip has been queued and not completed: flip_mu */
	pthread_cond_t flipped;

	/* fd is an fbdev framebuffer, not a DRM device: buf[0] is its memory,
	 * mapped from fbmap, and there is nothing to set up or flip. */
	bool fbdev;
	uint8_t* fbmap;
	size_t fblen;

	pthread_t thread;
	bool running;
	pthread_cond_t cv; /* with mu: dirty, or stop */
	bool dirty;

	/* The hardware cursor takes an image of exactly cap_w x cap_h; there are
	 * two, so the one on screen is never rewritten. */
	uint32_t cap_w, cap_h;
	bool hw;    /* the display has a cursor plane that works */
	bool on_hw; /* the pointer is on it now; else drawn into frames */
	struct buffer cur[2];
	int cur_next;
};

struct kms {
	struct device dev[MAX_DEVICES];
	int ndev;
	struct output out[MAX_OUTPUTS];
	int n;
	int w, h; /* the desktop: the displays side by side, the first at the left */
	char name[160];

	pthread_mutex_t* src_lock;
	const uint8_t* src;
	int sstride, sw, sh, ox, oy;

	pthread_mutex_t mu; /* guards everything below, and the outputs' damage,
	                       dirty and pointer state */
	bool stop;
	unsigned presented; /* frames the first display has shown */

	pthread_mutex_t flip_mu;
	bool stop_events;

	/* The pointer, its hot spot at (px, py) on the desktop, on display pout. */
	uint32_t* img;
	int iw, ih, hx, hy;
	bool visible;
	int px, py, pout;
};

static bool make_buffer(int fd, struct buffer* b, uint32_t w, uint32_t h, uint32_t format)
{
	struct drm_mode_create_dumb c = { .width = w, .height = h, .bpp = 32 };
	if (drmIoctl(fd, DRM_IOCTL_MODE_CREATE_DUMB, &c) < 0) {
		fprintf(stderr, "kms: dumb buffer %ux%u: %s\n", w, h, strerror(errno));
		return false;
	}
	b->handle = c.handle;
	b->pitch = c.pitch;
	b->size = c.size;

	if (format) {
		uint32_t handles[4] = { b->handle }, pitches[4] = { b->pitch }, offsets[4] = { 0 };
		if (drmModeAddFB2(fd, w, h, format, handles, pitches, offsets, &b->fb, 0) < 0 &&
		    drmModeAddFB(fd, w, h, 24, 32, b->pitch, b->handle, &b->fb) < 0) {
			fprintf(stderr, "kms: framebuffer %ux%u: %s\n", w, h, strerror(errno));
			return false;
		}
	}

	struct drm_mode_map_dumb m = { .handle = b->handle };
	if (drmIoctl(fd, DRM_IOCTL_MODE_MAP_DUMB, &m) < 0) {
		fprintf(stderr, "kms: map dumb buffer: %s\n", strerror(errno));
		return false;
	}
	b->map = mmap(NULL, b->size, PROT_READ | PROT_WRITE, MAP_SHARED, fd, m.offset);
	if (b->map == MAP_FAILED) {
		b->map = NULL;
		fprintf(stderr, "kms: mmap dumb buffer: %s\n", strerror(errno));
		return false;
	}
	memset(b->map, 0, b->size);
	return true;
}

static void free_buffer(int fd, struct buffer* b)
{
	if (b->map)
		munmap(b->map, b->size);
	if (b->fb)
		drmModeRmFB(fd, b->fb);
	if (b->handle) {
		struct drm_mode_destroy_dumb d = { .handle = b->handle };
		drmIoctl(fd, DRM_IOCTL_MODE_DESTROY_DUMB, &d);
	}
	memset(b, 0, sizeof(*b));
}

/* refresh_mhz is a mode's refresh rate in thousandths of a hertz. */
static long refresh_mhz(const drmModeModeInfo* m)
{
	long t = (long)m->htotal * m->vtotal;
	if (!t)
		return (long)m->vrefresh * 1000;
	long r = (long)m->clock * 1000000L / t;
	if (m->flags & DRM_MODE_FLAG_INTERLACE)
		r *= 2;
	if (m->flags & DRM_MODE_FLAG_DBLSCAN)
		r /= 2;
	return r;
}

/* pick_mode chooses among a connector's modes: SC_MODE when it names one, else
 * the preferred resolution (the largest when none is marked), at its highest
 * refresh rate. Interlaced modes are never chosen. */
static const drmModeModeInfo* pick_mode(const drmModeConnector* c)
{
	int want_w = 0, want_h = 0, want_hz = 0;
	const char* env = getenv("SC_MODE");
	if (env && sscanf(env, "%dx%d@%d", &want_w, &want_h, &want_hz) < 2)
		want_w = want_h = want_hz = 0;

	if (!want_w) {
		for (int i = 0; i < c->count_modes; i++)
			if (c->modes[i].type & DRM_MODE_TYPE_PREFERRED) {
				want_w = c->modes[i].hdisplay;
				want_h = c->modes[i].vdisplay;
				break;
			}
	}
	if (!want_w) {
		for (int i = 0; i < c->count_modes; i++) {
			const drmModeModeInfo* m = &c->modes[i];
			if ((long)m->hdisplay * m->vdisplay > (long)want_w * want_h) {
				want_w = m->hdisplay;
				want_h = m->vdisplay;
			}
		}
	}

	const drmModeModeInfo* best = NULL;
	for (int i = 0; i < c->count_modes; i++) {
		const drmModeModeInfo* m = &c->modes[i];
		if (m->hdisplay != want_w || m->vdisplay != want_h || (m->flags & DRM_MODE_FLAG_INTERLACE))
			continue;
		if (want_hz && (refresh_mhz(m) + 500) / 1000 != want_hz)
			continue;
		if (!best || refresh_mhz(m) > refresh_mhz(best))
			best = m;
	}
	if (!best && (env || want_hz)) {
		fprintf(stderr, "kms: no mode %s on this display, using its own\n", env ? env : "");
		unsetenv("SC_MODE");
		return pick_mode(c);
	}

	/* A native resolution the link carries only at 30 Hz — 4K over HDMI 1.4
	 * — would halve the frame rate. For a moving desktop the largest mode
	 * that runs at 60 is the better trade. */
	if (best && !env && refresh_mhz(best) < 50000) {
		const drmModeModeInfo* fast = NULL;
		for (int i = 0; i < c->count_modes; i++) {
			const drmModeModeInfo* m = &c->modes[i];
			if ((m->flags & DRM_MODE_FLAG_INTERLACE) || refresh_mhz(m) < 59000)
				continue;
			long a = (long)m->hdisplay * m->vdisplay;
			long fa = fast ? (long)fast->hdisplay * fast->vdisplay : 0;
			if (!fast || a > fa || (a == fa && refresh_mhz(m) > refresh_mhz(fast)))
				fast = m;
		}
		if (fast)
			best = fast;
	}
	return best;
}

/* pick_crtc finds a CRTC that can drive the connector and no other display
 * has taken (used, a bit for each of the device's CRTCs), preferring the one
 * it is on now. */
static uint32_t pick_crtc(int fd, const drmModeRes* res, const drmModeConnector* c, uint32_t* used)
{
	int n = MIN(res->count_crtcs, 32);
	if (c->encoder_id) {
		drmModeEncoder* e = drmModeGetEncoder(fd, c->encoder_id);
		if (e) {
			uint32_t crtc = e->crtc_id;
			drmModeFreeEncoder(e);
			for (int j = 0; crtc && j < n; j++)
				if (res->crtcs[j] == crtc && !(*used & (1u << j))) {
					*used |= 1u << j;
					return crtc;
				}
		}
	}
	for (int i = 0; i < c->count_encoders; i++) {
		drmModeEncoder* e = drmModeGetEncoder(fd, c->encoders[i]);
		if (!e)
			continue;
		uint32_t possible = e->possible_crtcs;
		drmModeFreeEncoder(e);
		for (int j = 0; j < n; j++)
			if ((possible & (1u << j)) && !(*used & (1u << j))) {
				*used |= 1u << j;
				return res->crtcs[j];
			}
	}
	return 0;
}

static const char* connector_type(uint32_t t)
{
	switch (t) {
	case DRM_MODE_CONNECTOR_VGA: return "VGA";
	case DRM_MODE_CONNECTOR_DVII: return "DVI-I";
	case DRM_MODE_CONNECTOR_DVID: return "DVI-D";
	case DRM_MODE_CONNECTOR_DVIA: return "DVI-A";
	case DRM_MODE_CONNECTOR_LVDS: return "LVDS";
	case DRM_MODE_CONNECTOR_DisplayPort: return "DP";
	case DRM_MODE_CONNECTOR_HDMIA: return "HDMI-A";
	case DRM_MODE_CONNECTOR_HDMIB: return "HDMI-B";
	case DRM_MODE_CONNECTOR_eDP: return "eDP";
	case DRM_MODE_CONNECTOR_VIRTUAL: return "Virtual";
	case DRM_MODE_CONNECTOR_DSI: return "DSI";
	case DRM_MODE_CONNECTOR_DPI: return "DPI";
	default: return "Unknown";
	}
}

/* A laptop's own panel stays connected with the lid shut, where no one can
 * see it: then it is left out, unless it is the only display. */
static bool internal_panel(uint32_t type)
{
	return type == DRM_MODE_CONNECTOR_eDP || type == DRM_MODE_CONNECTOR_LVDS ||
	       type == DRM_MODE_CONNECTOR_DSI;
}

static bool lid_closed(void)
{
	glob_t g;
	if (glob("/proc/acpi/button/lid/*/state", 0, NULL, &g) != 0)
		return false;
	bool closed = false;
	for (size_t i = 0; i < g.gl_pathc && !closed; i++) {
		FILE* f = fopen(g.gl_pathv[i], "r");
		char line[64];
		if (!f)
			continue;
		if (fgets(line, sizeof(line), f) && strstr(line, "closed"))
			closed = true;
		fclose(f);
	}
	globfree(&g);
	return closed;
}

/* take_device adds a DRM device's connected displays, up to max in all. */
static void take_device(struct kms* k, const char* path, int max, bool skip_internal)
{
	if (k->ndev == MAX_DEVICES || k->n >= max)
		return;
	int fd = open(path, O_RDWR | O_CLOEXEC);
	if (fd < 0)
		return;
	uint64_t dumb = 0;
	drmModeRes* res = NULL;
	if (drmGetCap(fd, DRM_CAP_DUMB_BUFFER, &dumb) < 0 || !dumb || !(res = drmModeGetResources(fd))) {
		close(fd);
		return;
	}

	struct device* d = &k->dev[k->ndev];
	const char* dev = strrchr(path, '/');
	uint32_t used = 0;
	int before = k->n;
	for (int i = 0; i < res->count_connectors && k->n < max; i++) {
		drmModeConnector* c = drmModeGetConnector(fd, res->connectors[i]);
		if (!c)
			continue;
		const drmModeModeInfo* m;
		uint32_t crtc;
		if (c->connection == DRM_MODE_CONNECTED && c->count_modes > 0 &&
		    !(skip_internal && internal_panel(c->connector_type)) && (m = pick_mode(c)) &&
		    (crtc = pick_crtc(fd, res, c, &used))) {
			struct output* o = &k->out[k->n++];
			o->k = k;
			o->dev = d;
			o->fd = fd;
			o->conn = c->connector_id;
			o->crtc = crtc;
			o->mode = *m;
			o->w = m->hdisplay;
			o->h = m->vdisplay;
			o->hz = (int)((refresh_mhz(m) + 500) / 1000);
			snprintf(o->name, sizeof(o->name), "%s:%s-%u", dev ? dev + 1 : path,
			         connector_type(c->connector_type), c->connector_type_id);
		}
		drmModeFreeConnector(c);
	}
	drmModeFreeResources(res);
	if (k->n == before) {
		close(fd);
		return;
	}
	d->k = k;
	d->fd = fd;
	k->ndev++;
}

/* take_fbdev takes the fbdev framebuffer as it is, when there is no DRM
 * device to drive: the firmware's (efifb, vesafb) under safe graphics or on a
 * GPU no driver knows. Its pixels must be XRGB8888, which is what GRUB sets
 * (gfxmode ...x32). */
static bool take_fbdev(struct kms* k)
{
	const char* path = getenv("SC_FBDEV");
	if (!path)
		path = "/dev/fb0";
	int fd = open(path, O_RDWR | O_CLOEXEC);
	if (fd < 0)
		return false;
	struct fb_var_screeninfo v;
	struct fb_fix_screeninfo f;
	if (ioctl(fd, FBIOGET_VSCREENINFO, &v) < 0 || ioctl(fd, FBIOGET_FSCREENINFO, &f) < 0) {
		close(fd);
		return false;
	}
	if (v.bits_per_pixel != 32 || v.red.offset != 16 || v.green.offset != 8 ||
	    v.blue.offset != 0 || v.red.length != 8 || v.green.length != 8 || v.blue.length != 8) {
		fprintf(stderr, "kms: %s is %u bits a pixel, not XRGB8888\n", path, v.bits_per_pixel);
		close(fd);
		return false;
	}
	size_t off = (size_t)v.yoffset * f.line_length + (size_t)v.xoffset * 4;
	if (!v.xres || !v.yres || off + (size_t)v.yres * f.line_length > f.smem_len) {
		fprintf(stderr, "kms: %s: %ux%u does not fit its %u bytes\n", path, v.xres, v.yres,
		        f.smem_len);
		close(fd);
		return false;
	}
	uint8_t* map = mmap(NULL, f.smem_len, PROT_READ | PROT_WRITE, MAP_SHARED, fd, 0);
	if (map == MAP_FAILED) {
		fprintf(stderr, "kms: mmap %s: %s\n", path, strerror(errno));
		close(fd);
		return false;
	}
	struct output* o = &k->out[k->n++];
	o->k = k;
	o->fd = fd;
	o->fbdev = true;
	o->fbmap = map;
	o->fblen = f.smem_len;
	o->buf[0].map = map + off;
	o->buf[0].pitch = f.line_length;
	o->w = (int)v.xres;
	o->h = (int)v.yres;
	o->hz = 60;
	const char* dev = strrchr(path, '/');
	snprintf(o->name, sizeof(o->name), "%s", dev ? dev + 1 : path);
	fprintf(stderr, "kms: %s (fbdev %.16s) %dx%d, drawn in place, no cursor plane\n", o->name, f.id,
	        o->w, o->h);
	return true;
}

static void flip_done(int fd, unsigned seq, unsigned sec, unsigned usec, void* data)
{
	(void)fd;
	(void)seq;
	(void)sec;
	(void)usec;
	struct output* o = data;
	pthread_mutex_lock(&o->k->flip_mu);
	o->pending = false;
	pthread_cond_signal(&o->flipped);
	pthread_mutex_unlock(&o->k->flip_mu);
}

/* events hands a device's page-flip events to the displays that wait for
 * them. One thread reads them all: displays on the same device share its fd,
 * and a display waiting on it would take the other's events too. */
static void* events(void* arg)
{
	struct device* d = arg;
	drmEventContext ev = { .version = 2, .page_flip_handler = flip_done };
	while (!__atomic_load_n(&d->k->stop_events, __ATOMIC_ACQUIRE)) {
		struct pollfd p = { .fd = d->fd, .events = POLLIN };
		if (poll(&p, 1, 100) > 0)
			drmHandleEvent(d->fd, &ev);
	}
	return NULL;
}

struct kms* kms_open(void)
{
	struct kms* k = calloc(1, sizeof(*k));
	if (!k)
		return NULL;
	pthread_mutex_init(&k->mu, NULL);
	pthread_mutex_init(&k->flip_mu, NULL);
	pthread_condattr_t ca;
	pthread_condattr_init(&ca);
	pthread_condattr_setclock(&ca, CLOCK_MONOTONIC);
	for (int i = 0; i < MAX_OUTPUTS; i++) {
		pthread_cond_init(&k->out[i].cv, NULL);
		pthread_cond_init(&k->out[i].flipped, &ca);
	}
	pthread_condattr_destroy(&ca);

	/* SC_OUTPUTS=1 drives the first display alone. */
	int max = MAX_OUTPUTS;
	const char* env = getenv("SC_OUTPUTS");
	if (env && atoi(env) > 0)
		max = MIN(atoi(env), MAX_OUTPUTS);

	bool lid = lid_closed();
	char path[32];
	for (int i = 0; i < 16; i++) {
		snprintf(path, sizeof(path), "/dev/dri/card%d", i);
		take_device(k, path, max, lid);
	}
	for (int i = 0; i < 16 && k->n == 0 && lid; i++) {
		snprintf(path, sizeof(path), "/dev/dri/card%d", i);
		take_device(k, path, max, false);
	}
	if (k->n == 0) {
		if (take_fbdev(k)) {
			k->w = k->out[0].w;
			k->h = k->out[0].h;
			snprintf(k->name, sizeof(k->name), "%s", k->out[0].name);
			k->px = k->w / 2;
			k->py = k->h / 2;
			return k;
		}
		fprintf(stderr, "kms: no connected display on any DRM device, and no framebuffer\n");
		kms_close(k);
		return NULL;
	}

	for (int i = 0; i < k->ndev; i++) {
		struct device* d = &k->dev[i];
		/* The first to open a device is its master. A session that just
		 * ended may still hold it for a moment. */
		for (int j = 0; j < 20 && drmSetMaster(d->fd) < 0 && !drmIsMaster(d->fd); j++)
			usleep(50 * 1000);
		drmVersionPtr v = drmGetVersion(d->fd);
		if (v) {
			snprintf(d->driver, sizeof(d->driver), "%s", v->name);
			drmFreeVersion(v);
		}
	}

	size_t used = 0;
	for (int i = 0; i < k->n; i++) {
		struct output* o = &k->out[i];
		o->x = k->w;
		o->y = 0;
		k->w += o->w;
		k->h = MAX(k->h, o->h);
		used += (size_t)snprintf(k->name + used, sizeof(k->name) - MIN(used, sizeof(k->name)),
		                         "%s%s", i ? "+" : "", o->name);
		used = MIN(used, sizeof(k->name));

		for (int j = 0; j < 2; j++)
			if (!make_buffer(o->fd, &o->buf[j], o->w, o->h, DRM_FORMAT_XRGB8888)) {
				kms_close(k);
				return NULL;
			}

		/* A firmware framebuffer driver has no vertical blank to flip on,
		 * and copies the whole frame to the display on every flip; told
		 * what changed instead (DirtyFB), it copies only that. */
		const char* drv = o->dev->driver;
		o->flips = strcmp(drv, "simpledrm") != 0 && strcmp(drv, "efidrm") != 0 &&
		           strcmp(drv, "vesadrm") != 0 && strcmp(drv, "ofdrm") != 0;

		uint64_t cw = 64, ch = 64;
		drmGetCap(o->fd, DRM_CAP_CURSOR_WIDTH, &cw);
		drmGetCap(o->fd, DRM_CAP_CURSOR_HEIGHT, &ch);
		o->cap_w = (uint32_t)cw;
		o->cap_h = (uint32_t)ch;
		o->hw = make_buffer(o->fd, &o->cur[0], o->cap_w, o->cap_h, 0) &&
		        make_buffer(o->fd, &o->cur[1], o->cap_w, o->cap_h, 0);

		fprintf(stderr, "kms: %s (%s) %dx%d@%d at %d,%d, %s, cursor %ux%u\n", o->name, drv, o->w,
		        o->h, o->hz, o->x, o->y, o->flips ? "page flips" : "drawn in place", o->cap_w,
		        o->cap_h);
	}
	if (k->n > 1)
		fprintf(stderr, "kms: one desktop of %dx%d on %d displays, the first the primary\n", k->w,
		        k->h, k->n);

	for (int i = 0; i < k->ndev; i++) {
		struct device* d = &k->dev[i];
		if (pthread_create(&d->events, NULL, events, d) == 0)
			d->events_running = true;
	}

	k->px = k->out[0].w / 2;
	k->py = k->out[0].h / 2;
	return k;
}

int kms_width(const struct kms* k) { return k->w; }
int kms_height(const struct kms* k) { return k->h; }
int kms_refresh(const struct kms* k) { return k->out[0].hz; }
const char* kms_name(const struct kms* k) { return k->name; }
int kms_outputs(const struct kms* k) { return k->n; }

void kms_output(const struct kms* k, int i, int* x, int* y, int* w, int* h)
{
	const struct output* o = &k->out[i];
	*x = o->x;
	*y = o->y;
	*w = o->w;
	*h = o->h;
}

/* output_at is the display (x, y) on the desktop is on, or -1 where none is:
 * beside a display shorter than the others. */
static int output_at(const struct kms* k, int x, int y)
{
	for (int i = 0; i < k->n; i++) {
		const struct output* o = &k->out[i];
		if (x >= o->x && x < o->x + o->w && y >= o->y && y < o->y + o->h)
			return i;
	}
	return -1;
}

bool kms_clamp(const struct kms* k, int* x, int* y)
{
	if (output_at(k, *x, *y) >= 0)
		return false;
	long best = LONG_MAX;
	int bx = *x, by = *y;
	for (int i = 0; i < k->n; i++) {
		const struct output* o = &k->out[i];
		int cx = MIN(MAX(*x, o->x), o->x + o->w - 1);
		int cy = MIN(MAX(*y, o->y), o->y + o->h - 1);
		long d = (long)(cx - *x) * (cx - *x) + (long)(cy - *y) * (cy - *y);
		if (d < best) {
			best = d;
			bx = cx;
			by = cy;
		}
	}
	*x = bx;
	*y = by;
	return true;
}

/* wake asks a display's thread for a frame. Called with mu. */
static void wake(struct output* o)
{
	o->dirty = true;
	pthread_cond_signal(&o->cv);
}

/* fill_black clears a rectangle of a buffer. */
static void fill_black(struct buffer* b, struct rect r)
{
	if (rect_empty(r))
		return;
	size_t n = (size_t)(r.x1 - r.x0) * 4;
	uint8_t* p = b->map + (size_t)r.y0 * b->pitch + (size_t)r.x0 * 4;
	for (int y = r.y0; y < r.y1; y++, p += b->pitch)
		memset(p, 0, n);
}

/* fill brings a rectangle of a display's buffer up to date from the source,
 * with black where the source does not reach. r is in the display's own
 * coordinates. Called with the source lock held. */
static void fill(struct output* o, struct buffer* b, struct rect r)
{
	const struct kms* k = o->k;
	struct rect s = { k->ox, k->oy, k->ox + k->sw, k->oy + k->sh };
	struct rect in = k->src ? rect_clip(rect_move(r, o->x, o->y), s) : (struct rect){ 0 };
	if (rect_empty(in)) {
		fill_black(b, r);
		return;
	}
	in = rect_move(in, -o->x, -o->y);
	fill_black(b, (struct rect){ r.x0, r.y0, r.x1, in.y0 });
	fill_black(b, (struct rect){ r.x0, in.y1, r.x1, r.y1 });
	fill_black(b, (struct rect){ r.x0, in.y0, in.x0, in.y1 });
	fill_black(b, (struct rect){ in.x1, in.y0, r.x1, in.y1 });

	size_t n = (size_t)(in.x1 - in.x0) * 4;
	const uint8_t* src = k->src + (size_t)(in.y0 + o->y - k->oy) * k->sstride +
	                     (size_t)(in.x0 + o->x - k->ox) * 4;
	uint8_t* dst = b->map + (size_t)in.y0 * b->pitch + (size_t)in.x0 * 4;
	for (int y = in.y0; y < in.y1; y++) {
		memcpy(dst, src, n);
		src += k->sstride;
		dst += b->pitch;
	}
}

/* source_pixel is what the desktop shows at (x, y) without the pointer. */
static uint32_t source_pixel(const struct kms* k, int x, int y)
{
	int sx = x - k->ox, sy = y - k->oy;
	if (!k->src || sx < 0 || sy < 0 || sx >= k->sw || sy >= k->sh)
		return 0;
	return ((const uint32_t*)(k->src + (size_t)sy * k->sstride))[sx];
}

/* sw_pointer reports whether the pointer is drawn into the frames: it is
 * shown, and not on the cursor plane of the display it is on. */
static bool sw_pointer(const struct kms* k)
{
	return k->visible && k->img && !(k->pout >= 0 && k->out[k->pout].on_hw);
}

/* pointer_rect is where the software pointer covers a display, in the
 * display's own coordinates. */
static struct rect pointer_rect(const struct output* o)
{
	const struct kms* k = o->k;
	if (!sw_pointer(k))
		return (struct rect){ 0 };
	struct rect c = { k->px - k->hx, k->py - k->hy, k->px - k->hx + k->iw, k->py - k->hy + k->ih };
	return rect_clip(rect_move(c, -o->x, -o->y), (struct rect){ 0, 0, o->w, o->h });
}

/* draw_pointer blends the software pointer into a buffer, over the source
 * rather than over what the buffer holds: reading scanout memory is slow.
 * Called with the source lock and mu held. */
static void draw_pointer(struct output* o, struct buffer* b)
{
	const struct kms* k = o->k;
	struct rect v = pointer_rect(o);
	b->pointer = v;
	if (rect_empty(v))
		return;
	int x0 = k->px - k->hx - o->x, y0 = k->py - k->hy - o->y;
	for (int y = v.y0; y < v.y1; y++) {
		const uint32_t* s = k->img + (size_t)(y - y0) * k->iw;
		uint32_t* d = (uint32_t*)(b->map + (size_t)y * b->pitch);
		for (int x = v.x0; x < v.x1; x++) {
			uint32_t p = s[x - x0], a = p >> 24;
			if (a == 255) {
				d[x] = p;
				continue;
			}
			uint32_t q = source_pixel(k, x + o->x, y + o->y), out = 0;
			for (int sh = 0; sh < 24; sh += 8) {
				uint32_t c = ((p >> sh) & 0xff) + (((q >> sh) & 0xff) * (255 - a) + 127) / 255;
				out |= MIN(c, 255u) << sh;
			}
			d[x] = out;
		}
	}
}

/* wait_flip waits for the queued flip to reach the screen. A display that has
 * been switched off may never send the event; half a second is plenty. */
static void wait_flip(struct output* o)
{
	struct kms* k = o->k;
	struct timespec t;
	clock_gettime(CLOCK_MONOTONIC, &t);
	t.tv_nsec += 500 * 1000000L;
	if (t.tv_nsec >= 1000000000L) {
		t.tv_sec++;
		t.tv_nsec -= 1000000000L;
	}
	pthread_mutex_lock(&k->flip_mu);
	while (o->pending)
		if (pthread_cond_timedwait(&o->flipped, &k->flip_mu, &t) == ETIMEDOUT) {
			o->pending = false;
			break;
		}
	pthread_mutex_unlock(&k->flip_mu);
}

static bool hw_cursor(struct output* o);

static bool fits_hw(const struct output* o)
{
	const struct kms* k = o->k;
	return o->hw && k->img && (uint32_t)k->iw <= o->cap_w && (uint32_t)k->ih <= o->cap_h;
}

/* hide_hw takes the pointer off a display's cursor plane. Called with mu. */
static void hide_hw(struct output* o)
{
	if (o->on_hw)
		drmModeSetCursor(o->fd, o->crtc, 0, 0, 0);
	o->on_hw = false;
}

/* place_pointer shows the pointer on the cursor plane of the display its hot
 * spot is on, and on no other; image says the image changed. Where that
 * display has no cursor plane for it, it is drawn into the frames. Called
 * with mu. */
static void place_pointer(struct kms* k, bool image)
{
	int at = output_at(k, k->px, k->py);
	if (at < 0)
		at = k->pout;
	for (int i = 0; i < k->n; i++)
		if (i != at)
			hide_hw(&k->out[i]);
	k->pout = at;
	struct output* o = &k->out[at];
	if (!k->visible || !o->shown || !fits_hw(o)) {
		hide_hw(o);
		return;
	}
	if (o->on_hw && !image) {
		drmModeMoveCursor(o->fd, o->crtc, k->px - k->hx - o->x, k->py - k->hy - o->y);
		return;
	}
	bool was = o->on_hw;
	o->on_hw = hw_cursor(o);
	if (was && !o->on_hw)
		drmModeSetCursor(o->fd, o->crtc, 0, 0, 0);
}

/* show puts buffer i on the display: a flip on the next vertical blank, or,
 * the first time and where flips do not work, at once. */
static void show(struct output* o, int i, const struct region* drawn)
{
	struct kms* k = o->k;
	struct buffer* b = &o->buf[i];
	if (o->fbdev) {
		/* What was drawn is on the screen already. */
		o->shown = true;
		return;
	}
	if (!o->shown) {
		uint32_t conn = o->conn;
		if (drmModeSetCrtc(o->fd, o->crtc, b->fb, 0, 0, &conn, 1, &o->mode) < 0) {
			fprintf(stderr, "kms: %s: set mode %dx%d: %s\n", o->name, o->w, o->h, strerror(errno));
			return;
		}
		pthread_mutex_lock(&k->mu);
		o->shown = true;
		o->front = i;
		/* The pointer goes on the cursor plane once there is a mode for it
		 * to be on; until now it was drawn into the frames, which are
		 * redrawn without it. */
		if (k->pout == (int)(o - k->out) && k->visible && !o->on_hw && fits_hw(o)) {
			place_pointer(k, true);
			if (o->on_hw)
				for (int j = 0; j < k->n; j++)
					wake(&k->out[j]);
		}
		pthread_mutex_unlock(&k->mu);
		return;
	}
	if (o->flips) {
		pthread_mutex_lock(&k->flip_mu);
		o->pending = true;
		pthread_mutex_unlock(&k->flip_mu);
		if (drmModePageFlip(o->fd, o->crtc, b->fb, DRM_MODE_PAGE_FLIP_EVENT, o) == 0) {
			wait_flip(o);
			o->front = i;
			return;
		}
		pthread_mutex_lock(&k->flip_mu);
		o->pending = false;
		pthread_mutex_unlock(&k->flip_mu);
		fprintf(stderr, "kms: %s: page flip: %s; drawing in place from now on\n", o->name,
		        strerror(errno));
		o->flips = false;
		uint32_t conn = o->conn;
		drmModeSetCrtc(o->fd, o->crtc, b->fb, 0, 0, &conn, 1, &o->mode);
		o->front = i;
		return;
	}
	/* One buffer, drawn while it is shown: drivers that copy to the display
	 * themselves (USB, virtual, simpledrm) need telling what changed. */
	drmModeClip clips[REGION_RECTS + 2];
	int n = 0;
	for (int j = 0; j < drawn->n && n < (int)(sizeof(clips) / sizeof(clips[0])); j++)
		clips[n++] = (drmModeClip){ (uint16_t)drawn->r[j].x0, (uint16_t)drawn->r[j].y0,
		                            (uint16_t)drawn->r[j].x1, (uint16_t)drawn->r[j].y1 };
	if (n)
		drmModeDirtyFB(o->fd, b->fb, clips, (uint32_t)n);
}

static void* present(void* arg)
{
	struct output* o = arg;
	struct kms* k = o->k;
	pthread_mutex_lock(&k->mu);
	for (;;) {
		while (!o->dirty && !k->stop)
			pthread_cond_wait(&o->cv, &k->mu);
		if (k->stop)
			break;
		o->dirty = false;
		pthread_mutex_unlock(&k->mu);

		/* The source lock is held from taking the damage to the end of the
		 * copy, so a frame never shows half of an update; mu only briefly,
		 * so moving the pointer never waits for a copy. Lock order: the
		 * source, then mu. */
		pthread_mutex_lock(k->src_lock);
		pthread_mutex_lock(&k->mu);
		int i = o->shown && o->flips ? 1 - o->front : o->front;
		struct buffer* b = &o->buf[i];
		struct region todo = b->damage;
		b->damage.n = 0;
		/* Where the pointer was drawn in this buffer is restored too. */
		region_add(&todo, b->pointer);
		pthread_mutex_unlock(&k->mu);

		for (int j = 0; j < todo.n; j++)
			fill(o, b, todo.r[j]);

		pthread_mutex_lock(&k->mu);
		draw_pointer(o, b);
		region_add(&todo, b->pointer);
		pthread_mutex_unlock(&k->mu);
		pthread_mutex_unlock(k->src_lock);

		show(o, i, &todo);

		pthread_mutex_lock(&k->mu);
		if (o == &k->out[0])
			k->presented++;
	}
	pthread_mutex_unlock(&k->mu);
	return NULL;
}

/* damage_all marks every display changed in both its buffers. Called with mu. */
static void damage_all(struct kms* k)
{
	for (int i = 0; i < k->n; i++) {
		struct output* o = &k->out[i];
		for (int j = 0; j < 2; j++) {
			o->buf[j].damage.n = 0;
			region_add(&o->buf[j].damage, (struct rect){ 0, 0, o->w, o->h });
		}
		wake(o);
	}
}

/* set_source places the session's desktop: over the whole of ours, centred
 * where it is smaller. One that fits the first display — a server that took
 * no notice of the others — is centred on it. */
static void set_source(struct kms* k, const uint8_t* src, int stride, int w, int h)
{
	k->src = src;
	k->sstride = stride;
	k->sw = w;
	k->sh = h;
	const struct output* p = &k->out[0];
	if (k->n > 1 && w <= p->w && h <= p->h) {
		k->ox = p->x + (p->w - w) / 2;
		k->oy = p->y + (p->h - h) / 2;
		return;
	}
	k->ox = w < k->w ? (k->w - w) / 2 : 0;
	k->oy = h < k->h ? (k->h - h) / 2 : 0;
}

bool kms_start(struct kms* k, const uint8_t* src, int stride, int w, int h, pthread_mutex_t* lock)
{
	/* A second start, after FreeRDP reconnected a dropped session, only
	 * points the displays at the new buffer. */
	k->src_lock = lock;
	pthread_mutex_lock(lock);
	pthread_mutex_lock(&k->mu);
	set_source(k, src, stride, w, h);
	damage_all(k);
	pthread_mutex_unlock(&k->mu);
	pthread_mutex_unlock(lock);

	for (int i = 0; i < k->n; i++) {
		struct output* o = &k->out[i];
		if (o->running)
			continue;
		if (pthread_create(&o->thread, NULL, present, o) != 0) {
			fprintf(stderr, "kms: present thread: %s\n", strerror(errno));
			return false;
		}
		o->running = true;
	}
	return true;
}

void kms_source(struct kms* k, const uint8_t* src, int stride, int w, int h)
{
	pthread_mutex_lock(&k->mu);
	set_source(k, src, stride, w, h);
	damage_all(k);
	pthread_mutex_unlock(&k->mu);
}

void kms_damage(struct kms* k, int x, int y, int w, int h)
{
	pthread_mutex_lock(&k->mu);
	struct rect r = { k->ox + x, k->oy + y, k->ox + x + w, k->oy + y + h };
	for (int i = 0; i < k->n; i++) {
		struct output* o = &k->out[i];
		struct rect v = rect_clip(rect_move(r, -o->x, -o->y), (struct rect){ 0, 0, o->w, o->h });
		if (rect_empty(v))
			continue;
		region_add(&o->buf[0].damage, v);
		region_add(&o->buf[1].damage, v);
		wake(o);
	}
	pthread_mutex_unlock(&k->mu);
}

void kms_origin(struct kms* k, int* x, int* y)
{
	pthread_mutex_lock(&k->mu);
	*x = k->ox;
	*y = k->oy;
	pthread_mutex_unlock(&k->mu);
}

/* hw_cursor puts the image on a display's cursor plane. Called with mu. */
static bool hw_cursor(struct output* o)
{
	const struct kms* k = o->k;
	struct buffer* c = &o->cur[o->cur_next];
	memset(c->map, 0, c->size);
	for (int y = 0; y < k->ih; y++)
		memcpy(c->map + (size_t)y * c->pitch, k->img + (size_t)y * k->iw, (size_t)k->iw * 4);
	if (drmModeSetCursor2(o->fd, o->crtc, c->handle, o->cap_w, o->cap_h, k->hx, k->hy) < 0 &&
	    drmModeSetCursor(o->fd, o->crtc, c->handle, o->cap_w, o->cap_h) < 0) {
		fprintf(stderr, "kms: %s: no hardware cursor (%s), drawing the pointer\n", o->name,
		        strerror(errno));
		o->hw = false;
		return false;
	}
	o->cur_next ^= 1;
	drmModeMoveCursor(o->fd, o->crtc, k->px - k->hx - o->x, k->py - k->hy - o->y);
	return true;
}

/* sw_changed asks for a frame on every display the software pointer was or
 * now is on; before is where it was. Called with mu. */
static void sw_changed(struct kms* k, const struct rect* before)
{
	for (int i = 0; i < k->n; i++)
		if (!rect_empty(before[i]) || !rect_empty(pointer_rect(&k->out[i])))
			wake(&k->out[i]);
}

static void pointer_rects(const struct kms* k, struct rect* r)
{
	for (int i = 0; i < k->n; i++)
		r[i] = pointer_rect(&k->out[i]);
}

void kms_cursor(struct kms* k, const uint32_t* argb, int w, int h, int hx, int hy)
{
	pthread_mutex_lock(&k->mu);
	struct rect before[MAX_OUTPUTS];
	pointer_rects(k, before);
	if (!argb || w <= 0 || h <= 0) {
		k->visible = false;
		for (int i = 0; i < k->n; i++)
			hide_hw(&k->out[i]);
		sw_changed(k, before);
		pthread_mutex_unlock(&k->mu);
		return;
	}
	uint32_t* img = realloc(k->img, (size_t)w * h * 4);
	if (img) {
		memcpy(img, argb, (size_t)w * h * 4);
		k->img = img;
		k->iw = w;
		k->ih = h;
		k->hx = hx;
		k->hy = hy;
		k->visible = true;
		place_pointer(k, true);
	}
	sw_changed(k, before);
	pthread_mutex_unlock(&k->mu);
}

void kms_cursor_move(struct kms* k, int x, int y)
{
	pthread_mutex_lock(&k->mu);
	struct rect before[MAX_OUTPUTS];
	pointer_rects(k, before);
	k->px = x;
	k->py = y;
	place_pointer(k, false);
	sw_changed(k, before);
	pthread_mutex_unlock(&k->mu);
}

unsigned kms_presented(struct kms* k)
{
	pthread_mutex_lock(&k->mu);
	unsigned n = k->presented;
	pthread_mutex_unlock(&k->mu);
	return n;
}

void kms_close(struct kms* k)
{
	if (!k)
		return;
	pthread_mutex_lock(&k->mu);
	k->stop = true;
	for (int i = 0; i < k->n; i++)
		pthread_cond_signal(&k->out[i].cv);
	pthread_mutex_unlock(&k->mu);
	for (int i = 0; i < k->n; i++)
		if (k->out[i].running)
			pthread_join(k->out[i].thread, NULL);
	__atomic_store_n(&k->stop_events, true, __ATOMIC_RELEASE);
	for (int i = 0; i < k->ndev; i++)
		if (k->dev[i].events_running)
			pthread_join(k->dev[i].events, NULL);

	for (int i = 0; i < k->n; i++) {
		struct output* o = &k->out[i];
		if (o->fbdev) {
			/* The kiosk repaints its own screen over what the session left. */
			munmap(o->fbmap, o->fblen);
			close(o->fd);
			continue;
		}
		hide_hw(o);
		for (int j = 0; j < 2; j++) {
			free_buffer(o->fd, &o->buf[j]);
			free_buffer(o->fd, &o->cur[j]);
		}
	}
	/* Closing a device hands its displays back to the console's
	 * framebuffer, which is where the kiosk's own screens are. */
	for (int i = 0; i < k->ndev; i++) {
		drmDropMaster(k->dev[i].fd);
		close(k->dev[i].fd);
	}
	free(k->img);
	for (int i = 0; i < MAX_OUTPUTS; i++) {
		pthread_cond_destroy(&k->out[i].cv);
		pthread_cond_destroy(&k->out[i].flipped);
	}
	pthread_mutex_destroy(&k->mu);
	pthread_mutex_destroy(&k->flip_mu);
	free(k);
}
