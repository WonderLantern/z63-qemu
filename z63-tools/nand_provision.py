#!/usr/bin/env python3
"""Write the Linux component into the NAND image, with the firmware's own code.

    nand_provision.py --qemu build/qemu-system-aarch64 --eg <eg2010_0200i1.bi>
                      --loader <loader band> --snor-user <body>
                      --li <li2010_0200i1.bi> --nand <nand.img> [--port 41234]

On the body Linux is not in NOR at all: the partition table at 0x4531fd24
has only the loader, the SOLID image and SNOR_USER. It is in the NAND, in
volume 0 of the firmware's "ANAND" store, as the li component itself -- a
"NISI" container laid out in 2 KiB units. STSQNAND loads it from there
(sub_40C9C908 -> sub_4118A04C, read N units at unit U) before starting
CPU3. An erased NAND has no NISI header, so Linux never loads.

The store's on-flash format is the firmware's business, so this tool does
not write it. It boots the machine once and lets the firmware do it:

  1. The li file is placed raw at 0xa07800000 with QEMU's generic loader --
     the Linux window, untouched until STSQNAND loads Linux there anyway.
  2. Break in STSQNAND (sub_40D71314) at 0x40d71324, right after the store
     is mounted (sub_41189F78; on an erased chip that formats it) and before
     the parameters for the Linux read are loaded.
  3. Call the store's own write -- sub_4118A198(unit 0, units, 0xa07800000),
     the mirror of the read and what the FIRMWARE module uses (0x410193bc)
     -- with the return address set back to 0x40d71324. The return address
     of sub_40D71314 itself is already on its stack, and nothing
     caller-saved is live at that point, so the task then carries on as if
     nothing had happened: it reads Linux back out of the NAND.
  4. -machine nand=<file> writes every program and erase through, so the
     result persists. Later boots need none of this.

The number of units is read from the NISI table (the end of its last
entry), not from the file size: the file carries two bytes beyond it.

What this is: provisioning, like snor_user_input.py -- an input file made
once, recorded with hashes. What it is not: part of any normal boot, or a
claim about how the body's NAND was laid out beyond what the firmware's own
write produced.
"""
import argparse, hashlib, json, os, re, socket, struct, subprocess, sys, time

BREAK = 0x40d71324          # STSQNAND, after sub_41189F78
WRITE = 0x4118a198          # sub_4118A198(unit, units, src)
LI_ADDR = 0xa07800000
MOUNT_FLAG = 0xa04e67848    # 1 = store unusable (sub_4118AEAC / AF70)


class Rsp:
    def __init__(self, port):
        for _ in range(100):
            try:
                self.s = socket.create_connection(("127.0.0.1", port), 600)
                break
            except OSError:
                time.sleep(0.2)
        else:
            sys.exit("no gdbstub on port %d" % port)
        self.buf = b""

    def send(self, cmd):
        self.s.sendall(b"$" + cmd + b"#%02x" % (sum(cmd) & 0xff))
        while True:
            c = self.s.recv(1)
            if c == b"+":
                return
            if not c:
                raise EOFError

    def recv(self):
        while True:
            i = self.buf.find(b"#")
            if i >= 0 and len(self.buf) >= i + 3:
                body = self.buf[self.buf.find(b"$") + 1:i]
                self.buf = self.buf[i + 3:]
                self.s.sendall(b"+")
                return body.decode()
            d = self.s.recv(65536)
            if not d:
                raise EOFError
            self.buf += d

    def cmd(self, c):
        self.send(c)
        return self.recv()

    def regs(self):
        g = self.cmd(b"g")
        return [int.from_bytes(bytes.fromhex(g[i * 16:(i + 1) * 16]), "little")
                for i in range(34)]

    def setreg(self, n, v):
        r = self.cmd(b"P%x=%s" % (n, v.to_bytes(8, "little").hex().encode()))
        if r != "OK":
            sys.exit("cannot set register %d: %r" % (n, r))

    def mem(self, addr, n):
        r = self.cmd(b"m%x,%x" % (addr, n))
        return bytes.fromhex(r) if not r.startswith("E") else None

    def run_to(self, addr):
        """Continue to a breakpoint at addr; return the stopping thread."""
        self.cmd(b"Z0,%x,4" % addr)
        self.send(b"c")
        stop = self.recv()
        self.cmd(b"z0,%x,4" % addr)
        m = re.search(r"thread:([0-9a-fp.]+);", stop)
        if m:
            self.cmd(b"Hg" + m.group(1).encode())
        return m.group(1) if m else "?"


def sha256(path):
    h = hashlib.sha256()
    with open(path, "rb") as f:
        for chunk in iter(lambda: f.read(1 << 20), b""):
            h.update(chunk)
    return h.hexdigest()


def main():
    ap = argparse.ArgumentParser(description=__doc__.split("\n")[0])
    for a in ("qemu", "eg", "loader", "snor-user", "li", "nand"):
        ap.add_argument("--" + a, required=True)
    ap.add_argument("--port", type=int, default=41234)
    ap.add_argument("--settle", type=float, default=30,
                    help="seconds to let the boot carry on afterwards")
    a = ap.parse_args()

    li = open(a.li, "rb").read()
    if li[:4] != b"NISI":
        sys.exit("%s is not a NISI container" % a.li)
    units = 0
    for o in range(0x20, 0x60, 0x10):
        start, count = struct.unpack_from("<II", li, o)
        units = max(units, start + count)
    if units * 2048 > len(li) or units >= 0x10000:
        sys.exit("NISI table says %d units, file has %d bytes" % (units, len(li)))
    if os.path.exists(a.nand):
        sys.exit("%s exists; provisioning starts from an erased chip" % a.nand)

    log = open(a.nand + ".qemu.log", "w")
    q = subprocess.Popen([
        a.qemu, "-machine",
        "nikon-z63,loader=%s,snor-user=%s,nand=%s" % (a.loader, a.snor_user,
                                                      a.nand),
        "-cpu", "cortex-a53", "-smp", "4", "-bios", a.eg,
        "-device", "loader,file=%s,addr=0x%x,force-raw=on" % (a.li, LI_ADDR),
        "-serial", "null", "-monitor", "none", "-display", "none",
        "-gdb", "tcp:127.0.0.1:%d" % a.port, "-S"], stdout=log, stderr=log)
    try:
        r = Rsp(a.port)
        r.cmd(b"qSupported:multiprocess+")
        r.cmd(b"?")
        th = r.run_to(BREAK)
        flag = struct.unpack("<I", r.mem(MOUNT_FLAG, 4))[0]
        head = r.mem(LI_ADDR, 0x60)
        print("stopped in STSQNAND (%s); store flag %d; li at 0x%x %s" %
              (th, flag, LI_ADDR, "intact" if head == li[:0x60] else "CHANGED"))
        if flag == 1 or head != li[:0x60]:
            sys.exit("not provisioning: the store is unusable or the li "
                     "copy was overwritten before STSQNAND reached it")
        regs = r.regs()
        r.setreg(0, 0)
        r.setreg(1, units)
        r.setreg(2, LI_ADDR)
        r.setreg(30, BREAK)
        r.setreg(32, WRITE)
        t0 = time.time()
        r.run_to(BREAK)
        result = r.regs()[0] & 0xffffffff
        print("sub_4118A198(0, %d, 0x%x) = %d after %.1f s" %
              (units, LI_ADDR, result, time.time() - t0))
        if result != 0:
            sys.exit("the store refused the write")
        assert r.regs()[31] == regs[31], "stack pointer moved"
        r.send(b"c")                 # let the task read Linux back out
        time.sleep(a.settle)
    finally:
        q.terminate()
        q.wait()
        log.close()
    rec = {
        "nand": a.nand, "nand_sha256": sha256(a.nand),
        "li": a.li, "li_sha256": sha256(a.li), "units": units,
        "eg_sha256": sha256(a.eg), "tool_sha256": sha256(__file__),
        "how": "the firmware's own sub_4118A198 (ANAND volume 0, unit 0), "
               "called from STSQNAND at 0x40d71324 on a freshly formatted "
               "store; see the tool's docstring",
    }
    json.dump(rec, open(a.nand + ".json", "w"), indent=1)
    print(json.dumps(rec, indent=1))


if __name__ == "__main__":
    main()
