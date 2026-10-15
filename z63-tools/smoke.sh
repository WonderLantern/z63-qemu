#!/bin/sh
# Regression for the Z 6III research machine. One command, three checks.
#
#   1. The RTOS stops at the recorded frontier and nowhere earlier.
#   2. Linux reaches a BusyBox prompt on CPU3 -- but only once the RTOS has
#      no frontier left. While the crank is still turning this is skipped,
#      not failed: a refused access stops the whole machine, so the Linux
#      core never gets its chance, and that is a fact about where the RTOS
#      has got to rather than anything wrong with the Linux side.
#
# A device model that is correct but never exercised shows up as "no change"
# here, which is the expected result for anything on a path the booting
# firmware does not take. A model that is *wrong* about an offset the firmware
# does touch shows up as a stop with a PC, which is the point.
#
# During Phase 2 the RTOS always stops somewhere: a stop is how the crank
# turns. So "no stop at all" is not the pass condition and never will be
# until the crank reaches the end. What this checks instead is that the stop
# is the one we already know about -- the address in z63-tools/frontier. A
# stop at any other address is news, and needs a person to decide whether the
# frontier moved forward or a model that used to work stopped working. When
# it has moved forward, write the new address into that file as part of the
# same change that moved it.
#
# The Linux check is the exit condition of the RTOS work, not a gate on it.
# It runs when the frontier file records no address.
#
# The loader matters: without it the firmware's own boot-ROM service page
# branches into 0x40000000-0x40160000 and finds nothing. Pass it in LOADER
# (a dump of the window from 0x40000000); the board then needs no stub.
#
# SNOR_USER is the per-unit SNOR_USER body (z63-tools/snor_user_extract.py
# from a full NOR dump). Without it configuration object 28 reads 0 and the
# module lifecycle desynchronises before it reaches the frontier, so the
# frontier is recorded with it set.
#
#   LOADER=../nikon/out/z63/rtos/eg_z63_0x40000000.bin \
#   SNOR_USER=snor_user.bin \
#       z63-tools/smoke.sh <components-dir> [seconds]
set -e
cd "$(dirname "$0")/.."
DIR=${1:?usage: smoke.sh <dir holding eg2010_0200i1.bi and li2010_0200i1.bi> [seconds]}
# The Linux check needs much longer than it used to: CPU3 is no longer
# handed over at reset but when the RTOS publishes share_mem's directory,
# which an emulated RTOS takes minutes to reach. That wait is the point --
# see the note by Z63_SHARE_MEM_BASE -- so the budget has to cover it.
WAIT=${2:-420}
EG="$DIR/eg2010_0200i1.bi"
LI="$DIR/li2010_0200i1.bi"
if [ -n "${LOADER:-}" ] && [ -f "$LOADER" ]; then
    OPT="loader=$LOADER"
else
    OPT="rom-page-stub=on"
    echo "note: no LOADER=<dump of 0x40000000>; falling back to the stub."
fi
if [ -n "${SNOR_USER:-}" ] && [ -f "$SNOR_USER" ]; then
    OPT="$OPT,snor-user=$SNOR_USER"
else
    echo "note: no SNOR_USER=<per-unit body>; the frontier assumes one."
fi
QEMU=./build/qemu-system-aarch64
[ -x "$QEMU" ] || { echo "no $QEMU -- build first"; exit 1; }
[ -f "$EG" ] || { echo "no $EG"; exit 1; }

RUN=$(mktemp -d)
# Kill only what this script started: a machine someone has open in a
# window (z63-tools/gui.sh) is not ours to stop.
RTOS=; FULL=
trap 'kill $RTOS $FULL 2>/dev/null; rm -rf "$RUN"' EXIT

echo "== 1/2  RTOS alone"
"$QEMU" -machine "nikon-z63,$OPT" -cpu cortex-a53 -smp 4 \
    -bios "$EG" -serial null -monitor none -display none \
    -qmp "unix:$RUN/q1.sock,server=on,wait=off" > "$RUN/rtos.log" 2>&1 &
RTOS=$!
sleep 45
kill $RTOS 2>/dev/null || true
wait $RTOS 2>/dev/null || true
FRONTIER=$(sed -n 's/^frontier *//p' z63-tools/frontier 2>/dev/null | head -1)
GOT=$(grep -oE 'addr=0x[0-9a-f]+' "$RUN/rtos.log" | head -1 | cut -d= -f2)
if [ -z "$GOT" ]; then
    echo "   ok -- nothing stopped in 45s (the crank has run out of blockers)"
elif [ "$GOT" = "$FRONTIER" ]; then
    echo "   ok -- stopped at the recorded frontier $GOT, and nowhere earlier"
else
    echo "   FAIL -- stopped at $GOT, not the recorded frontier ${FRONTIER:-<unset>}:"
    grep 'stopping (unmodeled device\|unmodeled read\|unmodeled write' \
        "$RUN/rtos.log" | head -3
    echo "   If the frontier moved forward, record it in z63-tools/frontier."
    exit 1
fi

[ -f "$LI" ] || { echo "== 2/2  skipped, no $LI"; exit 0; }

if [ -n "$FRONTIER" ]; then
    echo "== 2/2  skipped -- the RTOS still stops at $FRONTIER, so a whole-"
    echo "        machine stop reaches CPU3 before Linux can print. This"
    echo "        check is the exit condition of the RTOS crank, not a gate"
    echo "        on it; clear the frontier line to turn it back on."
    echo
    echo "PASS"
    exit 0
fi

echo "== 2/2  RTOS + LI payload, expecting a shell"
"$QEMU" -machine "nikon-z63,$OPT,li-image=$LI,cpu3-entry=0xa07800000" \
    -cpu cortex-a53 -smp 4 -bios "$EG" -serial "file:$RUN/serial.log" \
    -monitor none -display none > "$RUN/full.log" 2>&1 &
FULL=$!
n=0
while [ $n -lt "$WAIT" ]; do
    grep -q BusyBox "$RUN/serial.log" 2>/dev/null && break
    sleep 5
    n=$((n + 5))
done
kill $FULL 2>/dev/null || true
wait $FULL 2>/dev/null || true
if grep -q 'stopping (unmodeled device' "$RUN/full.log"; then
    echo "   FAIL -- unmodelled access:"
    grep 'stopping (unmodeled device' "$RUN/full.log" | head -3
    exit 1
fi
if ! grep -q BusyBox "$RUN/serial.log" 2>/dev/null; then
    echo "   FAIL -- no BusyBox prompt within ${WAIT}s. Serial tail:"
    tail -5 "$RUN/serial.log" 2>/dev/null || echo "   (serial log empty)"
    exit 1
fi
echo "   ok -- $(grep -c '' "$RUN/serial.log") serial lines, BusyBox reached"
echo
echo "PASS"
