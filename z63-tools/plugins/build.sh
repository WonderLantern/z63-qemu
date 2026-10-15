#!/bin/sh
# Build the z63 TCG plugins against this tree's plugin header. The QEMU
# that loads them must be configured with -Dplugins=true.
set -e
here=$(cd "$(dirname "$0")" && pwd)
top=$(cd "$here/../.." && pwd)
case "$(uname)" in
Darwin) ext=dylib; ld="-dynamiclib -undefined dynamic_lookup" ;;
*)      ext=so;    ld="-shared" ;;
esac
for src in "$here"/*.c; do
    name=$(basename "$src" .c)
    cc -O2 -fPIC -Wall $ld -I"$top/include/qemu" \
        $(pkg-config --cflags glib-2.0) \
        -o "$here/lib$name.$ext" "$src"
    echo "built $here/lib$name.$ext"
done
