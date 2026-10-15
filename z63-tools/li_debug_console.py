#!/usr/bin/env python3
"""Make a DEBUG copy of the li component whose serial port talks.

    li_debug_console.py <li2010_0200i1.bi> <out.bi>     (needs dtc on PATH)

The shipped device tree -- the first one in the NISI table, the one the
RTOS hands Linux (sub_40C9C908 reads the table entry at +0x40) -- marks the
console UART `console = "disabled"`. nkj_uart_probe then treats the port as
not a real serial line and discards tty output, so a production body has no
serial console: Linux runs, and says nothing. That is correct behaviour and
the shipped NAND image keeps it.

For looking at Linux this removes that one property from that one node and
nothing else, rebuilds the blob with dtc, puts it back in its own units and
updates the size in the NISI table. Provision a separate NAND image from the
output (nand_provision.py --li <out.bi>); never mistake it for the body's.
A record of both hashes and the change goes next to the output.
"""
import hashlib, json, os, re, struct, subprocess, sys, tempfile

src, dst = sys.argv[1], sys.argv[2]
li = bytearray(open(src, "rb").read())
if li[:4] != b"NISI":
    sys.exit("not a NISI container")
unit, count, size = struct.unpack_from("<III", li, 0x40)
blob = bytes(li[unit * 2048:unit * 2048 + size])
with tempfile.TemporaryDirectory() as d:
    open(os.path.join(d, "in.dtb"), "wb").write(blob)
    dts = subprocess.run(["dtc", "-q", "-I", "dtb", "-O", "dts",
                          os.path.join(d, "in.dtb")],
                         check=True, capture_output=True, text=True).stdout
    node = re.search(r"serial@f000000 \{.*?\n\t\};", dts, re.S)
    if not node or '\t\tconsole = "disabled";\n' not in node.group(0):
        sys.exit("the console UART node does not carry console = \"disabled\"")
    fixed = node.group(0).replace('\t\tconsole = "disabled";\n', "", 1)
    dts = dts[:node.start()] + fixed + dts[node.end():]
    open(os.path.join(d, "out.dts"), "w").write(dts)
    new = subprocess.run(["dtc", "-q", "-I", "dts", "-O", "dtb",
                          os.path.join(d, "out.dts")],
                         check=True, capture_output=True).stdout
if len(new) > count * 2048:
    sys.exit("the rebuilt tree no longer fits its %d units" % count)
li[unit * 2048:(unit + count) * 2048] = new + bytes(count * 2048 - len(new))
struct.pack_into("<I", li, 0x48, len(new))
open(dst, "wb").write(li)
rec = {
    "debug": True,
    "what": "li component with console = \"disabled\" removed from the "
            "serial@f000000 node of its first device tree; nothing else",
    "source": src, "source_sha256": hashlib.sha256(open(src, "rb").read()).hexdigest(),
    "output": dst, "output_sha256": hashlib.sha256(li).hexdigest(),
    "dtb_bytes": [size, len(new)],
    "tool_sha256": hashlib.sha256(open(__file__, "rb").read()).hexdigest(),
}
json.dump(rec, open(dst + ".json", "w"), indent=1)
print(json.dumps(rec, indent=1))
