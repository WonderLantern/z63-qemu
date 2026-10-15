#!/usr/bin/env python3
"""Write a copy of a SNOR_USER body with its saved camera error cleared.

The body z63-tools/snor_user_extract.py produces is the camera's own
per-unit NOR data, and it can carry a fault the camera recorded before it
was dumped. The one this was written for holds, in each of its three
redundant copies, the settings record

    4d 00 00 00  04 00 00 00  00 00 3a 06      id 0x4d, 4 bytes

whose bytes are data items 0x4a00..0x4a03. At boot the camera controller
(sub_40F29110, 0x40f2cfec) reads item 0x4a02 and, when it is nonzero, posts
it as an active error (sub_40F35AD4 -> sub_40EB1414): 0x3a here, which the
GUI shows as Err ("Error. Press shutter-release button to reset.", string
3103, caution 0xbe). The error history (record 0x4f, right after it) keeps
the same timestamp.

This writes a NEW file with items 0x4a02/0x4a03 zeroed in every copy of
that record -- what the body would hold had the error been cleared. It is a
declared edit of the input, not a model: the original file is never
touched, and nothing else in the body changes. Use it only to look past the
Err dialog; with the original body the machine shows what the camera would.

    snor_user_clear_error.py <snor_user.bin> <out.bin>
"""
import sys

src, dst = sys.argv[1:3]
if src == dst:
    sys.exit("refusing to overwrite the input; give a different output path")
data = bytearray(open(src, "rb").read())
head = bytes.fromhex("4d000000" "04000000")
n = 0
i = data.find(head)
while i >= 0:
    if data[i + 10] or data[i + 11]:
        print("record at %#x: items 0x4a00..03 = %s -> cleared 0x4a02/03"
              % (i, data[i + 8:i + 12].hex(" ")))
        data[i + 10] = data[i + 11] = 0
        n += 1
    i = data.find(head, i + 1)
if not n:
    sys.exit("no saved error record found; nothing written")
open(dst, "wb").write(data)
print("wrote %s (%d record(s) cleared)" % (dst, n))
