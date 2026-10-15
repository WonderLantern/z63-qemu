#!/usr/bin/env python3
"""PTP to the Nikon Z 6III machine over its USB/IP server (-machine usbip=).

    ptp.py <server> info                 GetDeviceInfo, storages
    ptp.py <server> ls                   every object: handle, size, name
    ptp.py <server> get <handle> <file>  GetObject
    ptp.py <server> get-latest <dir>     the newest image, by handle
    ptp.py <server> capture [<dir>]      release; the new image(s) to <dir>
    ptp.py <server> raw <opcode> [p...]  one operation, its response

<server> is what usbip= was given: a unix socket path, unix:<path>, or
host:port. A reply that takes longer than $PTP_TIMEOUT seconds (30) ends
the run. This is a USB/IP client (the protocol of Linux's vhci-hcd)
speaking USB to the camera itself -- descriptors, SET_CONFIGURATION, then
PTP containers on the Still Image interface's bulk endpoints -- for hosts
that have no vhci-hcd. On Linux, `usbip attach -r <host> -b 1-1` (with
usbip=tcp:<addr>:3240) and gphoto2 should do the same; that is not tried.

`capture` is Nikon's InitiateCaptureRecInMedia (0x9207: no AF, to the
card), retried while the camera says busy, then DeviceReady (0x90C8) and
the object list until the new image is in it. On this machine a release only completes with Custom Settings
d6 Shutter type = Electronic shutter, which does not persist across boots.
"""
import os
import socket
import struct
import sys
import time

USBIP_VERSION = 0x0111
BUSID = b"1-1"

# PTP
PTP_CMD, PTP_DATA, PTP_RESP = 1, 2, 3
OP_GET_DEVICE_INFO = 0x1001
OP_OPEN_SESSION = 0x1002
OP_CLOSE_SESSION = 0x1003
OP_GET_STORAGE_IDS = 0x1004
OP_GET_STORAGE_INFO = 0x1005
OP_GET_OBJECT_HANDLES = 0x1007
OP_GET_OBJECT_INFO = 0x1008
OP_GET_OBJECT = 0x1009
OP_NIKON_DEVICE_READY = 0x90C8
OP_NIKON_CAPTURE_REC_IN_MEDIA = 0x9207
RC_OK = 0x2001
RC_DEVICE_BUSY = 0x2019
RC_SESSION_OPEN = 0x201E


class UsbIp:
    def __init__(self, where):
        if where.startswith("unix:"):
            where = where[5:]
        if where.startswith("tcp:"):
            where = where[4:]
        if "/" in where or not ":" in where:
            self.s = socket.socket(socket.AF_UNIX)
            self.s.connect(where)
        else:
            host, port = where.rsplit(":", 1)
            self.s = socket.create_connection((host, int(port)))
        self.s.settimeout(float(os.environ.get("PTP_TIMEOUT", "30")))
        self.seq = 0
        self.import_device()

    def recvn(self, n):
        b = b""
        while len(b) < n:
            c = self.s.recv(n - len(b))
            if not c:
                raise IOError("USB/IP server closed the connection")
            b += c
        return b

    def import_device(self):
        self.s.sendall(struct.pack(">HHI", USBIP_VERSION, 0x8003, 0) +
                       BUSID.ljust(32, b"\0"))
        ver, code, status = struct.unpack(">HHI", self.recvn(8))
        if status:
            raise IOError("import refused -- is the camera on the bus?")
        dev = self.recvn(312)
        self.vid, self.pid = struct.unpack(">HH", dev[300:304])

    def submit(self, ep, direction, length=0, setup=b"\0" * 8, data=b""):
        """ep: endpoint number; direction 1 = IN. Returns (status, data)."""
        self.seq += 1
        hdr = struct.pack(">IIIII", 1, self.seq, 0x00010002, direction, ep)
        hdr += struct.pack(">IiiiI", 0, length if direction else len(data),
                           0, 0, 0) + setup
        self.s.sendall(hdr + (data if not direction else b""))
        while True:
            r = self.recvn(48)
            cmd, seq = struct.unpack(">II", r[:8])
            status, actual = struct.unpack(">ii", r[20:28])
            payload = self.recvn(actual) if direction and actual > 0 else b""
            if cmd == 3 and seq == self.seq:
                return status, payload

    def control(self, bmRequestType, bRequest, wValue, wIndex, data_or_len):
        setup_in = bmRequestType & 0x80
        length = data_or_len if setup_in else len(data_or_len)
        setup = struct.pack("<BBHHH", bmRequestType, bRequest, wValue,
                            wIndex, length)
        st, d = self.submit(0, 1 if setup_in else 0, length, setup,
                            b"" if setup_in else data_or_len)
        if st:
            raise IOError("control request %02x/%02x failed: %d" %
                          (bmRequestType, bRequest, st))
        return d

    def bulk_out(self, ep, data):
        st, _ = self.submit(ep, 0, data=data)
        if st:
            raise IOError("bulk OUT ep %d failed: %d" % (ep, st))

    def bulk_in(self, ep, length):
        st, d = self.submit(ep, 1, length)
        if st:
            raise IOError("bulk IN ep %d failed: %d" % (ep, st))
        return d


class Ptp:
    def __init__(self, where):
        self.u = UsbIp(where)
        dev = self.u.control(0x80, 6, 0x0100, 0, 18)
        cfg = self.u.control(0x80, 6, 0x0200, 0, 9)
        total = struct.unpack("<H", cfg[2:4])[0]
        cfg = self.u.control(0x80, 6, 0x0200, 0, total)
        self.ep_in = self.ep_out = self.ep_int = None
        self.config = cfg[5]
        i, still = 0, False
        while i + 2 <= len(cfg) and cfg[i]:
            ln, ty = cfg[i], cfg[i + 1]
            if ty == 4:                       # interface
                still = cfg[i + 5] == 6 or (cfg[i + 5] == 0xff and
                                             self.ep_in is None)
                self.iface = cfg[i + 2]
            elif ty == 5 and still:           # endpoint
                addr, attr = cfg[i + 2], cfg[i + 3] & 3
                if attr == 2 and addr & 0x80:
                    self.ep_in = addr & 0x0f
                elif attr == 2:
                    self.ep_out = addr & 0x0f
                elif attr == 3:
                    self.ep_int = addr & 0x0f
            i += ln
        if self.ep_in is None or self.ep_out is None:
            raise IOError("no Still Image interface with two bulk endpoints")
        self.u.control(0x00, 9, self.config, 0, b"")
        self.tid = 0
        self.vid, self.pid = struct.unpack("<HH", dev[8:12])

    def transaction(self, op, params=(), data=None, want_data=True):
        self.tid += 1
        cmd = struct.pack("<IHHI", 12 + 4 * len(params), PTP_CMD, op,
                          self.tid) + b"".join(struct.pack("<I", p)
                                               for p in params)
        self.u.bulk_out(self.ep_out, cmd)
        if data is not None:
            self.u.bulk_out(self.ep_out, struct.pack(
                "<IHHI", 12 + len(data), PTP_DATA, op, self.tid) + data)
        payload = b""
        while True:
            c = self.u.bulk_in(self.ep_in, 512 * 1024)
            if len(c) < 12:
                continue                      # a zero-length packet
            ln, ty, code, tid = struct.unpack("<IHHI", c[:12])
            if ty == PTP_DATA:
                buf = c
                while len(buf) < ln:
                    more = self.u.bulk_in(self.ep_in,
                                          min(ln - len(buf), 4 << 20))
                    if not more:
                        break
                    buf += more
                payload = buf[12:ln]
                continue
            if ty == PTP_RESP:
                rparams = struct.unpack("<%dI" % ((ln - 12) // 4), c[12:ln])
                return code, rparams, payload

    def open(self):
        code, _, _ = self.transaction(OP_OPEN_SESSION, (1,))
        if code not in (RC_OK, RC_SESSION_OPEN):
            raise IOError("OpenSession: 0x%04x" % code)

    def close(self):
        self.transaction(OP_CLOSE_SESSION)


def ptp_string(b, o):
    n = b[o]
    s = b[o + 1:o + 1 + 2 * n].decode("utf-16-le", "replace").rstrip("\0")
    return s, o + 1 + 2 * n


def ptp_array(b, o, fmt="I"):
    n = struct.unpack_from("<I", b, o)[0]
    w = struct.calcsize(fmt)
    return list(struct.unpack_from("<%d%s" % (n, fmt), b, o + 4)), \
        o + 4 + n * w


def device_info(b):
    o = 8
    o += 0  # standard version, vendor extension id/version
    std, vext, vver = struct.unpack_from("<HIH", b, 0)
    o = 8
    ext, o = ptp_string(b, o)
    o += 2                                     # functional mode
    ops, o = ptp_array(b, o, "H")
    events, o = ptp_array(b, o, "H")
    props, o = ptp_array(b, o, "H")
    capf, o = ptp_array(b, o, "H")
    imgf, o = ptp_array(b, o, "H")
    manuf, o = ptp_string(b, o)
    model, o = ptp_string(b, o)
    version, o = ptp_string(b, o)
    serial, o = ptp_string(b, o)
    return dict(manufacturer=manuf, model=model, version=version,
                serial=serial, extension=ext, operations=ops)


def object_info(b):
    storage, fmt, prot, size = struct.unpack_from("<IHHI", b, 0)
    name, o = ptp_string(b, 52)
    return dict(storage=storage, format=fmt, size=size, name=name)


def main():
    if len(sys.argv) < 3:
        sys.exit(__doc__)
    where, verb = sys.argv[1], sys.argv[2]
    p = Ptp(where)
    print("camera %04x:%04x, Still Image interface %d, bulk in %d out %d" %
          (p.vid, p.pid, p.iface, p.ep_in, p.ep_out))
    if verb == "raw":
        op = int(sys.argv[3], 0)
        params = [int(a, 0) for a in sys.argv[4:]]
        if op != OP_GET_DEVICE_INFO:
            p.open()
        code, rp, data = p.transaction(op, params)
        print("response 0x%04x params %s, %d data bytes" %
              (code, [hex(x) for x in rp], len(data)))
        sys.stdout.write(data[:256].hex(" ") + "\n")
        return
    code, _, data = p.transaction(OP_GET_DEVICE_INFO)
    info = device_info(data)
    if verb == "info":
        print("%s %s, firmware %s, serial %s" % (info["manufacturer"],
              info["model"], info["version"], info["serial"]))
        print("%d operations: %s" % (len(info["operations"]),
              " ".join("%04x" % x for x in info["operations"])))
    p.open()
    try:
        code, _, data = p.transaction(OP_GET_STORAGE_IDS)
        storages, _ = ptp_array(data, 0)
        if verb == "info":
            print("storages:", " ".join("%08x" % s for s in storages))
            return
        code, _, data = p.transaction(OP_GET_OBJECT_HANDLES,
                                      (0xffffffff, 0, 0))
        handles, _ = ptp_array(data, 0)
        if verb == "ls":
            for h in handles:
                _, _, d = p.transaction(OP_GET_OBJECT_INFO, (h,))
                oi = object_info(d)
                print("%08x %10d %04x %s" % (h, oi["size"], oi["format"],
                                             oi["name"]))
        elif verb == "capture":
            for _ in range(10):
                code, _, _ = p.transaction(OP_NIKON_CAPTURE_REC_IN_MEDIA,
                                           (0xffffffff, 0))
                if code != RC_DEVICE_BUSY:
                    break
                time.sleep(2)
            if code != RC_OK:
                sys.exit("InitiateCaptureRecInMedia: 0x%04x" % code)
            new = []
            for _ in range(60):             # until the image is listed
                time.sleep(1)
                code, _, _ = p.transaction(OP_NIKON_DEVICE_READY)
                if code != RC_OK:
                    continue
                _, _, data = p.transaction(OP_GET_OBJECT_HANDLES,
                                           (0xffffffff, 0, 0))
                after, _ = ptp_array(data, 0)
                new = [(h, object_info(p.transaction(OP_GET_OBJECT_INFO,
                                                     (h,))[2]))
                       for h in after if h not in handles]
                new = [(h, oi) for h, oi in new if oi["format"] != 0x3001]
                if new:
                    break
            else:
                sys.exit("no new image after 60 s (DeviceReady 0x%04x)" %
                         code)
            for h, oi in new:
                print("%08x %10d %s" % (h, oi["size"], oi["name"]))
                if len(sys.argv) > 3:
                    _, _, d = p.transaction(OP_GET_OBJECT, (h,))
                    out = os.path.join(sys.argv[3], oi["name"])
                    with open(out, "wb") as f:
                        f.write(d)
                    print("  -> %s (%d bytes)" % (out, len(d)))
        elif verb in ("get", "get-latest"):
            if verb == "get":
                h, out = int(sys.argv[3], 16), sys.argv[4]
            else:
                images = [h for h in handles
                          if object_info(p.transaction(
                              OP_GET_OBJECT_INFO, (h,))[2])["format"]
                          not in (0x3001,)]
                if not images:
                    sys.exit("no images")
                h = images[-1]
                _, _, d = p.transaction(OP_GET_OBJECT_INFO, (h,))
                out = os.path.join(sys.argv[3], object_info(d)["name"])
            code, _, data = p.transaction(OP_GET_OBJECT, (h,))
            if code != RC_OK:
                sys.exit("GetObject: 0x%04x" % code)
            with open(out, "wb") as f:
                f.write(data)
            print("%08x -> %s (%d bytes)" % (h, out, len(data)))
    finally:
        p.close()


if __name__ == "__main__":
    main()
