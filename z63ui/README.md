# z63ui

The Nikon Z 6III QEMU machine (`nikon-z63`, in the qemu-z63 tree) as a camera
on the desktop: one window with the body's back -- the live scanout on its
monitor, which takes touches -- its top with the control panel the body MCU
draws, the serial console, and a debug dock with QEMU's stderr, the
firmwares' logs and the QEMU monitor. A [MyGo](https://mygo.egoist.dev/) app:
Go, and a Vite page in the system webview.

QEMU runs as a child process; `main.go` says why, and how the pieces connect.

## Needs

- qemu-z63 built with `-machine scanout-live=` and `toplcd-live=`
  (`hw/arm/nikon-z63-scanout.h`, `nikon-z63-toplcd.h`) and, for the firmware
  log, `z63-tools/plugins/libz63log.dylib`
- the run directory the defaults in `main.go` name (`/Volumes/ExAPFS/z63-run`:
  firmware, loader dump, SNOR_USER, NAND image)
- Go 1.27 and Bun; on exapfs: `. /Volumes/ExAPFS/z63-run/claude/toolchain/env.sh`

## Build and run

    bun install
    bun run build          # build/darwin-arm64/Z63.app
    build/darwin-arm64/Z63.app/Contents/MacOS/Z63 [-no-start] [-machine ...] [-- qemu args]

From ssh, start the binary directly (`open` is refused there); the window opens on the
logged-in desktop. Closing the window quits, and stops its QEMU.

`<out>/ctl.sock` (default `/Volumes/ExAPFS/z63-run/claude/ui/ctl.sock`) takes
one command a line -- `status`, `press menu`, `hmp info cpus`, `capture
/tmp/page.png`, ... -- listed in `ctl.go`.

## The pictures

`skin/nik.png` (the back, an illustration) and `skin/top.png` (the top) are
the originals; `sh skin/make.sh` makes `src/assets/` from them with
`cmd/cutout` (background out, lens off, trimmed). Where the buttons and the
monitor are in each is in `src/skin.ts`, measured on those pictures.
