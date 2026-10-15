#!/bin/sh
# Run the Z 6III machine with a Cocoa window, to look at what the scanout
# composes. Needs a build configured with --enable-cocoa, and a desktop
# session to open the window in (start it from Terminal or Finder, not
# over ssh). Extra arguments are passed to QEMU.
#
#   LOADER=loader_0x40000000-0x40160000.bin SNOR_USER=snor_user.bin \
#   NAND=nand-debug-pristine.img \
#       z63-tools/gui.sh <dir holding eg2010_0200i1.bi> [qemu options...]
#
# MCU=<dir holding ex2010_0200a0.bi> runs the body MCU's own image on a
# Cortex-M4 beside the A53s, instead of the scripted peer (nikon-z63-mcu.h
# and nikon-z63-stm32.h). It needs -smp 4,maxcpus=5 -- the M4 is the fifth
# CPU and QEMU sizes its TCG context array from max_cpus -- which this
# script supplies automatically. MCU_PINS=G7=1:C5=0 drives the MCU's own
# input pins, standing in for the wires the EXPEED holds; the separator is
# ":" because "," separates -machine properties. It replaces the default,
# D13=1 (the battery data line idling high), so keep D13=1 in the list.
#
# MCU_SPI_PEER bridges the MCU's SPI<n> to this board's serial instance
# <m>, which is the link between the two chips (nikon-z63-sio.h). The
# default, 1:0+4:1, is the pairing both firmwares' frame checks agree with;
# every frame both ways is logged as Z63_MCU_FRAME.
# MCU_WIRES=B12=a3:2+E3=a3:3+D12=a4:3 wires the MCU's own handshake pins to
# the EXPEED's GPIO_TOP port bits, which is what the two firmwares poll and
# drive. Declared, and each wire is logged as Z63_MCU_WIRE.
#
#
# Z63_LOG=<file> loads z63-tools/plugins/libz63log (build it with
# z63-tools/plugins/build.sh) and writes the firmwares' own log calls there,
# one line each: the RTOS's binary module logs, its kernel syslog and the
# body MCU's error log. The build must be configured with -Dplugins=true.
#
# NAND is copied before every run (to $NAND.run) because the firmware
# writes to it; the original is never touched. Without NAND the RTOS does
# not get as far as the display.
#
# A refused access stops the machine by asking QEMU to shut down; here
# -action shutdown=pause turns that into a pause, so the window stays open
# on the last frame and the reason is on stderr. QMP is on
# /tmp/z63-gui.sock for looking at a paused machine.
#
# Keys in the window are camera buttons (nikon-z63-mcu.h): arrows, Return
# (OK), M (MENU), P (playback), Backspace (delete), H (half-press), Space
# (full press), D (DISP), F1-F3 (Fn), R (movie), A (AF-ON). With the
# original SNOR_USER body the camera shows Err (a fault saved in that dump);
# z63-tools/snor_user_clear_error.py writes a copy without it. The dump also
# lacks the image sensor's parameter record (zero pages), so the first sensor
# power-up fails its version check and the camera shows "Error. Turn the
# camera off and then on again"; z63-tools/snor_user_sensor_version.py writes
# a copy with a declared version in place (run it on the cleared copy).
#
# The window stays blank until the firmware programs the display controller:
# nikon-z63-scanout.h refuses to compose a frame from a register file that
# has no output size, and says why once on stderr.
set -e
DIR=${1:?usage: gui.sh <dir holding eg2010_0200i1.bi> [qemu options...]}
shift
QEMU=$(cd "$(dirname "$0")/.." && pwd)/build/qemu-system-aarch64
OPT=""
SMP=4
[ -n "${LOADER:-}" ] && OPT="$OPT,loader=$LOADER"
[ -n "${SNOR_USER:-}" ] && OPT="$OPT,snor-user=$SNOR_USER"
if [ -n "${NAND:-}" ]; then
    cp "$NAND" "$NAND.run"
    OPT="$OPT,nand=$NAND.run"
fi
if [ -n "${MCU:-}" ]; then
    OPT="$OPT,mcu-image=$MCU/ex2010_0200a0.bi"
    [ -n "${MCU_PINS:-}" ] && OPT="$OPT,mcu-pins=$MCU_PINS"
    [ -n "${MCU_WIRES:-}" ] && OPT="$OPT,mcu-wires=$MCU_WIRES"
    [ -n "${MCU_SPI_PEER:-}" ] && OPT="$OPT,mcu-spi-peer=$MCU_SPI_PEER"
    SMP=4,maxcpus=5
fi
PLUGIN=""
if [ -n "${Z63_LOG:-}" ]; then
    lib=$(cd "$(dirname "$0")" && pwd)/plugins/libz63log
    [ -f "$lib.dylib" ] && lib=$lib.dylib || lib=$lib.so
    PLUGIN="-plugin $lib,out=$Z63_LOG"
fi
rm -f /tmp/z63-gui.sock
exec "$QEMU" -name z63-gui -machine "nikon-z63$OPT" \
    -cpu cortex-a53 -smp "$SMP" -bios "$DIR/eg2010_0200i1.bi" \
    -serial file:/tmp/z63-gui-serial.log -monitor none \
    -qmp unix:/tmp/z63-gui.sock,server=on,wait=off \
    -display cocoa -action shutdown=pause $PLUGIN "$@"
