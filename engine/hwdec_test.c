/*
 * hwdec_test.c — the build's check of hwdec.c's test stream and of the check
 * its pictures get, on the CPU (see sc_hwdec_selftest).
 */
#include <stdio.h>

#include "hwdec.h"

int main(void)
{
	if (!sc_hwdec_selftest()) {
		fprintf(stderr, "hwdec_test: the test stream does not decode to its pictures\n");
		return 1;
	}
	printf("hwdec_test: the test stream decodes to its pictures\n");
	return 0;
}
