#!/usr/bin/env python3
"""Write a copy of a SNOR_USER body whose sensor-parameter record names a
readout descriptor for the still capture's mode.

The shutter release (sub_40FC7CDC -> sub_40FC4140(1)) runs CAPSVC down to
sub_4105B440, which asks sub_411790C8 for a readout descriptor of the
capture's sensor mode: sub_41175884 picks a mode, the table at 0xa02bd0558
turns it into a selector (1 for the photo here, traced), and record 0x63
(the copy at RAM 0x47e80000, kept at 0xA04CE13A0; see
snor_user_sensor_version.py) gives one byte per selector at +0x110840 --
a 1-based index into 32-byte descriptors at +0x110860, of which the code
reads u16s +0, +2 and +6. In our RAM-derived body that page is zero, so
the index is 0, the descriptor address is the table minus 32 plus 2^37,
and CAPSVC takes a data abort at 0x411791A4 (FAR 0x2047F90846) on the
first release.

../z9-qemu (WonderLantern, GPL-2.0-or-later) met the same lookup on the
Z 9 -- tools/z9_processing_index_input.py, "table47e80000 + selector +
daf40 -> record daf60 + (index-1)*20" -- and supplies index 1 for the
observed selectors only, leaving the descriptor itself empty. This does
the same here: index 1 for selector 1, in every copy where that byte is
zero. The descriptor stays zero (format 0, length 0), so this supplies a
place to look, not a readout geometry. It is a declared edit of the input,
not a model, and the original file is never touched; a dump of record 0x63
from a body would replace it.

    snor_user_capture_index.py <snor_user.bin> <out.bin>
"""
import sys

REGION = 0x45980000            # snor_user.json "region.base"
RECORD = 0x47E80000 - REGION   # record 0x63, the copy the firmware reads
INDEX = 0x110840               # one byte per selector
SELECTORS = (1,)               # the photo's, traced at 0x41179170

src, dst = sys.argv[1:3]
if src == dst:
    sys.exit("refusing to overwrite the input; give a different output path")
data = bytearray(open(src, "rb").read())
if len(data) < RECORD + INDEX + max(SELECTORS) + 1:
    sys.exit("input is too short to hold record 0x63 at %#x" % RECORD)
head = bytes(data[RECORD:RECORD + 16])
if not any(head):
    sys.exit("no record header at %#x; is this a SNOR_USER body?" % RECORD)
n = 0
i = data.find(head)
while i >= 0:
    for sel in SELECTORS:
        o = i + INDEX + sel
        if o >= len(data):
            continue
        if data[o]:
            print("copy at %#x: selector %d index %d, left as it is"
                  % (i, sel, data[o]))
        else:
            data[o] = 1
            print("copy at %#x: selector %d index 0 -> 1" % (i, sel))
            n += 1
    i = data.find(head, i + 1)
if not n:
    sys.exit("no copy with a zero index; nothing written")
open(dst, "wb").write(data)
print("wrote %s (%d bytes set)" % (dst, n))
