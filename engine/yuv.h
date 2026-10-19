/*
 * yuv.h — see yuv.c.
 */
#ifndef SC_YUV_H
#define SC_YUV_H

#include <freerdp/primitives.h>

/* sc_yuv_install puts sc_yuv420_to_bgrx in FreeRDP's primitives table, in
 * place of its own YUV 4:2:0 to RGB routine, which it still uses for every
 * other destination format. */
void sc_yuv_install(void);

pstatus_t sc_yuv420_to_bgrx(const BYTE* WINPR_RESTRICT pSrc[3], const UINT32 srcStep[3],
                            BYTE* WINPR_RESTRICT pDst, UINT32 dstStep, UINT32 DstFormat,
                            const prim_size_t* WINPR_RESTRICT roi);

/* sc_yuv_use_avx2 picks the AVX2 code (where the processor has it) or the
 * SSE2 code, for testing both; it reports whether AVX2 is in use. */
int sc_yuv_use_avx2(int on);

/* sc_yuv_stats reports the pixels converted so far and the time it took. */
void sc_yuv_stats(uint64_t* pixels, uint64_t* ns);

#endif
