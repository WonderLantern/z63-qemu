#!/bin/sh
# The body's pictures, from the originals in this directory, into src/assets
# (which skin.ts imports and the build embeds):
#
#   back.png  nik.png, the back-view illustration, its baked-in checkerboard
#             made transparent; same size, so skin.ts measures on nik.png
#   top.png   top.png, the top view, without its lens and trimmed; skin.ts
#             measures on the result
#
# The lens: its hood band, then its mount band down to the body's front edge
# at y 379 -- narrower, so the two small bumps either side of the mount
# (x 163-187 and 420-443), which are the body's, stay -- and the hood's
# right edge above that bump.
set -e
cd "$(dirname "$0")/.."
go run ./cmd/cutout skin/nik.png src/assets/back.png
go run ./cmd/cutout -fill=false -erase 180,0,436,290 -erase 193,290,420,379 -erase 419,290,424,370 -trim 4 \
    skin/top.png src/assets/top.png
