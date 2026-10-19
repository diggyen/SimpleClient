/*
 * kms.h — the session's screen, driven through KMS.
 *
 * FreeRDP draws the remote desktop into a buffer in memory. This copies what
 * changed into one of two scanout buffers and flips to it on the display's
 * vertical blank, so a frame is never seen half-drawn (no tearing) and frames
 * reach the screen as fast as the display shows them: 60, 120, 144 a second,
 * whatever the monitor runs at. The pointer is the display's hardware cursor
 * where it has one, moved the moment the mouse moves rather than with the next
 * frame, and drawn into the frames where it has not.
 */
#ifndef SC_KMS_H
#define SC_KMS_H

#include <pthread.h>
#include <stdbool.h>
#include <stdint.h>

struct kms;

/* kms_open takes the first connected display of the first DRM device that has
 * one and picks its mode: the native (preferred) resolution, at the highest
 * refresh rate the display offers there. SC_MODE=WxH or WxH@Hz picks another.
 * The display keeps showing what it shows until kms_start. */
struct kms* kms_open(void);

/* kms_close stops presenting and gives the display back. */
void kms_close(struct kms* k);

int kms_width(const struct kms* k);
int kms_height(const struct kms* k);
int kms_refresh(const struct kms* k); /* Hz, rounded */
const char* kms_name(const struct kms* k);

/* kms_start shows the session: XRGB8888 pixels at src, stride bytes a row,
 * w x h, centred on the screen, which other threads change only while holding
 * lock. The lock must be recursive if a thread that holds it calls back in.
 * Called again, it only switches to the new source. */
bool kms_start(struct kms* k, const uint8_t* src, int stride, int w, int h,
               pthread_mutex_t* lock);

/* kms_source points presentation at a new source after a resize. Call with
 * the source lock held. */
void kms_source(struct kms* k, const uint8_t* src, int stride, int w, int h);

/* kms_damage marks a rectangle of the source as changed. Call with the source
 * lock held, after the change. */
void kms_damage(struct kms* k, int x, int y, int w, int h);

/* kms_origin is where the source's top-left corner is on the screen. */
void kms_origin(struct kms* k, int* x, int* y);

/* kms_cursor sets the pointer's image, w x h premultiplied ARGB8888 (what
 * FreeRDP makes of the server's pointer, as for an X cursor) with its hot spot
 * at (hx, hy), or hides the pointer when argb is NULL. */
void kms_cursor(struct kms* k, const uint32_t* argb, int w, int h, int hx, int hy);

/* kms_cursor_move puts the pointer's hot spot at (x, y) on the screen. */
void kms_cursor_move(struct kms* k, int x, int y);

/* kms_presented counts the frames shown so far. */
unsigned kms_presented(struct kms* k);

#endif
