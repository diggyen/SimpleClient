#!/usr/bin/env python3
"""probe264.py > probe264.h: the H.264 stream hwdec.c has a GPU decode to see
whether it can. Two 256x256 frames, an IDR and a P frame, High profile with
CABAC, as Windows and xrdp's x264 send them. Their pictures are gradients
hwdec.c checks a decoded frame against (see expect() there): luma rising left
to right, Cb rising top to bottom, Cr falling left to right. Needs x264."""
import os
import subprocess
import sys
import tempfile

W = H = 256


def plane(w, h, f):
    return bytes(f(x, y) for y in range(h) for x in range(w))


def nal_units(s):
    """The NAL units of an Annex B stream, without their start codes."""
    starts = []
    i = s.find(b"\x00\x00\x01")
    while i >= 0:
        starts.append(i + 3)
        i = s.find(b"\x00\x00\x01", i + 3)
    units = []
    for k, a in enumerate(starts):
        b = starts[k + 1] - 3 if k + 1 < len(starts) else len(s)
        # A NAL unit ends in a non-zero byte; zeros after it are the next
        # start code's.
        units.append(s[a:b].rstrip(b"\x00"))
    return units


def main():
    y = plane(W, H, lambda x, y: 16 + x * 219 // 255)
    cb = plane(W // 2, H // 2, lambda x, y: 64 + y)
    cr = plane(W // 2, H // 2, lambda x, y: 192 - x // 2)
    with tempfile.TemporaryDirectory() as d:
        raw, out = os.path.join(d, "in.yuv"), os.path.join(d, "out.264")
        with open(raw, "wb") as f:
            f.write((y + cb + cr) * 2)
        subprocess.run(["x264", "--quiet", "--input-res", f"{W}x{H}", "--fps", "30",
                        "--profile", "high", "--qp", "10", "--bframes", "0", "--threads", "1",
                        "--frames", "2", "-o", out, raw], check=True)
        with open(out, "rb") as f:
            stream = f.read()

    # The parameter sets and the IDR slice, then the P frame's slice. The SEI
    # x264 signs a stream with is left out.
    frames = [[], []]
    k = 0
    for unit in nal_units(stream):
        kind = unit[0] & 0x1F
        if kind == 6:
            continue
        if kind == 1:
            k = 1
        frames[k].append(unit)
    if not frames[0] or not frames[1]:
        sys.exit("probe264.py: x264 did not give an IDR and a P frame")

    w = sys.stdout.write
    w("/* probe264.h: written by probe264.py, which says what it is. */\n")
    w(f"#define PROBE264_W {W}\n#define PROBE264_H {H}\n")
    for k, units in enumerate(frames):
        data = b"".join(b"\x00\x00\x00\x01" + u for u in units)
        w(f"static const unsigned char probe264_{k}[] = {{\n")
        for i in range(0, len(data), 12):
            w("\t" + " ".join(f"0x{c:02x}," for c in data[i:i + 12]) + "\n")
        w("};\n")


main()
