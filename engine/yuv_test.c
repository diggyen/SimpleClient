/*
 * yuv_test.c — checks yuv.c against FreeRDP's own C conversion, byte for
 * byte, at every width from 1 to 99 and some screen widths, with both its
 * AVX2 and its SSE2 code; checks it writes nothing past a row's end; and times
 * it against FreeRDP's. The build runs it and stops on any difference.
 */
#include <freerdp/codec/color.h>
#include <freerdp/primitives.h>

#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>

#include "yuv.h"

static uint32_t seed = 12345;

static uint8_t rnd(void)
{
	seed = seed * 1103515245u + 12345u;
	return (uint8_t)(seed >> 16);
}

static int check(int w, int h, const char* what)
{
	const UINT32 ys = (UINT32)w + 37, cs = (UINT32)(w + 1) / 2 + 19, ds = (UINT32)w * 4 + 64;
	const int ch = (h + 1) / 2;
	uint8_t *Y = malloc(ys * h), *U = malloc(cs * ch), *V = malloc(cs * ch);
	uint8_t *want = malloc((size_t)ds * h), *got = malloc((size_t)ds * h);
	for (size_t i = 0; i < ys * (size_t)h; i++)
		Y[i] = rnd();
	for (size_t i = 0; i < cs * (size_t)ch; i++) {
		U[i] = rnd();
		V[i] = rnd();
	}
	/* The extremes too, where the clipping is. */
	if (w > 2) {
		Y[0] = 0;
		U[0] = 0;
		V[0] = 255;
		Y[1] = 255;
		U[cs - 1] = 255;
		V[cs - 1] = 0;
	}
	memset(want, 0xaa, (size_t)ds * h);
	memset(got, 0xaa, (size_t)ds * h);

	const BYTE* src[3] = { Y, U, V };
	const UINT32 steps[3] = { ys, cs, cs };
	const prim_size_t roi = { (UINT32)w, (UINT32)h };
	if (primitives_get_generic()->YUV420ToRGB_8u_P3AC4R(src, steps, want, ds, PIXEL_FORMAT_BGRX32,
	                                                     &roi) != PRIMITIVES_SUCCESS ||
	    sc_yuv420_to_bgrx(src, steps, got, ds, PIXEL_FORMAT_BGRX32, &roi) != PRIMITIVES_SUCCESS) {
		fprintf(stderr, "yuv_test: %s %dx%d: conversion failed\n", what, w, h);
		return 1;
	}
	int bad = 0;
	for (int y = 0; y < h && !bad; y++)
		for (int x = 0; x < w; x++) {
			const uint8_t* a = want + (size_t)y * ds + 4 * x;
			const uint8_t* b = got + (size_t)y * ds + 4 * x;
			if (a[0] != b[0] || a[1] != b[1] || a[2] != b[2]) {
				fprintf(stderr,
				        "yuv_test: %s %dx%d: pixel (%d,%d) is %02x%02x%02x, FreeRDP makes %02x%02x%02x\n",
				        what, w, h, x, y, b[2], b[1], b[0], a[2], a[1], a[0]);
				bad = 1;
				break;
			}
		}
	for (int y = 0; y < h && !bad; y++)
		for (UINT32 i = (UINT32)w * 4; i < ds; i++)
			if (got[(size_t)y * ds + i] != 0xaa) {
				fprintf(stderr, "yuv_test: %s %dx%d: wrote past the end of row %d\n", what, w, h, y);
				bad = 1;
				break;
			}
	free(Y);
	free(U);
	free(V);
	free(want);
	free(got);
	return bad;
}

static double mpix(fn_YUV420ToRGB_8u_P3AC4R_t f)
{
	enum { W = 1920, H = 1080, N = 60 };
	uint8_t *Y = malloc(W * H), *U = malloc(W * H / 4), *V = malloc(W * H / 4), *D = malloc(W * H * 4);
	for (size_t i = 0; i < (size_t)W * H; i++)
		Y[i] = rnd();
	for (size_t i = 0; i < (size_t)W * H / 4; i++) {
		U[i] = rnd();
		V[i] = rnd();
	}
	const BYTE* src[3] = { Y, U, V };
	const UINT32 steps[3] = { W, W / 2, W / 2 };
	const prim_size_t roi = { W, H };
	struct timespec a, b;
	clock_gettime(CLOCK_MONOTONIC, &a);
	for (int i = 0; i < N; i++)
		(void)f(src, steps, D, W * 4, PIXEL_FORMAT_BGRX32, &roi);
	clock_gettime(CLOCK_MONOTONIC, &b);
	free(Y);
	free(U);
	free(V);
	free(D);
	return (double)W * H * N / ((b.tv_sec - a.tv_sec) * 1e6 + (b.tv_nsec - a.tv_nsec) / 1e3);
}

int main(void)
{
	fn_YUV420ToRGB_8u_P3AC4R_t freerdp = primitives_get()->YUV420ToRGB_8u_P3AC4R;
	sc_yuv_install();

	int bad = 0;
	static const int screens[] = { 640, 1024, 1280, 1366, 1600, 1919, 1920, 2560, 3840 };
	for (int avx2 = 1; avx2 >= 0; avx2--) {
		const char* what = sc_yuv_use_avx2(avx2) ? "AVX2" : "SSE2";
		for (int w = 1; w < 100; w++)
			bad |= check(w, 1 + w % 5, what);
		for (size_t i = 0; i < sizeof(screens) / sizeof(screens[0]); i++)
			bad |= check(screens[i], 9, what);
	}
	if (bad)
		return 1;

	/* Times at 1080p: FreeRDP's C, FreeRDP's SIMD, and this one's two. */
	printf("yuv_test: identical to FreeRDP; 1080p in Mpixel/s: FreeRDP C %.0f, FreeRDP SIMD %.0f",
	       mpix(primitives_get_generic()->YUV420ToRGB_8u_P3AC4R), mpix(freerdp));
	sc_yuv_use_avx2(0);
	printf(", SSE2 %.0f", mpix(sc_yuv420_to_bgrx));
	if (sc_yuv_use_avx2(1))
		printf(", AVX2 %.0f", mpix(sc_yuv420_to_bgrx));
	printf("\n");
	return 0;
}
