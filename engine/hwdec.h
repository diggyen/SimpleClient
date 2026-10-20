/*
 * hwdec.h — see hwdec.c.
 */
#ifndef SC_HWDEC_H
#define SC_HWDEC_H

#include <stdbool.h>
#include <stddef.h>

/* sc_hwdec reports whether a GPU here decodes the session's H.264, and if so
 * gives its render node, for FreeRDP, and its VA-API driver's name. */
bool sc_hwdec(char* node, size_t node_size, char* name, size_t name_size);

/* sc_hwdec_selftest decodes the test stream on the CPU and checks its
 * pictures, as a GPU's are checked: the build runs it, so a stream or a check
 * gone wrong cannot leave every GPU looking unable. */
bool sc_hwdec_selftest(void);

#endif
