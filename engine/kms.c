/*
 * kms.c — see kms.h.
 *
 * The legacy KMS calls (SetCrtc, PageFlip, SetCursor) and dumb buffers: every
 * KMS driver has them, from the oldest i915 to simpledrm on a firmware
 * framebuffer, and they are all a full-screen kiosk needs.
 */
#include "kms.h"

#include <errno.h>
#include <fcntl.h>
#include <limits.h>
#include <poll.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/mman.h>
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

struct kms {
	int fd;
	char name[64];
	char driver[32];
	uint32_t conn, crtc;
	drmModeModeInfo mode;
	int w, h, hz;

	struct buffer buf[2];
	int front;
	bool shown;   /* the CRTC scans out one of ours */
	bool flips;   /* page flips work; else one buffer, drawn in place */
	bool pending; /* a flip has been queued and not completed */

	pthread_mutex_t* src_lock;
	const uint8_t* src;
	int sstride, sw, sh, ox, oy;

	pthread_t thread;
	bool running;
	pthread_mutex_t mu; /* guards everything below, and damage */
	pthread_cond_t cv;
	bool dirty, stop;
	unsigned presented;

	/* The pointer. The hardware cursor takes an image of exactly cap_w x
	 * cap_h; there are two, so the one on screen is never rewritten. */
	uint32_t cap_w, cap_h;
	bool hw;     /* the display has a cursor plane that works */
	bool on_hw;  /* the current image is on it; else drawn into frames */
	struct buffer cur[2];
	int cur_next;
	uint32_t* img;
	int iw, ih, hx, hy;
	bool visible;
	int px, py; /* the hot spot, on the screen */
};

static bool make_buffer(struct kms* k, struct buffer* b, uint32_t w, uint32_t h, uint32_t format)
{
	struct drm_mode_create_dumb c = { .width = w, .height = h, .bpp = 32 };
	if (drmIoctl(k->fd, DRM_IOCTL_MODE_CREATE_DUMB, &c) < 0) {
		fprintf(stderr, "kms: dumb buffer %ux%u: %s\n", w, h, strerror(errno));
		return false;
	}
	b->handle = c.handle;
	b->pitch = c.pitch;
	b->size = c.size;

	if (format) {
		uint32_t handles[4] = { b->handle }, pitches[4] = { b->pitch }, offsets[4] = { 0 };
		if (drmModeAddFB2(k->fd, w, h, format, handles, pitches, offsets, &b->fb, 0) < 0 &&
		    drmModeAddFB(k->fd, w, h, 24, 32, b->pitch, b->handle, &b->fb) < 0) {
			fprintf(stderr, "kms: framebuffer %ux%u: %s\n", w, h, strerror(errno));
			return false;
		}
	}

	struct drm_mode_map_dumb m = { .handle = b->handle };
	if (drmIoctl(k->fd, DRM_IOCTL_MODE_MAP_DUMB, &m) < 0) {
		fprintf(stderr, "kms: map dumb buffer: %s\n", strerror(errno));
		return false;
	}
	b->map = mmap(NULL, b->size, PROT_READ | PROT_WRITE, MAP_SHARED, k->fd, m.offset);
	if (b->map == MAP_FAILED) {
		b->map = NULL;
		fprintf(stderr, "kms: mmap dumb buffer: %s\n", strerror(errno));
		return false;
	}
	memset(b->map, 0, b->size);
	return true;
}

static void free_buffer(struct kms* k, struct buffer* b)
{
	if (b->map)
		munmap(b->map, b->size);
	if (b->fb)
		drmModeRmFB(k->fd, b->fb);
	if (b->handle) {
		struct drm_mode_destroy_dumb d = { .handle = b->handle };
		drmIoctl(k->fd, DRM_IOCTL_MODE_DESTROY_DUMB, &d);
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
	return best;
}

/* pick_crtc finds a CRTC that can drive the connector, preferring the one it
 * is on now. */
static uint32_t pick_crtc(int fd, const drmModeRes* res, const drmModeConnector* c)
{
	if (c->encoder_id) {
		drmModeEncoder* e = drmModeGetEncoder(fd, c->encoder_id);
		if (e) {
			uint32_t crtc = e->crtc_id;
			drmModeFreeEncoder(e);
			if (crtc)
				return crtc;
		}
	}
	for (int i = 0; i < c->count_encoders; i++) {
		drmModeEncoder* e = drmModeGetEncoder(fd, c->encoders[i]);
		if (!e)
			continue;
		uint32_t possible = e->possible_crtcs;
		drmModeFreeEncoder(e);
		for (int j = 0; j < res->count_crtcs; j++)
			if (possible & (1u << j))
				return res->crtcs[j];
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

/* take_display looks for a connected display on one DRM device. */
static bool take_display(struct kms* k, const char* path)
{
	int fd = open(path, O_RDWR | O_CLOEXEC);
	if (fd < 0)
		return false;
	uint64_t dumb = 0;
	drmModeRes* res = NULL;
	if (drmGetCap(fd, DRM_CAP_DUMB_BUFFER, &dumb) < 0 || !dumb || !(res = drmModeGetResources(fd))) {
		close(fd);
		return false;
	}

	bool found = false;
	for (int i = 0; i < res->count_connectors && !found; i++) {
		drmModeConnector* c = drmModeGetConnector(fd, res->connectors[i]);
		if (!c)
			continue;
		const drmModeModeInfo* m;
		uint32_t crtc;
		if (c->connection == DRM_MODE_CONNECTED && c->count_modes > 0 && (m = pick_mode(c)) &&
		    (crtc = pick_crtc(fd, res, c))) {
			k->fd = fd;
			k->conn = c->connector_id;
			k->crtc = crtc;
			k->mode = *m;
			k->w = m->hdisplay;
			k->h = m->vdisplay;
			k->hz = (int)((refresh_mhz(m) + 500) / 1000);
			const char* dev = strrchr(path, '/');
			snprintf(k->name, sizeof(k->name), "%s:%s-%u", dev ? dev + 1 : path,
			         connector_type(c->connector_type), c->connector_type_id);
			found = true;
		}
		drmModeFreeConnector(c);
	}
	drmModeFreeResources(res);
	if (!found)
		close(fd);
	return found;
}

struct kms* kms_open(void)
{
	struct kms* k = calloc(1, sizeof(*k));
	if (!k)
		return NULL;
	k->fd = -1;
	pthread_mutex_init(&k->mu, NULL);
	pthread_cond_init(&k->cv, NULL);

	char path[32];
	for (int i = 0; i < 16 && k->fd < 0; i++) {
		snprintf(path, sizeof(path), "/dev/dri/card%d", i);
		take_display(k, path);
	}
	if (k->fd < 0) {
		fprintf(stderr, "kms: no connected display on any DRM device\n");
		free(k);
		return NULL;
	}

	/* The first to open a device is its master. A session that just ended
	 * may still hold it for a moment. */
	for (int i = 0; i < 20 && drmSetMaster(k->fd) < 0 && !drmIsMaster(k->fd); i++)
		usleep(50 * 1000);

	for (int i = 0; i < 2; i++)
		if (!make_buffer(k, &k->buf[i], k->w, k->h, DRM_FORMAT_XRGB8888)) {
			kms_close(k);
			return NULL;
		}

	/* A firmware framebuffer driver has no vertical blank to flip on, and
	 * copies the whole frame to the display on every flip; told what changed
	 * instead (DirtyFB), it copies only that. */
	k->flips = true;
	drmVersionPtr v = drmGetVersion(k->fd);
	if (v) {
		if (strcmp(v->name, "simpledrm") == 0 || strcmp(v->name, "efidrm") == 0 ||
		    strcmp(v->name, "vesadrm") == 0 || strcmp(v->name, "ofdrm") == 0)
			k->flips = false;
		snprintf(k->driver, sizeof(k->driver), "%s", v->name);
		drmFreeVersion(v);
	}

	uint64_t cw = 64, ch = 64;
	drmGetCap(k->fd, DRM_CAP_CURSOR_WIDTH, &cw);
	drmGetCap(k->fd, DRM_CAP_CURSOR_HEIGHT, &ch);
	k->cap_w = (uint32_t)cw;
	k->cap_h = (uint32_t)ch;
	k->hw = make_buffer(k, &k->cur[0], k->cap_w, k->cap_h, 0) &&
	        make_buffer(k, &k->cur[1], k->cap_w, k->cap_h, 0);
	k->px = k->w / 2;
	k->py = k->h / 2;

	fprintf(stderr, "kms: %s (%s) %dx%d@%d, %s, cursor %ux%u\n", k->name, k->driver, k->w, k->h,
	        k->hz, k->flips ? "page flips" : "drawn in place", k->cap_w, k->cap_h);
	return k;
}

int kms_width(const struct kms* k) { return k->w; }
int kms_height(const struct kms* k) { return k->h; }
int kms_refresh(const struct kms* k) { return k->hz; }
const char* kms_name(const struct kms* k) { return k->name; }

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

/* fill brings a screen rectangle of a buffer up to date from the source, with
 * black where the source does not reach. Called with the source lock held. */
static void fill(struct kms* k, struct buffer* b, struct rect r)
{
	struct rect s = { k->ox, k->oy, k->ox + k->sw, k->oy + k->sh };
	struct rect in = k->src ? rect_clip(r, s) : (struct rect){ 0 };
	if (rect_empty(in)) {
		fill_black(b, r);
		return;
	}
	fill_black(b, (struct rect){ r.x0, r.y0, r.x1, in.y0 });
	fill_black(b, (struct rect){ r.x0, in.y1, r.x1, r.y1 });
	fill_black(b, (struct rect){ r.x0, in.y0, in.x0, in.y1 });
	fill_black(b, (struct rect){ in.x1, in.y0, r.x1, in.y1 });

	size_t n = (size_t)(in.x1 - in.x0) * 4;
	const uint8_t* src = k->src + (size_t)(in.y0 - k->oy) * k->sstride + (size_t)(in.x0 - k->ox) * 4;
	uint8_t* dst = b->map + (size_t)in.y0 * b->pitch + (size_t)in.x0 * 4;
	for (int y = in.y0; y < in.y1; y++) {
		memcpy(dst, src, n);
		src += k->sstride;
		dst += b->pitch;
	}
}

/* source_pixel is what the screen shows at (x, y) without the pointer. */
static uint32_t source_pixel(const struct kms* k, int x, int y)
{
	int sx = x - k->ox, sy = y - k->oy;
	if (!k->src || sx < 0 || sy < 0 || sx >= k->sw || sy >= k->sh)
		return 0;
	return ((const uint32_t*)(k->src + (size_t)sy * k->sstride))[sx];
}

/* pointer_rect is where the software pointer covers the screen. */
static struct rect pointer_rect(const struct kms* k)
{
	if (!k->visible || k->on_hw || !k->img)
		return (struct rect){ 0 };
	struct rect c = { k->px - k->hx, k->py - k->hy, k->px - k->hx + k->iw, k->py - k->hy + k->ih };
	return rect_clip(c, (struct rect){ 0, 0, k->w, k->h });
}

/* draw_pointer blends the software pointer into a buffer, over the source
 * rather than over what the buffer holds: reading scanout memory is slow.
 * Called with the source lock and mu held. */
static void draw_pointer(struct kms* k, struct buffer* b)
{
	struct rect v = pointer_rect(k);
	b->pointer = v;
	if (rect_empty(v))
		return;
	int x0 = k->px - k->hx, y0 = k->py - k->hy;
	for (int y = v.y0; y < v.y1; y++) {
		const uint32_t* s = k->img + (size_t)(y - y0) * k->iw;
		uint32_t* d = (uint32_t*)(b->map + (size_t)y * b->pitch);
		for (int x = v.x0; x < v.x1; x++) {
			uint32_t p = s[x - x0], a = p >> 24;
			if (a == 255) {
				d[x] = p;
				continue;
			}
			uint32_t q = source_pixel(k, x, y), out = 0;
			for (int sh = 0; sh < 24; sh += 8) {
				uint32_t c = ((p >> sh) & 0xff) + (((q >> sh) & 0xff) * (255 - a) + 127) / 255;
				out |= MIN(c, 255u) << sh;
			}
			d[x] = out;
		}
	}
}

static void flip_done(int fd, unsigned seq, unsigned sec, unsigned usec, void* data)
{
	(void)fd;
	(void)seq;
	(void)sec;
	(void)usec;
	((struct kms*)data)->pending = false;
}

/* wait_flip waits for the queued flip to reach the screen. A display that has
 * been switched off may never send the event; half a second is plenty. */
static void wait_flip(struct kms* k)
{
	drmEventContext ev = { .version = 2, .page_flip_handler = flip_done };
	while (k->pending) {
		struct pollfd p = { .fd = k->fd, .events = POLLIN };
		int r = poll(&p, 1, 500);
		if (r < 0 && errno == EINTR)
			continue;
		if (r <= 0) {
			k->pending = false;
			break;
		}
		drmHandleEvent(k->fd, &ev);
	}
}

static bool hw_cursor(struct kms* k);

static bool fits_hw(const struct kms* k)
{
	return k->hw && k->img && (uint32_t)k->iw <= k->cap_w && (uint32_t)k->ih <= k->cap_h;
}

/* show puts buffer i on the screen: a flip on the next vertical blank, or, the
 * first time and where flips do not work, at once. */
static void show(struct kms* k, int i, const struct region* drawn)
{
	struct buffer* b = &k->buf[i];
	if (!k->shown) {
		uint32_t conn = k->conn;
		if (drmModeSetCrtc(k->fd, k->crtc, b->fb, 0, 0, &conn, 1, &k->mode) < 0) {
			fprintf(stderr, "kms: set mode %dx%d: %s\n", k->w, k->h, strerror(errno));
			return;
		}
		pthread_mutex_lock(&k->mu);
		k->shown = true;
		k->front = i;
		/* The pointer goes on the cursor plane once there is a mode for
		 * it to be on; until now it was drawn into the frame. */
		if (k->visible && !k->on_hw && fits_hw(k) && hw_cursor(k)) {
			k->on_hw = true;
			k->dirty = true;
		}
		pthread_mutex_unlock(&k->mu);
		return;
	}
	if (k->flips) {
		if (drmModePageFlip(k->fd, k->crtc, b->fb, DRM_MODE_PAGE_FLIP_EVENT, k) == 0) {
			k->pending = true;
			wait_flip(k);
			k->front = i;
			return;
		}
		fprintf(stderr, "kms: page flip: %s; drawing in place from now on\n", strerror(errno));
		k->flips = false;
		uint32_t conn = k->conn;
		drmModeSetCrtc(k->fd, k->crtc, b->fb, 0, 0, &conn, 1, &k->mode);
		k->front = i;
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
		drmModeDirtyFB(k->fd, b->fb, clips, (uint32_t)n);
}

static void* present(void* arg)
{
	struct kms* k = arg;
	pthread_mutex_lock(&k->mu);
	for (;;) {
		while (!k->dirty && !k->stop)
			pthread_cond_wait(&k->cv, &k->mu);
		if (k->stop)
			break;
		k->dirty = false;
		pthread_mutex_unlock(&k->mu);

		/* The source lock is held from taking the damage to the end of the
		 * copy, so a frame never shows half of an update; mu only briefly,
		 * so moving the pointer never waits for a copy. Lock order: the
		 * source, then mu. */
		pthread_mutex_lock(k->src_lock);
		pthread_mutex_lock(&k->mu);
		int i = k->shown && k->flips ? 1 - k->front : k->front;
		struct buffer* b = &k->buf[i];
		struct region todo = b->damage;
		b->damage.n = 0;
		/* Where the pointer was drawn in this buffer is restored too. */
		region_add(&todo, b->pointer);
		pthread_mutex_unlock(&k->mu);

		for (int j = 0; j < todo.n; j++)
			fill(k, b, todo.r[j]);

		pthread_mutex_lock(&k->mu);
		draw_pointer(k, b);
		region_add(&todo, b->pointer);
		pthread_mutex_unlock(&k->mu);
		pthread_mutex_unlock(k->src_lock);

		show(k, i, &todo);

		pthread_mutex_lock(&k->mu);
		k->presented++;
	}
	pthread_mutex_unlock(&k->mu);
	return NULL;
}

/* damage_all marks the whole screen changed in both buffers. Called with mu. */
static void damage_all(struct kms* k)
{
	for (int i = 0; i < 2; i++) {
		k->buf[i].damage.n = 0;
		region_add(&k->buf[i].damage, (struct rect){ 0, 0, k->w, k->h });
	}
	k->dirty = true;
	pthread_cond_signal(&k->cv);
}

static void set_source(struct kms* k, const uint8_t* src, int stride, int w, int h)
{
	k->src = src;
	k->sstride = stride;
	k->sw = w;
	k->sh = h;
	k->ox = w < k->w ? (k->w - w) / 2 : 0;
	k->oy = h < k->h ? (k->h - h) / 2 : 0;
}

bool kms_start(struct kms* k, const uint8_t* src, int stride, int w, int h, pthread_mutex_t* lock)
{
	/* A second start, after FreeRDP reconnected a dropped session, only
	 * points the screen at the new buffer. */
	k->src_lock = lock;
	pthread_mutex_lock(lock);
	pthread_mutex_lock(&k->mu);
	set_source(k, src, stride, w, h);
	damage_all(k);
	pthread_mutex_unlock(&k->mu);
	pthread_mutex_unlock(lock);
	if (k->running)
		return true;

	if (pthread_create(&k->thread, NULL, present, k) != 0) {
		fprintf(stderr, "kms: present thread: %s\n", strerror(errno));
		return false;
	}
	k->running = true;
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
	struct rect r = rect_clip((struct rect){ k->ox + x, k->oy + y, k->ox + x + w, k->oy + y + h },
	                          (struct rect){ 0, 0, k->w, k->h });
	if (!rect_empty(r)) {
		region_add(&k->buf[0].damage, r);
		region_add(&k->buf[1].damage, r);
		k->dirty = true;
		pthread_cond_signal(&k->cv);
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

/* hw_cursor puts the image on the cursor plane. Called with mu. */
static bool hw_cursor(struct kms* k)
{
	struct buffer* c = &k->cur[k->cur_next];
	memset(c->map, 0, c->size);
	for (int y = 0; y < k->ih; y++)
		memcpy(c->map + (size_t)y * c->pitch, k->img + (size_t)y * k->iw, (size_t)k->iw * 4);
	if (drmModeSetCursor2(k->fd, k->crtc, c->handle, k->cap_w, k->cap_h, k->hx, k->hy) < 0 &&
	    drmModeSetCursor(k->fd, k->crtc, c->handle, k->cap_w, k->cap_h) < 0) {
		fprintf(stderr, "kms: no hardware cursor (%s), drawing the pointer\n", strerror(errno));
		k->hw = false;
		return false;
	}
	k->cur_next ^= 1;
	drmModeMoveCursor(k->fd, k->crtc, k->px - k->hx, k->py - k->hy);
	return true;
}

/* sw_changed asks for a frame when the software pointer moved or changed. */
static void sw_changed(struct kms* k, struct rect before)
{
	struct rect after = pointer_rect(k);
	if (rect_empty(before) && rect_empty(after))
		return;
	k->dirty = true;
	pthread_cond_signal(&k->cv);
}

void kms_cursor(struct kms* k, const uint32_t* argb, int w, int h, int hx, int hy)
{
	pthread_mutex_lock(&k->mu);
	struct rect before = pointer_rect(k);
	if (!argb || w <= 0 || h <= 0) {
		k->visible = false;
		if (k->on_hw)
			drmModeSetCursor(k->fd, k->crtc, 0, 0, 0);
		k->on_hw = false;
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
		bool was_hw = k->on_hw;
		k->on_hw = k->shown && fits_hw(k) && hw_cursor(k);
		if (was_hw && !k->on_hw)
			drmModeSetCursor(k->fd, k->crtc, 0, 0, 0);
	}
	sw_changed(k, before);
	pthread_mutex_unlock(&k->mu);
}

void kms_cursor_move(struct kms* k, int x, int y)
{
	pthread_mutex_lock(&k->mu);
	struct rect before = pointer_rect(k);
	k->px = x;
	k->py = y;
	if (k->on_hw)
		drmModeMoveCursor(k->fd, k->crtc, x - k->hx, y - k->hy);
	else
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
	if (k->running) {
		pthread_mutex_lock(&k->mu);
		k->stop = true;
		pthread_cond_signal(&k->cv);
		pthread_mutex_unlock(&k->mu);
		pthread_join(k->thread, NULL);
	}
	if (k->fd >= 0) {
		if (k->on_hw)
			drmModeSetCursor(k->fd, k->crtc, 0, 0, 0);
		for (int i = 0; i < 2; i++) {
			free_buffer(k, &k->buf[i]);
			free_buffer(k, &k->cur[i]);
		}
		/* Closing the device hands the display back to the console's
		 * framebuffer, which is where the kiosk's own screens are. */
		drmDropMaster(k->fd);
		close(k->fd);
	}
	free(k->img);
	pthread_mutex_destroy(&k->mu);
	pthread_cond_destroy(&k->cv);
	free(k);
}
