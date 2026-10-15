#!/usr/bin/env python3
"""Write a synthetic NZ9RAW1 Bayer file for -machine z9-capture-raw=.

    make_raw.py <out.raw> [width height [bits]]   (default 640 480 12)

The format is ../z9-qemu's (tools/make_cmos.py, whose statistics model and
RAW pipeline read it, see hw/arm/nikon-z63-z9lab.c statistics_load): the
8-byte magic "NZ9RAW1\\0", then little-endian u32 width, height, bits,
cfa (0 = RGGB), black, white, then width*height little-endian u16 samples.

The picture is that tool's default -- eight colour bars, darkening towards
the top -- made linear and laid out RGGB at 12 bits (or the bits given, 12..16). It is an input chosen
for the run so a frame has something recognisable in it; nothing about it
describes the Z 6III's sensor or its readout.
"""
import struct
import sys

out = sys.argv[1]
w, h = (int(sys.argv[2]), int(sys.argv[3])) if len(sys.argv) > 3 else (640, 480)
bits = int(sys.argv[4]) if len(sys.argv) > 4 else 12
white = (1 << bits) - 1
if w % 2 or h % 2 or w < 60 or h < 24:
    sys.exit("even dimensions, at least 60x24")
if not 12 <= bits <= 16:
    sys.exit("bits 12..16")
bars = [(1, 1, 1), (1, 1, 0), (0, 1, 1), (0, 1, 0),
        (1, 0, 1), (1, 0, 0), (0, 0, 1), (0, 0, 0)]


def linear(v):
    return v / 12.92 if v <= 0.04045 else ((v + 0.055) / 1.055) ** 2.4


rows = []
for y in range(h):
    gain = 0.25 + 0.75 * y / (h - 1)
    row = bytearray()
    for x in range(w):
        rgb = bars[x * 8 // w]
        ch = (0, 1)[x & 1] if not y & 1 else (1, 2)[x & 1]   # RGGB
        row += struct.pack("<H", round(linear(rgb[ch] * gain) * white))
    rows.append(bytes(row))
with open(out, "wb") as f:
    f.write(struct.pack("<8s6I", b"NZ9RAW1\0", w, h, bits, 0, 0, white))
    f.write(b"".join(rows))
print("wrote %s (%dx%d RGGB, %d-bit, synthetic bars)" % (out, w, h, bits))
