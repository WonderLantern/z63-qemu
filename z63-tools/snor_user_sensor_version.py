#!/usr/bin/env python3
"""Write a copy of a SNOR_USER body whose sensor-parameter record carries a
version the firmware accepts.

The first sensor power-up (sub_4115EDF4, from CaptureSequence's start via
sub_40F45BE4) runs, once, sub_411781F4: it takes record 0x63 -- the image
sensor's parameter block, ~1.3 MB, found through sub_40D7185C(0x63) and
kept at 0xA04CE13A0 by sub_4115EDC4 -- and reads the two bytes at
+0x117ab4 as a 12-bit version, (b0 << 4) | (b1 >> 4). Below 0x104 the
power-up returns error bit 0x400 (b0 other than 0x16 gives 0x200);
sub_40F422E8 turns that into CaptureSequence event 0x4110181, which
sub_40F2FA70 reports as error 0x45 (sub_40F35AD4 -> sub_40EB1414): the
"Error. Turn the camera off and then on again" dialog over everything.

The SNOR_USER body is not a flash dump: z63-tools/snor_user_extract.py
takes it from a RAM image of the body (snor_user.json: region 0x45980000,
size 0x2680000), and in that image record 0x63 is mostly zero pages -- in
all four copies (file offsets 0x120000, 0x500000, 0x2120000, 0x2500000;
the firmware uses the last, RAM 0x47e80000), including these two bytes and
the sensor register tables from +0x119510. The camera's real values are
therefore not known. This writes a NEW file with the two bytes set to 0x16
0x00 (version 0x160, the byte the code names and the smallest such version
both checks accept) in every copy where both are zero. It is a declared
edit of the input, like snor_user_clear_error.py, not a model; the original
file is never touched. The register tables stay zero: the sensor's control
port is a write-only sink (nikon-z63.c, sio-sink), so nothing consumes
them.

    snor_user_sensor_version.py <snor_user.bin> <out.bin>
"""
import sys

REGION = 0x45980000            # snor_user.json "region.base"
RECORD = 0x47E80000 - REGION   # record 0x63, the copy the firmware reads
VERSION = 0x117AB4

src, dst = sys.argv[1:3]
if src == dst:
    sys.exit("refusing to overwrite the input; give a different output path")
data = bytearray(open(src, "rb").read())
if len(data) < RECORD + VERSION + 2:
    sys.exit("input is too short to hold record 0x63 at %#x" % RECORD)
head = bytes(data[RECORD:RECORD + 16])
if not any(head):
    sys.exit("no record header at %#x; is this a SNOR_USER body?" % RECORD)
n = 0
i = data.find(head)
while i >= 0:
    v = i + VERSION
    if v + 2 <= len(data):
        if data[v] or data[v + 1]:
            print("copy at %#x: version bytes %s, left as they are"
                  % (i, data[v:v + 2].hex(" ")))
        else:
            data[v], data[v + 1] = 0x16, 0x00
            print("copy at %#x: version bytes 00 00 -> 16 00" % i)
            n += 1
    i = data.find(head, i + 1)
if not n:
    sys.exit("no copy with zero version bytes; nothing written")
open(dst, "wb").write(data)
print("wrote %s (%d copies set)" % (dst, n))
