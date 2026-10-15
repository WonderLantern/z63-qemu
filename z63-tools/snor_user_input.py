#!/usr/bin/env python3
"""Build the minimum SNOR_USER framing the Z 6III's own reader checks.

    snor_user_input.py <eg2010_0200i1.bi> <out.bin> [--json out.json]

WHAT THIS IS NOT
----------------
It is **not** recovered calibration, not a parameter table, not a flash dump
and not anything measured from a camera. Every byte it writes is one of the
four characters `ENDM`, and nothing else in the region is touched. It exists
so a firmware reader that refuses an empty partition can get past its
validity check, and for no other reason. A body that satisfies a marker check
makes the firmware proceed; it does not make the firmware right, and any
value the firmware then computes from the surrounding zeros is meaningless.

WHY THOSE BYTES
---------------
The serial NOR beyond the firmware image -- 0x45980000-0x48000000, which the
SoC's own region table calls SNOR_USER -- holds factory-written, per-unit
bodies. They are in no firmware package, so an emulator has nothing to put
there and the region reads as zeros.

The image carries the partition map at rodata 0x4531fd24, 0x28 per entry.
Field +0x04 is the base, +0x14 the partition size and +0x18 the body size.
Where the two differ, the gap is the trailer. `sub_40D7675C` is the validator
and its last act is explicit about what the trailer must contain:

    v = sub_40D76F88(...);
    if (v[3] == 77 && v[2] == 68 && v[0] == 69 && v[1] == 78) return ok;

69, 78, 68, 77 -- 'E', 'N', 'D', 'M'. The same four bytes terminate string
lists elsewhere in the image, at 0x441b802b and 0x454faf1c, so `ENDM` is this
firmware's end marker generally and not a resource-specific magic.

This reads the partition table out of the image passed on the command line
rather than hardcoding it, so it cannot silently describe a different build.
"""
import hashlib
import json
import struct
import sys
from pathlib import Path

LOAD = 0x40160000
TABLE = 0x4531fd24
STRIDE = 0x28
SNOR_BASE = 0x45980000
SNOR_SIZE = 0x02680000
MARKER = b'ENDM'


def sha(data):
    return hashlib.sha256(data).hexdigest()


def partitions(image):
    out = []
    for i in range(256):
        off = TABLE - LOAD + i * STRIDE
        if off + STRIDE > len(image):
            break
        f = struct.unpack_from('<10I', image, off)
        flags, base, size, body = f[0], f[1], f[5], f[6]
        if not (0x40000000 <= base < 0x48000000):
            break
        if size == 0 and body == 0 and flags == 0:
            break
        out.append(dict(index=i, flags=flags, base=base, size=size, body=body))
    return out


def main():
    argv = sys.argv[1:]
    json_out = None
    if '--json' in argv:
        i = argv.index('--json')
        json_out = argv[i + 1]
        del argv[i:i + 2]
    args = argv
    if len(args) != 2:
        sys.exit(__doc__.splitlines()[2].strip())
    image = Path(args[0]).read_bytes()
    out = Path(args[1])

    parts = partitions(image)
    if not parts:
        sys.exit(f"no partition table at {TABLE:#x} -- wrong image?")

    region = bytearray(SNOR_SIZE)
    written = []
    for p in parts:
        if p['base'] < SNOR_BASE or p['body'] == 0 or p['body'] == p['size']:
            continue
        at = p['base'] + p['body']
        if not (SNOR_BASE <= at and at + 4 <= SNOR_BASE + SNOR_SIZE):
            continue
        region[at - SNOR_BASE:at - SNOR_BASE + 4] = MARKER
        written.append(dict(partition=p['index'], base=hex(p['base']),
                            size=hex(p['size']), body=hex(p['body']),
                            marker_at=hex(at)))

    out.write_bytes(bytes(region))
    record = dict(
        classification='synthetic end markers only; not recovered '
                       'calibration, not a parameter table, not a flash dump',
        marker=MARKER.decode(),
        bytes_written=4 * len(written),
        region=dict(base=hex(SNOR_BASE), size=hex(SNOR_SIZE)),
        partition_table=hex(TABLE),
        partitions_total=len(parts),
        partitions_marked=len(written),
        markers=written,
        evidence='sub_40D7675C compares the trailer against 69,78,68,77',
        firmware_sha256=sha(image),
        output_sha256=sha(bytes(region)),
        source_sha256=sha(Path(__file__).read_bytes()),
        body_recovered=False,
        hardware_calibration=False,
    )
    text = json.dumps(record, indent=1)
    if json_out:
        Path(json_out).write_text(text)
    print(text)


if __name__ == '__main__':
    main()
