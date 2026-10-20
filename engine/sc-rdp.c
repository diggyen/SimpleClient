/*
 * sc-rdp — the kiosk's session engine: FreeRDP, drawing straight to the
 * display through KMS (kms.c), with H.264 (AVC420/AVC444) through the
 * graphics pipeline, sound, microphone, USB and smart cards through FreeRDP's
 * own channels.
 *
 * The kiosk runs one per session and talks to it over two pipes:
 *
 *   fd 3  commands, one a line. First the connection, ending with "start":
 *             arg <one FreeRDP command-line argument>
 *             password <the rest of the line>
 *             start
 *         then input, until "q" or the end of the pipe:
 *             m <x> <y>                  pointer at (x, y) on the screen
 *             b <button> <down> <x> <y>  1 left, 2 right, 3 middle, 8 back, 9 forward
 *             w <notches> <x> <y>        wheel, positive away from the user
 *             h <notches> <x> <y>        horizontal wheel, positive to the right
 *             k <scancode> <extended> <down>
 *             q                          end the session
 *
 *   fd 4  what happens, one a line:
 *             MODE <w> <h> <hz> <display>   the screen, before connecting: with
 *                                           several displays, the desktop they
 *                                           make side by side, "+" between
 *                                           their names
 *             CONNECTED <w> <h>             the session's desktop is showing
 *             POINTER <x> <y>               the server moved the pointer
 *             SESSION                       the server has logged the user
 *                                           on (see sc_session)
 *             ERROR <class> <name> <text>   could not connect; class is one
 *                                           of auth, unreachable, refused,
 *                                           tls, display, other
 *             END <logoff|lost|quit>        the session is over
 *
 * stdout and stderr are the log. A password never goes on the command line,
 * where any process could read it.
 */
#include <errno.h>
#include <fcntl.h>
#include <pthread.h>
#include <signal.h>
#include <stdarg.h>
#include <stdatomic.h>
#include <stdbool.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/resource.h>
#include <time.h>
#include <unistd.h>

#include <freerdp/channels/channels.h>
#include <freerdp/client.h>
#include <freerdp/client/cmdline.h>
#include <freerdp/codec/color.h>
#include <freerdp/error.h>
#include <freerdp/freerdp.h>
#include <freerdp/gdi/gdi.h>
#include <freerdp/graphics.h>
#include <freerdp/input.h>
#include <freerdp/scancode.h>
#include <freerdp/session.h>
#include <freerdp/settings.h>
#include <winpr/synch.h>

#include "hwdec.h"
#include "kms.h"
#include "yuv.h"

/* ── Status ──────────────────────────────────────────────────────────────── */

static FILE* status;
static pthread_mutex_t status_mu = PTHREAD_MUTEX_INITIALIZER;

static void say(const char* fmt, ...)
{
	va_list ap;
	pthread_mutex_lock(&status_mu);
	va_start(ap, fmt);
	vfprintf(status, fmt, ap);
	va_end(ap);
	fputc('\n', status);
	fflush(status);
	pthread_mutex_unlock(&status_mu);
}

/* ── Commands ────────────────────────────────────────────────────────────── */

/* A line reader over fd 3. The connection lines and the input that follows
 * come through the same buffer, so nothing read ahead is lost. */
struct lines {
	int fd;
	char buf[8192];
	size_t start, end;
};

/* next_line returns the next line without its newline, or NULL at the end of
 * the pipe. The line stays valid until the next call. */
static char* next_line(struct lines* l)
{
	for (;;) {
		char* nl = memchr(l->buf + l->start, '\n', l->end - l->start);
		if (nl) {
			char* line = l->buf + l->start;
			*nl = '\0';
			l->start = (size_t)(nl - l->buf) + 1;
			return line;
		}
		if (l->start > 0) {
			memmove(l->buf, l->buf + l->start, l->end - l->start);
			l->end -= l->start;
			l->start = 0;
		}
		if (l->end == sizeof(l->buf)) /* a line too long to be ours */
			l->end = 0;
		ssize_t n = read(l->fd, l->buf + l->end, sizeof(l->buf) - l->end);
		if (n < 0 && errno == EINTR)
			continue;
		if (n <= 0)
			return NULL;
		l->end += (size_t)n;
	}
}

/* line_ready reports whether a whole line is already buffered. */
static bool line_ready(const struct lines* l)
{
	return memchr(l->buf + l->start, '\n', l->end - l->start) != NULL;
}

/* ── The context ─────────────────────────────────────────────────────────── */

typedef struct {
	rdpClientContext common;
	struct kms* kms;
	/* Guards the GDI's frame buffer: held while FreeRDP draws a frame and
	 * while kms.c copies from it. Recursive, because FreeRDP can resize the
	 * buffer from inside a frame. */
	pthread_mutex_t lock;
	bool quit;
	unsigned frames; /* frames FreeRDP drew, for the statistics */
} scContext;

/* paint_depth is how deep this thread is in BeginPaint. FreeRDP also calls
 * EndPaint without a BeginPaint (gdi_resize does), and that must not unlock. */
static __thread int paint_depth;

static BOOL sc_begin_paint(rdpContext* context)
{
	scContext* sc = (scContext*)context;
	pthread_mutex_lock(&sc->lock);
	paint_depth++;
	return TRUE;
}

static BOOL sc_end_paint(rdpContext* context)
{
	scContext* sc = (scContext*)context;
	if (paint_depth == 0)
		return TRUE;
	rdpGdi* gdi = context->gdi;
	if (gdi && gdi->primary && gdi->primary->hdc) {
		HGDI_WND hwnd = gdi->primary->hdc->hwnd;
		if (hwnd && hwnd->invalid && !hwnd->invalid->null && hwnd->ninvalid > 0) {
			for (INT32 i = 0; i < hwnd->ninvalid; i++) {
				const GDI_RGN* r = &hwnd->cinvalid[i];
				kms_damage(sc->kms, r->x, r->y, r->w, r->h);
			}
			sc->frames++;
		}
	}
	paint_depth--;
	pthread_mutex_unlock(&sc->lock);
	return TRUE;
}

static BOOL sc_desktop_resize(rdpContext* context)
{
	scContext* sc = (scContext*)context;
	rdpGdi* gdi = context->gdi;
	rdpSettings* s = context->settings;
	pthread_mutex_lock(&sc->lock);
	BOOL ok = gdi_resize(gdi, freerdp_settings_get_uint32(s, FreeRDP_DesktopWidth),
	                     freerdp_settings_get_uint32(s, FreeRDP_DesktopHeight));
	if (ok)
		kms_source(sc->kms, gdi->primary_buffer, (int)gdi->stride, gdi->width, gdi->height);
	pthread_mutex_unlock(&sc->lock);
	fprintf(stderr, "sc-rdp: desktop %dx%d\n", gdi->width, gdi->height);
	return ok;
}

/* ── The pointer ─────────────────────────────────────────────────────────── */

typedef struct {
	rdpPointer pointer;
	uint32_t* argb;
} scPointer;

static BOOL sc_pointer_new(rdpContext* context, rdpPointer* pointer)
{
	scPointer* p = (scPointer*)pointer;
	if (!pointer->width || !pointer->height || !context->gdi)
		return FALSE;
	p->argb = calloc((size_t)pointer->width * pointer->height, 4);
	if (!p->argb)
		return FALSE;
	/* BGRA32 is FreeRDP's name for the bytes B, G, R, A: ARGB8888 in a
	 * little-endian word, as KMS takes a cursor. */
	if (!freerdp_image_copy_from_pointer_data((BYTE*)p->argb, PIXEL_FORMAT_BGRA32, 0, 0, 0,
	                                          pointer->width, pointer->height,
	                                          pointer->xorMaskData, pointer->lengthXorMask,
	                                          pointer->andMaskData, pointer->lengthAndMask,
	                                          pointer->xorBpp, &context->gdi->palette)) {
		free(p->argb);
		p->argb = NULL;
		return FALSE;
	}
	return TRUE;
}

static void sc_pointer_free(rdpContext* context, rdpPointer* pointer)
{
	(void)context;
	scPointer* p = (scPointer*)pointer;
	free(p->argb);
	p->argb = NULL;
}

static BOOL sc_pointer_set(rdpContext* context, rdpPointer* pointer)
{
	scPointer* p = (scPointer*)pointer;
	if (p->argb)
		kms_cursor(((scContext*)context)->kms, p->argb, (int)pointer->width, (int)pointer->height,
		           (int)pointer->xPos, (int)pointer->yPos);
	return TRUE;
}

static BOOL sc_pointer_set_null(rdpContext* context)
{
	kms_cursor(((scContext*)context)->kms, NULL, 0, 0, 0, 0);
	return TRUE;
}

/* The arrow for when the server asks for the system's default pointer. */
static const char* const arrow_art[] = {
	"X           ", "XX          ", "X.X         ", "X..X        ", "X...X       ",
	"X....X      ", "X.....X     ", "X......X    ", "X.......X   ", "X........X  ",
	"X.........X ", "X......XXXXX", "X...X..X    ", "X..XX..X    ", "X.X  X..X   ",
	"XX   X..X   ", "X     X..X  ", "      X..X  ", "       XX   ",
};
#define ARROW_W 12
#define ARROW_H 19
static uint32_t arrow[ARROW_W * ARROW_H];

static BOOL sc_pointer_set_default(rdpContext* context)
{
	if (!arrow[0])
		for (int y = 0; y < ARROW_H; y++)
			for (int x = 0; x < ARROW_W; x++)
				arrow[y * ARROW_W + x] = arrow_art[y][x] == 'X'   ? 0xff000000u
				                         : arrow_art[y][x] == '.' ? 0xffffffffu
				                                                  : 0;
	kms_cursor(((scContext*)context)->kms, arrow, ARROW_W, ARROW_H, 0, 0);
	return TRUE;
}

static BOOL sc_pointer_set_position(rdpContext* context, UINT32 x, UINT32 y)
{
	scContext* sc = (scContext*)context;
	int ox, oy;
	kms_origin(sc->kms, &ox, &oy);
	kms_cursor_move(sc->kms, ox + (int)x, oy + (int)y);
	/* The kiosk tracks the pointer too: it has to follow, or the next move
	 * of the mouse puts the pointer back where it was. */
	say("POINTER %d %d", ox + (int)x, oy + (int)y);
	return TRUE;
}

/* ── Connecting ──────────────────────────────────────────────────────────── */

static BOOL sc_pre_connect(freerdp* instance)
{
	rdpSettings* s = instance->context->settings;
	if (!freerdp_settings_set_uint32(s, FreeRDP_OsMajorType, OSMAJORTYPE_UNIX) ||
	    !freerdp_settings_set_uint32(s, FreeRDP_OsMinorType, OSMINORTYPE_NATIVE_XSERVER))
		return FALSE;
	if (PubSub_SubscribeChannelConnected(instance->context->pubSub,
	                                     freerdp_client_OnChannelConnectedEventHandler) < 0 ||
	    PubSub_SubscribeChannelDisconnected(instance->context->pubSub,
	                                        freerdp_client_OnChannelDisconnectedEventHandler) < 0)
		return FALSE;
	return TRUE;
}

static BOOL sc_post_connect(freerdp* instance)
{
	rdpContext* context = instance->context;
	scContext* sc = (scContext*)context;

	if (!context->gdi && !gdi_init(instance, PIXEL_FORMAT_BGRX32))
		return FALSE;

	rdpPointer pointer = { 0 };
	pointer.size = sizeof(scPointer);
	pointer.New = sc_pointer_new;
	pointer.Free = sc_pointer_free;
	pointer.Set = sc_pointer_set;
	pointer.SetNull = sc_pointer_set_null;
	pointer.SetDefault = sc_pointer_set_default;
	pointer.SetPosition = sc_pointer_set_position;
	graphics_register_pointer(context->graphics, &pointer);

	context->update->BeginPaint = sc_begin_paint;
	context->update->EndPaint = sc_end_paint;
	context->update->DesktopResize = sc_desktop_resize;

	rdpGdi* gdi = context->gdi;
	if (!kms_start(sc->kms, gdi->primary_buffer, (int)gdi->stride, gdi->width, gdi->height,
	               &sc->lock))
		return FALSE;
	sc_pointer_set_default(context);
	say("CONNECTED %d %d", gdi->width, gdi->height);
	return TRUE;
}

static void sc_post_disconnect(freerdp* instance)
{
	scContext* sc = (scContext*)instance->context;
	PubSub_UnsubscribeChannelConnected(instance->context->pubSub,
	                                   freerdp_client_OnChannelConnectedEventHandler);
	PubSub_UnsubscribeChannelDisconnected(instance->context->pubSub,
	                                      freerdp_client_OnChannelDisconnectedEventHandler);
	/* Stop showing the buffer before it is freed. */
	pthread_mutex_lock(&sc->lock);
	kms_source(sc->kms, NULL, 0, 0, 0);
	pthread_mutex_unlock(&sc->lock);
	gdi_free(instance);
}

/* Nobody is at a terminal to answer FreeRDP's questions. Credentials are what
 * the kiosk passed; without them a server that allows it shows its own login
 * screen, and one that insists on NLA is refused. */
static BOOL sc_authenticate(freerdp* instance, char** username, char** password, char** domain,
                            rdp_auth_reason reason)
{
	(void)instance;
	(void)password;
	(void)domain;
	if (reason == AUTH_NLA && (!username || !*username || !**username))
		return FALSE;
	return TRUE;
}

/* The kiosk connects by address to the servers of its own network, which
 * almost never have a certificate a client could check. */
static DWORD sc_verify_certificate(freerdp* instance, const char* host, UINT16 port,
                                   const char* common_name, const char* subject,
                                   const char* issuer, const char* fingerprint, DWORD flags)
{
	(void)instance;
	(void)common_name;
	(void)subject;
	(void)issuer;
	(void)flags;
	fprintf(stderr, "sc-rdp: accepting the certificate of %s:%u (%s)\n", host, port, fingerprint);
	return 2; /* accept for this session */
}

static DWORD sc_verify_changed_certificate(freerdp* instance, const char* host, UINT16 port,
                                           const char* common_name, const char* subject,
                                           const char* issuer, const char* fingerprint,
                                           const char* old_subject, const char* old_issuer,
                                           const char* old_fingerprint, DWORD flags)
{
	(void)old_subject;
	(void)old_issuer;
	(void)old_fingerprint;
	return sc_verify_certificate(instance, host, port, common_name, subject, issuer, fingerprint,
	                             flags);
}

static BOOL sc_choose_smartcard(freerdp* instance, SmartcardCertInfo** cert_list, DWORD count,
                                DWORD* choice, BOOL gateway)
{
	(void)instance;
	(void)cert_list;
	(void)gateway;
	*choice = 0;
	return count > 0;
}

static BOOL sc_gateway_message(freerdp* instance, UINT32 type, BOOL display, BOOL consent,
                               size_t length, const WCHAR* message)
{
	(void)instance;
	(void)type;
	(void)display;
	(void)consent;
	(void)length;
	(void)message;
	return TRUE;
}

/* sc_session says SESSION once the server has logged the user on. Windows
 * tells (Save Session Info). xrdp does not, and with require_credentials it
 * turns a wrong password down by hanging up a moment after the session came
 * up, the same hang-up as a logoff. But its channel server (chansrv) runs only
 * in a session someone has logged on to, and opens the clipboard and sound
 * channels as it starts: a word from the server on either is the session's. */
static atomic_bool session_said;
static pReceiveChannelData channel_data;

static void sc_session(void)
{
	if (!atomic_exchange(&session_said, true))
		say("SESSION");
}

static BOOL sc_session_info(rdpContext* context, UINT32 type, const void* data)
{
	(void)context;
	(void)data;
	/* The extended one carries an auto-reconnect cookie or a logon error. */
	if (type != INFO_TYPE_LOGON_EXTENDED_INF)
		sc_session();
	return TRUE;
}

static BOOL sc_channel_data(freerdp* instance, UINT16 id, const BYTE* data, size_t size,
                            UINT32 flags, size_t total)
{
	const char* name = freerdp_channels_get_name_by_id(instance, id);
	if (name && (strcmp(name, "cliprdr") == 0 || strcmp(name, "rdpsnd") == 0))
		sc_session();
	return channel_data(instance, id, data, size, flags, total);
}

static int sc_logon_error(freerdp* instance, UINT32 data, UINT32 type)
{
	(void)instance;
	fprintf(stderr, "sc-rdp: logon: %s [%s]\n", freerdp_get_logon_error_info_data(data),
	        freerdp_get_logon_error_info_type(type));
	return 1;
}

static BOOL sc_client_new(freerdp* instance, rdpContext* context)
{
	scContext* sc = (scContext*)context;
	pthread_mutexattr_t a;
	pthread_mutexattr_init(&a);
	pthread_mutexattr_settype(&a, PTHREAD_MUTEX_RECURSIVE);
	pthread_mutex_init(&sc->lock, &a);
	pthread_mutexattr_destroy(&a);

	instance->PreConnect = sc_pre_connect;
	instance->PostConnect = sc_post_connect;
	instance->PostDisconnect = sc_post_disconnect;
	instance->AuthenticateEx = sc_authenticate;
	instance->VerifyCertificateEx = sc_verify_certificate;
	instance->VerifyChangedCertificateEx = sc_verify_changed_certificate;
	instance->ChooseSmartcard = sc_choose_smartcard;
	instance->PresentGatewayMessage = sc_gateway_message;
	instance->LogonErrorInfo = sc_logon_error;
	context->update->SaveSessionInfo = sc_session_info;
	channel_data = instance->ReceiveChannelData;
	instance->ReceiveChannelData = sc_channel_data;
	return TRUE;
}

static void sc_client_free(freerdp* instance, rdpContext* context)
{
	(void)instance;
	pthread_mutex_destroy(&((scContext*)context)->lock);
}

static BOOL sc_global_init(void) { return TRUE; }
static void sc_global_uninit(void) {}
static int sc_client_start(rdpContext* context) { (void)context; return 0; }
static int sc_client_stop(rdpContext* context) { (void)context; return 0; }

/* error_class sorts a failed connection into what the kiosk tells the user. */
static const char* error_class(UINT32 e)
{
	switch (e) {
	case FREERDP_ERROR_AUTHENTICATION_FAILED:
	case FREERDP_ERROR_INSUFFICIENT_PRIVILEGES:
	case FREERDP_ERROR_CONNECT_CANCELLED:
	case FREERDP_ERROR_CONNECT_PASSWORD_EXPIRED:
	case FREERDP_ERROR_CONNECT_PASSWORD_MUST_CHANGE:
	case FREERDP_ERROR_CONNECT_ACCOUNT_DISABLED:
	case FREERDP_ERROR_CONNECT_PASSWORD_CERTAINLY_EXPIRED:
	case FREERDP_ERROR_CONNECT_CLIENT_REVOKED:
	case FREERDP_ERROR_CONNECT_LOGON_FAILURE:
	case FREERDP_ERROR_CONNECT_WRONG_PASSWORD:
	case FREERDP_ERROR_CONNECT_ACCESS_DENIED:
	case FREERDP_ERROR_CONNECT_ACCOUNT_RESTRICTION:
	case FREERDP_ERROR_CONNECT_ACCOUNT_LOCKED_OUT:
	case FREERDP_ERROR_CONNECT_ACCOUNT_EXPIRED:
	case FREERDP_ERROR_CONNECT_LOGON_TYPE_NOT_GRANTED:
	case FREERDP_ERROR_CONNECT_NO_OR_MISSING_CREDENTIALS:
	case FREERDP_ERROR_SERVER_INSUFFICIENT_PRIVILEGES:
	case FREERDP_ERROR_SERVER_FRESH_CREDENTIALS_REQUIRED:
		return "auth";
	case FREERDP_ERROR_DNS_ERROR:
	case FREERDP_ERROR_DNS_NAME_NOT_FOUND:
	case FREERDP_ERROR_CONNECT_TRANSPORT_FAILED:
		return "unreachable";
	case FREERDP_ERROR_CONNECT_FAILED:
	case FREERDP_ERROR_SERVER_DENIED_CONNECTION:
		return "refused";
	case FREERDP_ERROR_TLS_CONNECT_FAILED:
	case FREERDP_ERROR_SECURITY_NEGO_CONNECT_FAILED:
		return "tls";
	case FREERDP_ERROR_POST_CONNECT_FAILED:
		return "display";
	}
	return "other";
}

/* end_reason sorts the end of a session that was up: the user or the server
 * ended it (logoff), or the connection died (lost). */
static const char* end_reason(freerdp* instance)
{
	switch (freerdp_error_info(instance)) {
	case ERRINFO_RPC_INITIATED_DISCONNECT:
	case ERRINFO_RPC_INITIATED_LOGOFF:
	case ERRINFO_IDLE_TIMEOUT:
	case ERRINFO_LOGON_TIMEOUT:
	case ERRINFO_DISCONNECTED_BY_OTHER_CONNECTION:
	case ERRINFO_SERVER_DENIED_CONNECTION:
	case ERRINFO_RPC_INITIATED_DISCONNECT_BY_USER:
	case ERRINFO_LOGOFF_BY_USER:
		return "logoff";
	}
	return "lost";
}

/* ── Input ───────────────────────────────────────────────────────────────── */

struct input_args {
	scContext* sc;
	struct lines* lines;
};

/* to_session turns a point on the screen into one on the remote desktop. */
static void to_session(scContext* sc, int x, int y, UINT16* sx, UINT16* sy)
{
	int ox, oy;
	kms_origin(sc->kms, &ox, &oy);
	rdpGdi* gdi = sc->common.context.gdi;
	int w = gdi ? gdi->width : kms_width(sc->kms), h = gdi ? gdi->height : kms_height(sc->kms);
	x -= ox;
	y -= oy;
	*sx = (UINT16)(x < 0 ? 0 : x >= w ? w - 1 : x);
	*sy = (UINT16)(y < 0 ? 0 : y >= h ? h - 1 : y);
}

static void wheel(rdpInput* input, UINT16 base, int notches, UINT16 x, UINT16 y)
{
	for (; notches > 0; notches--)
		freerdp_input_send_mouse_event(input, base | 0x0078, x, y);
	for (; notches < 0; notches++)
		freerdp_input_send_mouse_event(input, base | PTR_FLAGS_WHEEL_NEGATIVE | 0x0088, x, y);
}

/* input reads the kiosk's input. The pointer moves on the screen the moment a
 * move arrives; a burst of moves goes to the server as its last one. */
static void* input(void* arg)
{
	struct input_args* a = arg;
	scContext* sc = a->sc;
	rdpContext* context = &sc->common.context;
	rdpInput* in = context->input;
	bool moved = false;
	int mx = 0, my = 0;

	for (;;) {
		char* line = next_line(a->lines);
		if (!line)
			break;
		int p[4] = { 0 };
		char c = line[0];
		int n = sscanf(line + (c ? 1 : 0), "%d %d %d %d", &p[0], &p[1], &p[2], &p[3]);

		if (c == 'm' && n == 2) {
			mx = p[0];
			my = p[1];
			/* Beside a display shorter than its neighbour the pointer
			 * would be out of sight: it stays on the display, and the
			 * kiosk is told where. */
			if (kms_clamp(sc->kms, &mx, &my))
				say("POINTER %d %d", mx, my);
			kms_cursor_move(sc->kms, mx, my);
			moved = true;
			if (line_ready(a->lines))
				continue;
		}
		if (moved) {
			UINT16 x, y;
			to_session(sc, mx, my, &x, &y);
			freerdp_input_send_mouse_event(in, PTR_FLAGS_MOVE, x, y);
			moved = false;
		}
		UINT16 x, y;
		switch (c) {
		case 'b':
			if (n < 4)
				break;
			to_session(sc, p[2], p[3], &x, &y);
			switch (p[0]) {
			case 1:
			case 2:
			case 3: {
				static const UINT16 buttons[] = { 0, PTR_FLAGS_BUTTON1, PTR_FLAGS_BUTTON2,
					                              PTR_FLAGS_BUTTON3 };
				freerdp_input_send_mouse_event(in, buttons[p[0]] | (p[1] ? PTR_FLAGS_DOWN : 0), x,
				                               y);
				break;
			}
			case 8:
			case 9:
				freerdp_input_send_extended_mouse_event(
				    in, (p[0] == 8 ? PTR_XFLAGS_BUTTON1 : PTR_XFLAGS_BUTTON2) |
				            (p[1] ? PTR_XFLAGS_DOWN : 0),
				    x, y);
				break;
			}
			break;
		case 'w':
		case 'h':
			if (n < 3)
				break;
			to_session(sc, p[1], p[2], &x, &y);
			wheel(in, c == 'w' ? PTR_FLAGS_WHEEL : PTR_FLAGS_HWHEEL, p[0], x, y);
			break;
		case 'k':
			if (n == 3)
				freerdp_input_send_keyboard_event_ex(in, p[2] != 0, FALSE,
				                                     MAKE_RDP_SCANCODE(p[0], p[1]));
			break;
		case 'q':
			sc->quit = true;
			freerdp_abort_connect_context(context);
			return NULL;
		}
	}
	/* The kiosk is gone: so is the session. */
	sc->quit = true;
	freerdp_abort_connect_context(context);
	return NULL;
}

static double cpu_seconds(void)
{
	struct rusage u;
	getrusage(RUSAGE_SELF, &u);
	return (double)u.ru_utime.tv_sec + u.ru_utime.tv_usec / 1e6 + (double)u.ru_stime.tv_sec +
	       u.ru_stime.tv_usec / 1e6;
}

/* stats logs the frame rate every five seconds: frames FreeRDP drew, frames
 * the display showed, and the CPU the engine took doing it (100% is one core). */
static void* stats(void* arg)
{
	scContext* sc = arg;
	unsigned drawn = 0, shown = 0;
	double cpu = cpu_seconds();
	uint64_t px = 0, ns = 0;
	for (;;) {
		sleep(5);
		unsigned d = sc->frames, s = kms_presented(sc->kms);
		double c = cpu_seconds();
		uint64_t p, n;
		sc_yuv_stats(&p, &n);
		if (d != drawn || s != shown)
			fprintf(stderr,
			        "sc-rdp: %.1f fps drawn, %.1f fps shown, cpu %.0f%%, yuv %.0f Mpixel/s in %.0f ms/s\n",
			        (d - drawn) / 5.0, (s - shown) / 5.0, (c - cpu) * 100 / 5.0,
			        (p - px) / 5e6, (n - ns) / 5e6);
		drawn = d;
		shown = s;
		cpu = c;
		px = p;
		ns = n;
	}
	return NULL;
}

/* set_monitors tells the server the displays: it then puts its taskbar, its
 * windows and its full-screen video on one of them, not across the gap. The
 * first is the primary. */
static bool set_monitors(rdpSettings* s, const struct kms* kms)
{
	int n = kms_outputs(kms);
	if (n < 2)
		return true;
	rdpMonitor m[8] = { 0 };
	for (int i = 0; i < n && i < 8; i++) {
		int x, y, w, h;
		kms_output(kms, i, &x, &y, &w, &h);
		m[i].x = x;
		m[i].y = y;
		m[i].width = w;
		m[i].height = h;
		m[i].is_primary = i == 0;
		m[i].orig_screen = (UINT32)i;
		fprintf(stderr, "sc-rdp: monitor %d at %d,%d %dx%d%s\n", i, x, y, w, h,
		        i == 0 ? ", primary" : "");
	}
	return freerdp_settings_set_bool(s, FreeRDP_UseMultimon, TRUE) &&
	       freerdp_settings_set_monitor_def_array_sorted(s, m, (size_t)(n < 8 ? n : 8));
}

/* ── main ────────────────────────────────────────────────────────────────── */

int main(int nargs, char** args)
{
	/* sc-rdp --hwdec runs the GPU trial (hwdec.c) on its own: the kiosk
	 * runs it at start, to show its answer, which sessions then find. */
	if (nargs > 1 && strcmp(args[1], "--hwdec") == 0) {
		char node[300], name[200];
		if (sc_hwdec(node, sizeof(node), name, sizeof(name)))
			printf("gpu %s %s\n", node, name);
		else
			printf("cpu\n");
		return 0;
	}

	signal(SIGPIPE, SIG_IGN);
	/* The kiosk's end of a pipe may be non-blocking, and the flag would
	 * otherwise come along: a read with nothing to read yet is not the end. */
	int fl = fcntl(3, F_GETFL);
	if (fl >= 0)
		fcntl(3, F_SETFL, fl & ~O_NONBLOCK);
	status = fdopen(4, "w");
	if (!status) {
		fprintf(stderr, "sc-rdp: fd 4 is not open\n");
		return 2;
	}

	/* What the kiosk wants: FreeRDP's own arguments, and the password. */
	static struct lines lines = { .fd = 3 };
	char* argv[256];
	int argc = 0;
	char* password = NULL;
	argv[argc++] = "sc-rdp";
	for (;;) {
		char* line = next_line(&lines);
		if (!line) {
			fprintf(stderr, "sc-rdp: no \"start\" on fd 3\n");
			return 2;
		}
		if (strcmp(line, "start") == 0)
			break;
		if (strncmp(line, "arg ", 4) == 0 && argc < 200)
			argv[argc++] = strdup(line + 4);
		else if (strncmp(line, "password ", 9) == 0) {
			free(password);
			password = strdup(line + 9);
		}
	}

	struct kms* kms = kms_open();
	if (!kms) {
		say("ERROR display - no display that KMS can drive");
		return 3;
	}
	say("MODE %d %d %d %s", kms_width(kms), kms_height(kms), kms_refresh(kms), kms_name(kms));

	/* Whether the GPU decodes H.264, found by a trial in a child process:
	 * so before FreeRDP, while this process has only the one thread. */
	char node[300], gpu[200];
	bool hwdec = sc_hwdec(node, sizeof(node), gpu, sizeof(gpu));

	/* The display's own resolution, the graphics pipeline with H.264, and
	 * every visual nicety a LAN has room for. They go first, so an argument
	 * from the kiosk overrides any of them. */
	char size[32];
	snprintf(size, sizeof(size), "/size:%dx%d", kms_width(kms), kms_height(kms));
	char* defaults[] = { size,         "/bpp:32",   "/gfx:AVC444",  "/network:lan",
		                 "/cert:ignore", "+auto-reconnect", "/auto-reconnect-max-retries:20" };
	const int nd = (int)(sizeof(defaults) / sizeof(defaults[0]));
	char* full[256 + 16];
	full[0] = argv[0];
	for (int i = 0; i < nd; i++)
		full[1 + i] = defaults[i];
	for (int i = 1; i < argc; i++)
		full[nd + i] = argv[i];
	int fullc = nd + argc;

	RDP_CLIENT_ENTRY_POINTS ep = { 0 };
	ep.Version = RDP_CLIENT_INTERFACE_VERSION;
	ep.Size = sizeof(RDP_CLIENT_ENTRY_POINTS_V1);
	ep.GlobalInit = sc_global_init;
	ep.GlobalUninit = sc_global_uninit;
	ep.ContextSize = sizeof(scContext);
	ep.ClientNew = sc_client_new;
	ep.ClientFree = sc_client_free;
	ep.ClientStart = sc_client_start;
	ep.ClientStop = sc_client_stop;

	rdpContext* context = freerdp_client_context_new(&ep);
	if (!context) {
		say("ERROR other - FreeRDP did not start");
		kms_close(kms);
		return 1;
	}
	scContext* sc = (scContext*)context;
	sc->kms = kms;
	freerdp* instance = context->instance;

	int rc = freerdp_client_settings_parse_command_line(context->settings, fullc, full, FALSE);
	if (rc != 0) {
		say("ERROR other - bad arguments (%d)", rc);
		freerdp_client_context_free(context);
		kms_close(kms);
		return 2;
	}
	if (password) {
		freerdp_settings_set_string(context->settings, FreeRDP_Password, password);
		memset(password, 0, strlen(password));
		free(password);
	}
	if (!set_monitors(context->settings, kms)) {
		say("ERROR other - the monitor layout was refused");
		freerdp_client_context_free(context);
		kms_close(kms);
		return 2;
	}

	/* FreeRDP has the GPU decode only when its GDI is not the software one,
	 * which is all that setting still changes. */
	if (hwdec) {
		setenv("FREERDP_HWCTX_DEVICE", node, 1);
		freerdp_settings_set_bool(context->settings, FreeRDP_SoftwareGdi, FALSE);
	}

	sc_yuv_install();
	const char* threads = getenv("SC_THREADS");
	if (threads && strcmp(threads, "0") == 0)
		freerdp_settings_set_uint32(context->settings, FreeRDP_ThreadingFlags,
		                            THREADING_FLAGS_DISABLE_THREADS);

	pthread_t th;
	struct input_args ia = { sc, &lines };
	pthread_create(&th, NULL, stats, sc);
	pthread_detach(th);

	if (freerdp_client_start(context) != 0 || !freerdp_connect(instance)) {
		UINT32 e = freerdp_get_last_error(context);
		say("ERROR %s %s %s", error_class(e), freerdp_get_last_error_name(e),
		    freerdp_get_last_error_string(e));
		freerdp_client_stop(context);
		freerdp_client_context_free(context);
		kms_close(kms);
		return 1;
	}

	pthread_create(&th, NULL, input, &ia);
	pthread_detach(th);

	HANDLE handles[MAXIMUM_WAIT_OBJECTS];
	while (!freerdp_shall_disconnect_context(context)) {
		DWORD n = freerdp_get_event_handles(context, handles, ARRAYSIZE(handles));
		if (n == 0)
			break;
		if (WaitForMultipleObjects(n, handles, FALSE, INFINITE) == WAIT_FAILED)
			break;
		if (!freerdp_check_event_handles(context)) {
			if (!sc->quit && client_auto_reconnect(instance))
				continue;
			break;
		}
	}

	const char* why = sc->quit ? "quit" : end_reason(instance);
	freerdp_disconnect(instance);
	freerdp_client_stop(context);
	kms_close(kms);
	say("END %s", why);
	freerdp_client_context_free(context);
	return 0;
}
