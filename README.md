# qemu-nikon-z63

Patches for QEMU 10.2.4 that add `nikon-z63`, a machine that runs a Nikon
Z 6III's own firmware. The EXPEED 7's RTOS runs on three Cortex-A53 cores and
Linux on the fourth. The body MCU's firmware runs beside them on a modelled
STM32F4 (Cortex-M4). The camera boots to its GUI and takes button presses and
touches. It runs live view from a synthetic Bayer source and writes JPEGs to an
emulated CFexpress card. `z63ui/` is a desktop app that shows the machine as a
camera.

The repository contains no firmware and no data from a camera.

```
patches/    47 patches for QEMU v10.2.4 (git format-patch; apply with git am)
z63-tools/  the scripts this README uses, as the patches add them
z63ui/      the desktop front end (Go + MyGo, a Vite/TypeScript page)
SHA256SUMS  the firmware files the machine was built against
```

The commands below run from the patched QEMU tree, which has the same
`z63-tools/` (plus the rest of the debugging tools). The Python scripts
also run from this repository's copy. `smoke.sh`, `gui.sh` and
`plugins/build.sh` need the tree, because they find `build/` and
`include/` relative to themselves.

## What works

- **Boot.** The RTOS starts CPU3 itself, and Linux 4.14 boots there and
  starts the camera application. The body MCU runs its own
  firmware and talks to the EXPEED over the real frame link. That gives the
  buttons (through its I2C expanders), the camera clock and the top LCD.
- **Display.** The 2D graphics engine renders the GUI's draw programs, and
  the scanout composes the frame. All seven menu tabs can be walked top to
  bottom.
- **Input.** Keys go through the MCU. The shutter button's two contacts are
  wired, and the rear touch panel takes taps, drags and pinches.
- **Live view** runs frame by frame from a Bayer file, including DISP and the
  histogram screen.
- **Stills.** With custom setting d6 set to the electronic shutter, a release
  writes `DCIM/xxxNZ6_3/DSC_nnnn.JPG` (6048x4032) to the card, and live view
  resumes after each shot.
- **Storage.** The NAND store, the settings area of the serial NOR, a
  CFexpress card (QEMU's `nvme`, formatted by the camera) and an empty SD
  slot.
- **Standby** after ~68 s untouched runs to the end. By default the machine
  then shuts down.
- **Playback** decodes the picture's 1620x1080 preview.

### What does not work yet

- Playback stops at the resizer that follows the decode.
- Auto, mechanical and EFCS shutter types stall. Only the electronic shutter
  completes a still.
- The camera does not wake from standby. After any key press it also never
  goes to standby.
- Settings are not saved across boots: the NOR is written, but the writes do
  not reach the file. You have to set d6 again on every boot.
- **USB is not complete.** With `usbip=` the camera enumerates as
  04b0:0454 and starts answering PTP, but the work stops there. The first
  PTP session puts the camera into upload priority mode, and neither a
  half-press nor unplugging the cable brings it back to live view, so the
  shutter button stops taking pictures. Only the first remote release in a
  boot works.
- A release sometimes takes the wrong path and stops the machine on the
  JPEG core at 0x15e20400 (item 3 in `docs/plan.md`; what decides it is not
  known). The photo recipe below avoids the case that has been seen.
- Movie recording, Wi-Fi and Bluetooth are not modelled.

The machine follows one rule: an access that no model claims stops the VM and
prints the PC. It never returns an invented value. `-action shutdown=pause`
turns that stop into a pause, which keeps the last frame and QMP available.

## Build

```sh
git clone --depth 1 --branch v10.2.4 https://gitlab.com/qemu-project/qemu.git qemu-z63
cd qemu-z63
git switch -c nikon-z63
git am /path/to/qemu-nikon-z63/patches/*.patch
./configure --target-list=aarch64-softmmu --enable-plugins --disable-pvg --disable-docs --disable-werror
ninja -C build qemu-system-aarch64
sh z63-tools/plugins/build.sh     # optional: the z63log / z63watch / z63ftrace TCG plugins
```

Tag `v10.2.4` is upstream commit `3e0bcba1ca7d`. Applying the series to it
gives exactly the source tree that the development QEMU was built from
(tree `c743d69`).

`--disable-pvg` is needed on macOS 27: the SDK no longer has `PGTask_t`,
which QEMU 10.2.4's `hw/display/apple-gfx.m` uses, so the build fails
without it. The machine does not use that device. The machine needs libjpeg
(jpeg-turbo), libfdt (dtc), glib and pixman. On macOS:
`brew install glib pixman jpeg-turbo dtc meson ninja pkg-config`.

Development was done on macOS on Apple silicon. Add `--enable-cocoa` for
`z63-tools/gui.sh`, which opens a window whose keyboard acts as the camera's
buttons.

## Firmware

The machine runs body firmware 2.00. These files are not included. Check
yours against the hashes below; the same list is in `SHA256SUMS`, so
`shasum -a 256 -c SHA256SUMS` works in the folder that holds them:

```
736ceaacdd63dc233e51d68beb85378242788e2467773e762b038b2f41c47fda  eg2010_0200i1.bi
77befe8ab6eb8deea038871683fb3b874618ca43d175e158bf21c96ac17ae0b7  li2010_0200i1.bi
98bbe43f8d19875f114ce05417d5f3447bf75d4ac5009027dd00bc4dcb709f56  vr2010_010000.bi
41149887a9d47f82d67694e7bfaa86bb40c9f3a48709ee988904e6558921ee56  ex2010_0200a0.bi
cabfaea55cf4f39ce063d44c43447856da876aa7448584562af26f11136e019d  loader_0x40000000-0x40160000.bin
```

| file | what it is | how the machine takes it |
| --- | --- | --- |
| `eg2010_0200i1.bi` | the EXPEED's RTOS image | `-bios` |
| `li2010_0200i1.bi` | the Linux component (kernel, device tree, initramfs) | written into the NAND image (below), or `li-image=` |
| `vr2010_010000.bi` | the remaining component of the set | not loaded |
| `ex2010_0200a0.bi` | the body MCU's flash image | `mcu-image=` |
| `loader_0x40000000-0x40160000.bin` | the 0x160000-byte loader band below the image | `loader=` |

### Inputs you make

- **NAND image** (`nand=`). The camera loads Linux from its NAND store.
  `z63-tools/nand_provision.py` boots the machine once and has the firmware
  write `li` into the store with its own code:

  ```sh
  python3 z63-tools/nand_provision.py --qemu build/qemu-system-aarch64 \
      --eg eg2010_0200i1.bi --loader loader_0x40000000-0x40160000.bin \
      --snor-user snor_user.bin --li li2010_0200i1.bi --nand nand.img
  ```

  The firmware writes to the NAND while it runs, so give each run a copy.

  The shipped `li` turns its serial console off, so Linux prints nothing.
  To see the Linux console and get a BusyBox shell on the serial port,
  provision a second NAND image from a debug copy of `li`. Keep it apart
  from the stock image:

  ```sh
  python3 z63-tools/li_debug_console.py li2010_0200i1.bi li-debug.bi   # needs dtc
  python3 z63-tools/nand_provision.py ... --li li-debug.bi --nand nand-debug.img
  ```
- **SNOR_USER** (`snor-user=`). This is the 0x2680000-byte per-unit area of
  the serial NOR. `z63-tools/snor_user_input.py` builds only the framing that
  the firmware's reader checks. The status above was measured with a full
  body that `snor_user_clear_error.py`, `snor_user_sensor_version.py` and
  `snor_user_capture_index.py` had prepared. Each of those scripts writes a
  fixed copy and explains in its header what it changes. Stills need the
  capture index in particular: without it the release does not reach the
  capture.
- **Bayer source** (`z9-capture-raw=`). `python3 z63-tools/make_raw.py
  bars.raw` writes colour bars for the sensor to stream.
- **CFexpress card.** Any raw image works, e.g. `mkfile -n 8g cf.raw`
  (macOS) or `truncate -s 8G cf.raw`. Format it with the camera's own
  Format memory card.

## Run

```sh
cp nand.img run-nand.img
build/qemu-system-aarch64 -name z63 \
  -machine nikon-z63,loader=loader_0x40000000-0x40160000.bin,snor-user=snor_user.bin,nand=run-nand.img,mcu-image=ex2010_0200a0.bi,z9-capture=on,z9-capture-raw=bars.raw,gfx-skip-unmodelled=on,sio-sink=9:8:7,cfexpress=on,scanout-dump=frame.ppm \
  -drive if=none,id=cf,file=cf.raw,format=raw \
  -device nvme,drive=cf,serial=Z63CF0001,bus=dw-pcie,addr=0 \
  -cpu cortex-a53 -smp 4,maxcpus=5 -bios eg2010_0200i1.bi \
  -serial stdio -monitor none -display none \
  -qmp unix:qmp.sock,server=on,wait=off -action shutdown=pause
```

- `-smp 4,maxcpus=5` is required with `mcu-image=`, because the M4 is the
  fifth CPU.
- The serial port carries the RTOS's own exception reports. Linux uses it
  only with the debug NAND image (above), and then you get the boot log and
  a BusyBox shell.
- `scanout-dump=` writes the screen as a PPM about once a second.
  `scanout-live=` (every frame, in a file another process maps) is what
  z63ui uses.
- `-plugin z63-tools/plugins/libz63log.dylib,out=fw.log` writes the
  firmwares' own log calls to `fw.log`: RTOS module logs, kernel syslog and
  the MCU's error log.
- `power-off=continue` keeps the MCU running after standby instead of
  shutting the machine down.

`qemu-system-aarch64 -machine nikon-z63,help` lists every option with its
description.

### Driving the camera (QMP)

```
{"execute":"qom-set","arguments":{"path":"/machine","property":"mcu-keys","value":"menu"}}
{"execute":"qom-set","arguments":{"path":"/machine","property":"mcu-keys","value":""}}
{"execute":"qom-set","arguments":{"path":"/machine","property":"touch","value":"512:341"}}
{"execute":"qom-set","arguments":{"path":"/machine","property":"touch","value":"up"}}
```

- **Keys** are `half`, `full`, `menu`, `ok`, `play`, `up`, `down`, `left`,
  `right`, `delete`, `disp`, `fn`, `fn2`, `fn3`, `movie` and `af-on`. To hold
  several, join them with `:`. An empty value releases all keys.
- **Touch** coordinates are in the panel's 1024x682 space, from the top
  left. Use `x:y+x:y` for two fingers. QEMU's multi-touch input events (what
  z63ui sends) work too.
- **Clock and posture.** `mcu-rtc=host|YYYY-MM-DDTHH:MM:SS` sets the clock and
  `imu-mg=x:y:z` the posture.

**Taking a photo.** First format the card if it is new: menu, right,
right, up, ok (Format memory card, CFexpress slot, Yes).

Then set Custom Settings d6, shutter type, to Electronic shutter. The menu
opens on the Setup tab. On a fresh boot with the SNOR body used here, the
key sequence was: menu, left, up x4, down x2, right, down x4, right, down
x5, right, down x3, ok, menu. Watch the screen as you go, because a
different body can open the menu somewhere else.

Release straight from there: hold `half` (this also leaves the menu), then
`half:full` after about 3 s, then release all keys. The JPEG lands in
`DCIM/xxxNZ6_3/` on the card after about 30 s. Don't half-press on its own
between setting d6 and the release. That is the case where the release
went the wrong way and stopped on the JPEG core.

**USB (not complete).** `usbip=./usb.sock` plugs a cable into the
camera's USB-C port and serves it over USB/IP. A socket path must start
with `/` or `.`; otherwise the option reads it as `host:port`.
`z63-tools/ptp.py` is a USB/IP + PTP client for poking at it. See "What
does not work yet" for where it stops.

## Inside the patches

- `hw/arm/nikon-z63.c` is the board. Every other device model is a header
  next to it: `nikon-z63-*.h` (MCU link, STM32, display, gfx, NAND, NOR, I2C,
  touch, DWC3/USB host, JPEG decode, ...).
- `hw/arm/nikon-z63-z9lab.c` holds the capture-side models, ported from a
  Z 9 research machine. The file is generated by
  `z63-tools/import_z9lab.py`: edit the script, not the file.
- Outside `hw/arm` the patches change the GICv3 redistributor, the STM32F4
  EXTI, the STM32F2xx timer (compare interrupts, for the MCU) and the
  DesignWare PCIe host (for the card).
- Patch 0047 lets a DMA buffer run from one NoC bus window into the next.
  Without it the machine stops on a graphics clear when a menu page such as
  Custom Settings d opens.
- In the patched tree, `z63-tools/` has `smoke.sh` (regression), the gdb
  and QMP helpers, the TCG plugins and the input-file tools. This
  repository's copy keeps only the ones this README uses.
- `docs/` has `plan.md` (the current state and what comes next) and the
  register notes behind the models.

The commit messages carry the reasoning, with the firmware addresses behind
each modelled behaviour. Anything asserted that hardware could contradict is
marked `DECLARED` in the source.

| patch | subject |
| --- | --- |
| 0001 | Nikon Z 6III (EXPEED 7) RTOS research machine |
| 0002 | Z 6III: the loader, the image in DRAM, and both cores booting |
| 0003 | Z 6III: hand Linux over when the RTOS is ready, not at reset |
| 0004 | Z 6III: the body MCU, modelled and wired to the EXPEEDs |
| 0005 | Z 6III MCU link: frames intact both ways, compare timers, M4 debuggable |
| 0006 | Z 6III MCU: SPI and TIM1/8 compare interrupts, switch snapshot, live view starts |
| 0007 | Z 6III: capture-sync wired to the MCU; the multi-IO timer counts |
| 0008 | Z 6III: the firmwares' own logs as they are written; past the GUI draws |
| 0009 | Z 6III: the sensor streams -- live view starts and runs frame by frame |
| 0010 | Z 6III: engine 0x150c's primary path ends its frames on its own |
| 0011 | Z 6III: the audio codec answers; movie live view's set-up stops timing out |
| 0012 | Z 6III: the sensor interface acknowledges a lane-0 request (global0 bit 10) |
| 0013 | Z 6III: the movie live-view restart -- TENC, TDEC, the YUV codecs, device 0x190 |
| 0014 | Z 6III: the NOR controller -- the camera saves its settings to SNOR_USER |
| 0015 | Z 6III: the body's buttons -- the MCU's I2C expanders answer, and MENU opens |
| 0016 | Z 6III: z63-tools/snor_user_sensor_tables.py -- empty record 0x63's register tables |
| 0017 | Z 6III: z63watch, and z63log names camera errors and any function's callers |
| 0018 | Z 6III: the power-down register dump reads through |
| 0019 | Z 6III: the SD slot's card detect -- an empty slot reads empty |
| 0020 | Z 6III: the shutter mechanism -- the MCU answers capture sync, mode changes complete |
| 0021 | Z 6III: z63log mcall= -- hooks on the body MCU's functions too |
| 0022 | Z 6III: the vector DSPs -- mailboxes, a boot message, and run/stop |
| 0023 | Z 6III: the display's direct input -- live view under the menus |
| 0024 | Z 6III: live view's frame jobs complete -- the frame's other completions |
| 0025 | Z 6III tools: gdb_probe REGS= and read watchpoints, make_raw bit depth |
| 0026 | Z 6III: ../z9-qemu's capture models, revision deb3a75 |
| 0027 | Z 6III: a RAW frame owed by a capture tick is delivered after group 0 stops |
| 0028 | Z 6III: the DSPs' bus windows reach all of DRAM -- live view no longer stalls |
| 0029 | Z 6III: a CFexpress card -- NVMe on PCIe port 2, formatted by the camera |
| 0030 | Z 6III: the shutter button's contacts on the EXPEED -- the release fires |
| 0031 | Z 6III tools: snor_user_capture_index.py -- a readout descriptor index for the photo |
| 0032 | Z 6III: the still capture's sensor wait -- aux1 and global0 bit 26 at the frame boundary |
| 0033 | Z 6III MCU: I2C v1 receive has a shift register -- the last bytes of a read |
| 0034 | Z 6III: the camera clock -- a clock chip on the MCU, and -machine mcu-rtc |
| 0035 | Z 6III: -machine scanout-live -- every composed frame, in a file a viewer maps |
| 0036 | Z 6III: the top LCD -- the MCU's SPI2 by DMA, a 128x128 memory LCD |
| 0037 | Z 6III: the still through engine 0x150c -- z63-still-capture, and the capture models' timers |
| 0038 | Z 6III: the still's capture job ends -- the upper unit's +0x14 at the still's frame end |
| 0039 | Z 6III: a release writes a JPEG to the card -- the still's capture models on by default |
| 0040 | Z 6III: standby runs to the end -- the cores' WFI status, core hold, DDR self-refresh, the power-hold line |
| 0041 | Z 6III: the JPEG cores decode -- playback's and the menus' pictures, z63-jpeg-decode |
| 0042 | docs/plan.md: where the Z 6III stands -- phases 0-6 worked through, what does not work, what is next |
| 0043 | Z 6III: the touch panel -- a controller on I2C bank 0, EXT_INT_14, and -machine touch |
| 0044 | Z 6III: the touch panel takes QEMU's multi-touch input -- what z63ui sends |
| 0045 | Z 6III: the touch panel -- a drag no longer lifts the finger half-way |
| 0046 | Z 6III: USB -- a device-mode DWC3, a host behind USB/IP, PTP from outside |
| 0047 | Z 6III: a buffer may run from one NoC window into the next |

## z63ui

`z63ui/` is a desktop app built with [MyGo](https://mygo.egoist.dev/): a Go
backend and a Vite/TypeScript page in the system webview. It shows the
machine as the camera, in one window:

- the body's back, with clickable buttons and the live screen on its monitor,
  which also takes touches (mouse or trackpad);
- the top plate, with the control panel that the body MCU draws;
- the serial console, whose input goes to BusyBox;
- a debug dock with QEMU's stderr, the firmware log (z63log), the QEMU
  monitor and the run's details.

QEMU runs as a child process. Frames come through `-machine scanout-live=`
and `toplcd-live=` (shared files the app maps), keys and touches go through
QMP, and the serial port is a socket. `z63ui/main.go` explains the design.

The app needs Go 1.27 and Bun:

```sh
cd z63ui
bun install
bun run build                     # build/darwin-arm64/Z63.app
build/darwin-arm64/Z63.app/Contents/MacOS/Z63 \
    -qemu /path/to/qemu-z63/build/qemu-system-aarch64 \
    -dir /path/to/run -fw /path/to/firmware \
    -loader loader_0x40000000-0x40160000.bin -snor snor_user.bin \
    -nand nand.img -raw bars.raw \
    -plugin /path/to/qemu-z63/z63-tools/plugins/libz63log.dylib \
    -out /path/to/run/ui
```

- The built-in defaults are the author's paths, so pass every flag above.
- `-fw` is the folder that holds `eg2010_0200i1.bi` and `ex2010_0200a0.bi`.
- `-dir` is QEMU's working directory, which relative paths resolve against.
- The NAND image is copied to `<out>/nand-ui.img` before each run.
- `-machine <opts>` adds `-machine` options (default `sio-sink=9:8:7`).
  Anything after `--` goes to QEMU unchanged, for example the CFexpress
  `-drive`/`-device` pair.
- `-no-start` opens the window without booting.

`<out>/ctl.sock` takes one command per line, e.g.
`echo status | nc -U <out>/ctl.sock`:

```
status                        the machine's status
start | stop | restart | pause | resume
keys menu:ok                  hold exactly these buttons ("keys" alone releases)
press <key> [secs]            hold one for secs (default 0.3), then release
serial <text>                 type on the UART (\n, \r and \t are unescaped)
hmp <command>                 a monitor command
shot                          the monitor's picture to runs/
capture <path>                the window as a PNG
eval <js>                     run JavaScript in the page
```

Closing the window quits the app and stops its QEMU.

`skin/nik.png` and `skin/top.png` are the source pictures, and
`sh skin/make.sh` rebuilds `src/assets/` from them. The positions of the
buttons and the monitor are in `src/skin.ts`.
