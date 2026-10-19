/*
 * yuv.c — a faster YUV 4:2:0 to BGRX conversion for FreeRDP's H.264 path.
 *
 * Every H.264 frame is converted from the decoder's YUV planes to the
 * desktop's BGRX pixels. FreeRDP's SSE4.1 routine works on four pixels at a
 * time in 32-bit lanes and reads the destination back to keep its alpha;
 * measured on a full-screen 120 fps stream it took more of the engine's CPU
 * (38%) than decoding the H.264 itself (21%). This one does the same integer
 * arithmetic, bit for bit (see yuv_test.c), in 16-bit lanes: 32 pixels a step
 * with AVX2, 16 with SSE2 on processors without it, and never reads the
 * destination.
 *
 * FreeRDP's arithmetic (prim_internal.h), full-range BT.709, with D = U - 128
 * and E = V - 128:
 *   R = clip((256 Y + 403 E) >> 8)
 *   G = clip((256 Y - 48 D - 120 E) >> 8)
 *   B = clip((256 Y + 475 D) >> 8)
 * Since 256 Y is a multiple of 256 the shift distributes: R = Y + floor(403 E
 * / 256), and so on. With E scaled by 256 that floor is one mulhi; the G term
 * fits 16 bits as it is.
 */
#include <freerdp/codec/color.h>
#include <freerdp/primitives.h>

#include <immintrin.h>
#include <stdint.h>
#include <stdio.h>
#include <time.h>

#include "yuv.h"

static fn_YUV420ToRGB_8u_P3AC4R_t freerdp_yuv420;

static inline uint8_t clip(int v)
{
	return (uint8_t)(v < 0 ? 0 : v > 255 ? 255 : v);
}

/* tail converts pixels [x, w) of one row. */
static inline void tail(const uint8_t* y, const uint8_t* u, const uint8_t* v, uint8_t* dst, int x,
                        int w)
{
	for (; x < w; x++) {
		const int Y = y[x], D = u[x / 2] - 128, E = v[x / 2] - 128;
		dst[4 * x + 0] = clip(Y + ((475 * D) >> 8));
		dst[4 * x + 1] = clip(Y + ((-48 * D - 120 * E) >> 8));
		dst[4 * x + 2] = clip(Y + ((403 * E) >> 8));
		dst[4 * x + 3] = 0xff;
	}
}

static void row_sse2(const uint8_t* y, const uint8_t* u, const uint8_t* v, uint8_t* dst, int w)
{
	const __m128i zero = _mm_setzero_si128(), c128 = _mm_set1_epi16(128),
	              c403 = _mm_set1_epi16(403), c475 = _mm_set1_epi16(475),
	              cm48 = _mm_set1_epi16(-48), cm120 = _mm_set1_epi16(-120),
	              alpha = _mm_set1_epi8((char)0xff);
	int x = 0;
	for (; x + 16 <= w; x += 16) {
		const __m128i Y = _mm_loadu_si128((const __m128i*)(y + x));
		const __m128i U = _mm_loadl_epi64((const __m128i*)(u + x / 2));
		const __m128i V = _mm_loadl_epi64((const __m128i*)(v + x / 2));
		const __m128i ylo = _mm_unpacklo_epi8(Y, zero), yhi = _mm_unpackhi_epi8(Y, zero);
		const __m128i D = _mm_sub_epi16(_mm_unpacklo_epi8(U, zero), c128);
		const __m128i E = _mm_sub_epi16(_mm_unpacklo_epi8(V, zero), c128);
		/* Each chroma sample covers two pixels. */
		const __m128i Dlo = _mm_unpacklo_epi16(D, D), Dhi = _mm_unpackhi_epi16(D, D);
		const __m128i Elo = _mm_unpacklo_epi16(E, E), Ehi = _mm_unpackhi_epi16(E, E);

		const __m128i rlo = _mm_add_epi16(ylo, _mm_mulhi_epi16(_mm_slli_epi16(Elo, 8), c403));
		const __m128i rhi = _mm_add_epi16(yhi, _mm_mulhi_epi16(_mm_slli_epi16(Ehi, 8), c403));
		const __m128i blo = _mm_add_epi16(ylo, _mm_mulhi_epi16(_mm_slli_epi16(Dlo, 8), c475));
		const __m128i bhi = _mm_add_epi16(yhi, _mm_mulhi_epi16(_mm_slli_epi16(Dhi, 8), c475));
		/* floor((-48 D - 120 E) / 256): the sum is negated before the shift,
		 * not after, or negative values round the wrong way. */
		const __m128i glo = _mm_add_epi16(
		    ylo, _mm_srai_epi16(_mm_add_epi16(_mm_mullo_epi16(Dlo, cm48), _mm_mullo_epi16(Elo, cm120)),
		                        8));
		const __m128i ghi = _mm_add_epi16(
		    yhi, _mm_srai_epi16(_mm_add_epi16(_mm_mullo_epi16(Dhi, cm48), _mm_mullo_epi16(Ehi, cm120)),
		                        8));
		const __m128i R = _mm_packus_epi16(rlo, rhi), G = _mm_packus_epi16(glo, ghi),
		              B = _mm_packus_epi16(blo, bhi);

		const __m128i bglo = _mm_unpacklo_epi8(B, G), bghi = _mm_unpackhi_epi8(B, G);
		const __m128i ralo = _mm_unpacklo_epi8(R, alpha), rahi = _mm_unpackhi_epi8(R, alpha);
		__m128i* out = (__m128i*)(dst + 4 * x);
		_mm_storeu_si128(out + 0, _mm_unpacklo_epi16(bglo, ralo));
		_mm_storeu_si128(out + 1, _mm_unpackhi_epi16(bglo, ralo));
		_mm_storeu_si128(out + 2, _mm_unpacklo_epi16(bghi, rahi));
		_mm_storeu_si128(out + 3, _mm_unpackhi_epi16(bghi, rahi));
	}
	tail(y, u, v, dst, x, w);
}

__attribute__((target("avx2"))) static void row_avx2(const uint8_t* y, const uint8_t* u,
                                                     const uint8_t* v, uint8_t* dst, int w)
{
	const __m256i zero = _mm256_setzero_si256(), c128 = _mm256_set1_epi16(128),
	              c403 = _mm256_set1_epi16(403), c475 = _mm256_set1_epi16(475),
	              cm48 = _mm256_set1_epi16(-48), cm120 = _mm256_set1_epi16(-120),
	              alpha = _mm256_set1_epi8((char)0xff);
	int x = 0;
	for (; x + 32 <= w; x += 32) {
		/* unpack works within each 128-bit lane: ylo holds pixels 0-7 and
		 * 16-23, yhi 8-15 and 24-31. The chroma, widened in order and then
		 * doubled with the same unpack, lands in the same places, and packus
		 * puts the 32 results back in order. */
		const __m256i Y = _mm256_loadu_si256((const __m256i*)(y + x));
		const __m256i ylo = _mm256_unpacklo_epi8(Y, zero), yhi = _mm256_unpackhi_epi8(Y, zero);
		const __m256i D = _mm256_sub_epi16(
		    _mm256_cvtepu8_epi16(_mm_loadu_si128((const __m128i*)(u + x / 2))), c128);
		const __m256i E = _mm256_sub_epi16(
		    _mm256_cvtepu8_epi16(_mm_loadu_si128((const __m128i*)(v + x / 2))), c128);
		const __m256i Dlo = _mm256_unpacklo_epi16(D, D), Dhi = _mm256_unpackhi_epi16(D, D);
		const __m256i Elo = _mm256_unpacklo_epi16(E, E), Ehi = _mm256_unpackhi_epi16(E, E);

		const __m256i rlo =
		    _mm256_add_epi16(ylo, _mm256_mulhi_epi16(_mm256_slli_epi16(Elo, 8), c403));
		const __m256i rhi =
		    _mm256_add_epi16(yhi, _mm256_mulhi_epi16(_mm256_slli_epi16(Ehi, 8), c403));
		const __m256i blo =
		    _mm256_add_epi16(ylo, _mm256_mulhi_epi16(_mm256_slli_epi16(Dlo, 8), c475));
		const __m256i bhi =
		    _mm256_add_epi16(yhi, _mm256_mulhi_epi16(_mm256_slli_epi16(Dhi, 8), c475));
		const __m256i glo = _mm256_add_epi16(
		    ylo, _mm256_srai_epi16(
		             _mm256_add_epi16(_mm256_mullo_epi16(Dlo, cm48), _mm256_mullo_epi16(Elo, cm120)),
		             8));
		const __m256i ghi = _mm256_add_epi16(
		    yhi, _mm256_srai_epi16(
		             _mm256_add_epi16(_mm256_mullo_epi16(Dhi, cm48), _mm256_mullo_epi16(Ehi, cm120)),
		             8));
		const __m256i R = _mm256_packus_epi16(rlo, rhi), G = _mm256_packus_epi16(glo, ghi),
		              B = _mm256_packus_epi16(blo, bhi);

		/* Lane-wise again: o0 holds pixels 0-3 and 16-19, o1 4-7 and 20-23,
		 * o2 8-11 and 24-27, o3 12-15 and 28-31. */
		const __m256i bglo = _mm256_unpacklo_epi8(B, G), bghi = _mm256_unpackhi_epi8(B, G);
		const __m256i ralo = _mm256_unpacklo_epi8(R, alpha), rahi = _mm256_unpackhi_epi8(R, alpha);
		const __m256i o0 = _mm256_unpacklo_epi16(bglo, ralo), o1 = _mm256_unpackhi_epi16(bglo, ralo);
		const __m256i o2 = _mm256_unpacklo_epi16(bghi, rahi), o3 = _mm256_unpackhi_epi16(bghi, rahi);
		__m256i* out = (__m256i*)(dst + 4 * x);
		_mm256_storeu_si256(out + 0, _mm256_permute2x128_si256(o0, o1, 0x20));
		_mm256_storeu_si256(out + 1, _mm256_permute2x128_si256(o2, o3, 0x20));
		_mm256_storeu_si256(out + 2, _mm256_permute2x128_si256(o0, o1, 0x31));
		_mm256_storeu_si256(out + 3, _mm256_permute2x128_si256(o2, o3, 0x31));
	}
	tail(y, u, v, dst, x, w);
}

static void (*row)(const uint8_t*, const uint8_t*, const uint8_t*, uint8_t*, int) = row_sse2;

static _Atomic uint64_t converted, spent_ns;

static uint64_t now_ns(void)
{
	struct timespec t;
	clock_gettime(CLOCK_MONOTONIC, &t);
	return (uint64_t)t.tv_sec * 1000000000u + (uint64_t)t.tv_nsec;
}

pstatus_t sc_yuv420_to_bgrx(const BYTE* WINPR_RESTRICT pSrc[3], const UINT32 srcStep[3],
                            BYTE* WINPR_RESTRICT pDst, UINT32 dstStep, UINT32 DstFormat,
                            const prim_size_t* WINPR_RESTRICT roi)
{
	if (DstFormat != PIXEL_FORMAT_BGRX32 && DstFormat != PIXEL_FORMAT_BGRA32)
		return freerdp_yuv420(pSrc, srcStep, pDst, dstStep, DstFormat, roi);
	const uint64_t t0 = now_ns();
	for (UINT32 y = 0; y < roi->height; y++)
		row(pSrc[0] + (size_t)y * srcStep[0], pSrc[1] + (size_t)(y / 2) * srcStep[1],
		    pSrc[2] + (size_t)(y / 2) * srcStep[2], pDst + (size_t)y * dstStep, (int)roi->width);
	spent_ns += now_ns() - t0;
	converted += (uint64_t)roi->width * roi->height;
	return PRIMITIVES_SUCCESS;
}

void sc_yuv_stats(uint64_t* pixels, uint64_t* ns)
{
	*pixels = converted;
	*ns = spent_ns;
}

void sc_yuv_install(void)
{
	primitives_t* p = primitives_get();
	if (!p || freerdp_yuv420)
		return;
	__builtin_cpu_init();
	if (__builtin_cpu_supports("avx2"))
		row = row_avx2;
	freerdp_yuv420 = p->YUV420ToRGB_8u_P3AC4R;
	p->YUV420ToRGB_8u_P3AC4R = sc_yuv420_to_bgrx;
	fprintf(stderr, "sc-rdp: YUV 4:2:0 conversion %s\n", row == row_avx2 ? "AVX2" : "SSE2");
}

int sc_yuv_use_avx2(int on)
{
	__builtin_cpu_init();
	row = on && __builtin_cpu_supports("avx2") ? row_avx2 : row_sse2;
	return row == row_avx2;
}
