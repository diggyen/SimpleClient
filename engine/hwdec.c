/*
 * hwdec.c — H.264 on the GPU's own video decoder, where a machine has one
 * that works.
 *
 * FreeRDP decodes H.264 with libavcodec, which can hand the work to the GPU
 * through VA-API and take the pictures back as YUV planes; from there the
 * engine goes on as it does with the CPU's. The VA-API drivers here are
 * Intel's (iHD from Broadwell on, i965 for the GPUs before it) and Mesa's for
 * AMD (radeonsi, and r600 for the Radeons before the HD 7000s). NVIDIA's
 * decoder is reached only through NVIDIA's own driver, which cannot be here:
 * it is built for glibc, and under Secure Boot the kernel would not load it.
 * A machine whose only GPU is an NVIDIA decodes on the CPU.
 *
 * Whether a machine's GPU decodes is found out by having it decode: each
 * render node, Intel's first, then AMD's, is given the two frames of
 * probe264.h, and the first to give their pictures back is the one. An
 * integrated GPU goes first because its pictures are in the machine's memory
 * already, where a card's come over the bus. The trial runs in a child
 * process, so a driver that crashes or hangs costs a session only the trial,
 * and the CPU decodes. The answer holds for the rest of the boot, in
 * /run/sc-hwdec, which the kiosk reads to show it.
 */
#include <dirent.h>
#include <errno.h>
#include <limits.h>
#include <poll.h>
#include <signal.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/wait.h>
#include <time.h>
#include <unistd.h>

#include <libavcodec/avcodec.h>
#include <libavutil/hwcontext.h>
#include <libavutil/hwcontext_vaapi.h>
#include <va/va.h>

#include "hwdec.h"
#include "probe264.h"

/* The answer for this boot, one line: "cpu", or "gpu <render node> <kernel
 * driver> <the VA-API driver's name for itself>". */
static const char* answer_path = "/run/sc-hwdec";

/* How long the trial may take, all GPUs together. A driver that has not
 * answered by then is not going to. */
static const int trial_ms = 15000;

/* expect gives what the test stream's pictures hold at (x, y), as
 * probe264.py drew them. */
static void expect(int x, int y, int* Y, int* U, int* V)
{
	*Y = 16 + x * 219 / 255;
	*U = 64 + y / 2;
	*V = 192 - x / 4;
}

/* check compares a decoded picture, YUV420P or NV12, with the stream's at nine
 * points. The encoder's rounding is a unit or two; a decoder that gets the
 * picture wrong is off by far more. */
static bool check(const AVFrame* f)
{
	if (f->width != PROBE264_W || f->height != PROBE264_H)
		return false;
	static const int at[] = { 5, PROBE264_W / 2 + 3, PROBE264_W - 7 };
	for (int i = 0; i < 3; i++) {
		for (int j = 0; j < 3; j++) {
			int x = at[i], y = at[j], Y, U, V, u, v;
			expect(x, y, &Y, &U, &V);
			int lum = f->data[0][y * f->linesize[0] + x];
			if (f->format == AV_PIX_FMT_NV12) {
				const uint8_t* uv = f->data[1] + y / 2 * f->linesize[1] + x / 2 * 2;
				u = uv[0];
				v = uv[1];
			} else if (f->format == AV_PIX_FMT_YUV420P) {
				u = f->data[1][y / 2 * f->linesize[1] + x / 2];
				v = f->data[2][y / 2 * f->linesize[2] + x / 2];
			} else
				return false;
			if (abs(lum - Y) > 6 || abs(u - U) > 6 || abs(v - V) > 6)
				return false;
		}
	}
	return true;
}

static enum AVPixelFormat pick_vaapi(AVCodecContext* c, const enum AVPixelFormat* fmts)
{
	(void)c;
	for (; *fmts != AV_PIX_FMT_NONE; fmts++)
		if (*fmts == AV_PIX_FMT_VAAPI)
			return *fmts;
	return AV_PIX_FMT_NONE;
}

/* lists reports whether pictures can come off the GPU as fmt. */
static bool lists(AVBufferRef* frames, enum AVPixelFormat fmt)
{
	enum AVPixelFormat* f = NULL;
	bool found = false;
	if (av_hwframe_transfer_get_formats(frames, AV_HWFRAME_TRANSFER_DIRECTION_FROM, &f, 0) == 0)
		for (const enum AVPixelFormat* p = f; *p != AV_PIX_FMT_NONE; p++)
			found = found || *p == fmt;
	av_free(f);
	return found;
}

/* decode decodes the test stream on dev's GPU, or with dev NULL on the CPU,
 * and reports whether both pictures came back right. A GPU's come back as
 * FreeRDP takes them: as YUV420P where the driver lists it, and otherwise, or
 * if that fails, in the GPU's own format, NV12 (see FreeRDP's patch 0006).
 * how says which. */
static bool decode(AVBufferRef* dev, const char** how)
{
	const AVCodec* codec = avcodec_find_decoder(AV_CODEC_ID_H264);
	AVCodecContext* c = codec ? avcodec_alloc_context3(codec) : NULL;
	AVPacket* pkt = av_packet_alloc();
	AVFrame* frame = av_frame_alloc();
	AVFrame* sw = av_frame_alloc();
	int good = 0;
	*how = "YUV420P";
	if (!c || !pkt || !frame || !sw)
		goto out;
	if (dev) {
		c->hw_device_ctx = av_buffer_ref(dev);
		c->get_format = pick_vaapi;
	}
	if (avcodec_open2(c, codec, NULL) < 0)
		goto out;

	/* The two frames, then the end, for the decoder to give up what it
	 * holds. */
	const unsigned char* in[] = { probe264_0, probe264_1, NULL };
	const int size[] = { (int)sizeof(probe264_0), (int)sizeof(probe264_1), 0 };
	for (int i = 0; i < 3; i++) {
		pkt->data = (uint8_t*)in[i];
		pkt->size = size[i];
		if (avcodec_send_packet(c, in[i] ? pkt : NULL) < 0)
			goto out;
		while (avcodec_receive_frame(c, frame) == 0) {
			const AVFrame* pic = frame;
			if (dev) {
				if (frame->format != AV_PIX_FMT_VAAPI)
					goto out;
				int rc = -1;
				if (lists(frame->hw_frames_ctx, AV_PIX_FMT_YUV420P)) {
					sw->format = AV_PIX_FMT_YUV420P;
					rc = av_hwframe_transfer_data(sw, frame, 0);
					*how = "YUV420P";
				}
				if (rc < 0) {
					av_frame_unref(sw);
					rc = av_hwframe_transfer_data(sw, frame, 0);
					*how = "NV12";
				}
				if (rc < 0)
					goto out;
				pic = sw;
			}
			if (!check(pic))
				goto out;
			good++;
			av_frame_unref(sw);
			av_frame_unref(frame);
		}
	}

out:
	av_frame_free(&sw);
	av_frame_free(&frame);
	av_packet_free(&pkt);
	avcodec_free_context(&c);
	return good == 2;
}

/* rank orders the GPUs to try, by kernel driver: 0 for those with no VA-API
 * driver here. */
static int rank(const char* driver)
{
	if (strcmp(driver, "i915") == 0 || strcmp(driver, "xe") == 0)
		return 1;
	if (strcmp(driver, "amdgpu") == 0)
		return 2;
	if (strcmp(driver, "radeon") == 0)
		return 3;
	return 0;
}

struct gpu {
	char node[300]; /* /dev/dri/renderD128 */
	char driver[64];
	int rank;
};

/* gpus lists the GPUs that might decode, best first. */
static int gpus(struct gpu* g, int max)
{
	DIR* d = opendir("/sys/class/drm");
	if (!d)
		return 0;
	int n = 0;
	const struct dirent* e;
	while ((e = readdir(d)) != NULL && n < max) {
		if (strncmp(e->d_name, "renderD", 7) != 0)
			continue;
		char path[300], link[PATH_MAX];
		snprintf(path, sizeof(path), "/sys/class/drm/%s/device/driver", e->d_name);
		ssize_t len = readlink(path, link, sizeof(link) - 1);
		if (len <= 0)
			continue;
		link[len] = '\0';
		const char* driver = strrchr(link, '/');
		driver = driver ? driver + 1 : link;
		if (rank(driver) == 0) {
			fprintf(stderr, "sc-rdp: %s (%s): no VA-API driver for it here\n", e->d_name, driver);
			continue;
		}
		snprintf(g[n].node, sizeof(g[n].node), "/dev/dri/%s", e->d_name);
		snprintf(g[n].driver, sizeof(g[n].driver), "%s", driver);
		g[n].rank = rank(driver);
		n++;
	}
	closedir(d);
	for (int i = 1; i < n; i++) {
		for (int j = i; j > 0; j--) {
			const struct gpu* a = &g[j - 1];
			const struct gpu* b = &g[j];
			if (a->rank < b->rank || (a->rank == b->rank && strcmp(a->node, b->node) <= 0))
				break;
			struct gpu t = g[j];
			g[j] = g[j - 1];
			g[j - 1] = t;
		}
	}
	return n;
}

/* trial has a GPU decode the test stream, and names its VA-API driver. */
static bool trial(const struct gpu* g, char* name, size_t n)
{
	AVBufferRef* dev = NULL;
	int rc = av_hwdevice_ctx_create(&dev, AV_HWDEVICE_TYPE_VAAPI, g->node, NULL, 0);
	if (rc < 0) {
		fprintf(stderr, "sc-rdp: %s (%s): VA-API did not start: %s\n", g->node, g->driver,
		        av_err2str(rc));
		return false;
	}
	const AVVAAPIDeviceContext* va = ((const AVHWDeviceContext*)dev->data)->hwctx;
	const char* vendor = vaQueryVendorString(va->display);
	snprintf(name, n, "%s", vendor && *vendor ? vendor : g->driver);
	name[strcspn(name, "\r\n")] = '\0';

	const char* how;
	bool ok = decode(dev, &how);
	if (ok)
		fprintf(stderr, "sc-rdp: %s (%s): %s decodes H.264, its pictures come as %s\n", g->node,
		        g->driver, name, how);
	else
		fprintf(stderr, "sc-rdp: %s (%s): %s did not decode the test stream right\n", g->node,
		        g->driver, name);
	av_buffer_unref(&dev);
	return ok;
}

/* answer runs the trial, in the child, and writes the answer line. */
static void answer(char* a, size_t n)
{
	struct gpu g[8];
	int count = gpus(g, 8);
	for (int i = 0; i < count; i++) {
		char name[200];
		if (trial(&g[i], name, sizeof(name))) {
			snprintf(a, n, "gpu %s %s %s\n", g[i].node, g[i].driver, name);
			return;
		}
	}
	snprintf(a, n, "cpu\n");
}

/* ask has a child process run the trial, and gives back what it said in
 * trial_ms: nothing if it crashed or hung. */
static void ask(char* line, size_t n)
{
	line[0] = '\0';
	int p[2];
	if (pipe(p) != 0)
		return;
	fflush(NULL);
	pid_t pid = fork();
	if (pid < 0) {
		close(p[0]);
		close(p[1]);
		return;
	}
	if (pid == 0) {
		/* Only the answer's pipe: a child stuck in a driver must not keep
		 * the kiosk's pipes open, or the kiosk would never see the session
		 * end. */
		for (int fd = 3; fd < 1024; fd++)
			if (fd != p[1])
				close(fd);
		char a[600];
		answer(a, sizeof(a));
		ssize_t w = write(p[1], a, strlen(a));
		_exit(w < 0);
	}
	close(p[1]);

	struct timespec t0, t;
	clock_gettime(CLOCK_MONOTONIC, &t0);
	size_t got = 0;
	for (;;) {
		clock_gettime(CLOCK_MONOTONIC, &t);
		long left = trial_ms - ((t.tv_sec - t0.tv_sec) * 1000 + (t.tv_nsec - t0.tv_nsec) / 1000000);
		if (left <= 0) {
			fprintf(stderr, "sc-rdp: the GPU trial took over %d s\n", trial_ms / 1000);
			break;
		}
		struct pollfd pfd = { .fd = p[0], .events = POLLIN };
		int r = poll(&pfd, 1, (int)left);
		if (r < 0 && errno != EINTR)
			break;
		if (r <= 0)
			continue;
		ssize_t k = read(p[0], line + got, n - 1 - got);
		if (k < 0 && errno == EINTR)
			continue;
		if (k <= 0)
			break;
		got += (size_t)k;
		if (got == n - 1)
			break;
	}
	line[got] = '\0';
	close(p[0]);

	/* Whatever it said, it is done. One stuck in its driver may not die at
	 * once, and is not waited for long. */
	kill(pid, SIGKILL);
	for (int i = 0; i < 100; i++) {
		int status;
		pid_t w = waitpid(pid, &status, WNOHANG);
		if (w == pid) {
			if (WIFSIGNALED(status) && WTERMSIG(status) != SIGKILL)
				fprintf(stderr, "sc-rdp: the GPU trial died of signal %d\n", WTERMSIG(status));
			break;
		}
		if (w < 0)
			break;
		usleep(10000);
	}
}

/* save keeps the answer for the rest of the boot. */
static void save(const char* line)
{
	char tmp[64];
	snprintf(tmp, sizeof(tmp), "%s.%d", answer_path, (int)getpid());
	FILE* f = fopen(tmp, "w");
	if (!f)
		return;
	bool ok = fputs(line, f) >= 0;
	ok = fclose(f) == 0 && ok;
	if (!ok || rename(tmp, answer_path) != 0)
		unlink(tmp);
}

bool sc_hwdec(char* node, size_t node_size, char* name, size_t name_size)
{
	const char* off = getenv("SC_HWDEC");
	if (off && strcmp(off, "0") == 0) {
		fprintf(stderr, "sc-rdp: H.264 decoded on the CPU, as SC_HWDEC=0 says\n");
		return false;
	}

	char line[600] = "";
	FILE* f = fopen(answer_path, "r");
	if (f) {
		if (!fgets(line, sizeof(line), f))
			line[0] = '\0';
		fclose(f);
	}
	if (line[0] == '\0') {
		ask(line, sizeof(line));
		if (strncmp(line, "gpu ", 4) != 0)
			snprintf(line, sizeof(line), "cpu\n");
		save(line);
	}

	/* gpu <node> <driver> <name> */
	line[strcspn(line, "\n")] = '\0';
	char* at = strncmp(line, "gpu ", 4) == 0 ? line + 4 : NULL;
	char* driver = at ? strchr(at, ' ') : NULL;
	char* vendor = driver ? strchr(driver + 1, ' ') : NULL;
	if (!vendor) {
		fprintf(stderr, "sc-rdp: H.264 decoded on the CPU\n");
		return false;
	}
	*driver = '\0';
	snprintf(node, node_size, "%s", at);
	snprintf(name, name_size, "%s", vendor + 1);
	fprintf(stderr, "sc-rdp: H.264 decoded on the GPU, by %s (%s)\n", name, node);
	return true;
}

bool sc_hwdec_selftest(void)
{
	const char* how;
	return decode(NULL, &how);
}
