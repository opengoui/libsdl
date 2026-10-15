#!/usr/bin/env bash
# Reproducible build of libSDL3.so.0 for linux/amd64 and linux/arm64 using zig as the cross compiler.
#
# usage: build.sh <version|tag> <outdir>        e.g. build.sh 3.4.18 /tmp/out
#   <version> is "3.4.18" or "release-3.4.18" (a release tag of libsdl-org/SDL).
#   Output: <outdir>/SDL3_linux_amd64.so, SDL3_linux_arm64.so, SHA256SUMS, build-report.txt
#
# Requirements: zig >= 0.17 (headers + glibc stubs), cmake, ninja, curl, docker, tar.
# Environment overrides:
#   WORK             scratch dir (default: <script dir>/work)
#   GLIBC            glibc baseline passed to zig (default 2.17)
#   DEBIAN_SNAPSHOT  optional snapshot.debian.org timestamp (e.g. 20261001T000000Z) to freeze apt packages too
#   JOBS             parallel build jobs
#
# How it works:
#  1. The SDL release tarball is downloaded and its sha256 recorded.
#  2. A "sysroot" (headers + .pc files + libs, used only for configure/link checks) is extracted from a Debian image
#     pinned by digest, one per arch, holding the -dev packages of every backend SDL can dlopen.
#  3. wayland-scanner and GNU binutils (cross strip/readelf/objdump) run in a pinned native docker image.
#  4. SDL is configured with a CMake toolchain file around `zig cc -target <arch>-linux-gnu.2.17`. libc comes from zig
#     (glibc 2.17 symbol baseline), not from the sysroot. All optional backends are *_SHARED (dlopen at runtime) so
#     the library NEEDs only libc-family libraries.
#  5. The result is stripped with GNU strip (zig objcopy is unimplemented) and verified.
set -euo pipefail

VERSION=${1:?usage: build.sh <version> <outdir>}
OUTDIR=${2:?usage: build.sh <version> <outdir>}
TAG=$VERSION
case $TAG in release-*) ;; *) TAG=release-$TAG ;; esac
VER=${TAG#release-}
case $TAG in
  release-[0-9]*.[0-9]*.[0-9]*) ;;
  *) echo "invalid release tag: $TAG" >&2; exit 2 ;;
esac
case $TAG in *[!A-Za-z0-9.-]*) echo "invalid release tag: $TAG" >&2; exit 2 ;; esac

# Pinned multi-arch index digest of debian:trixie-slim (verified 2026-10-13).
DEBIAN_IMAGE='debian:trixie-slim@sha256:a29215f6a35e51e22adffa17f89e9d2ef06214e64a2bad10d765c46aea49f11f'
GLIBC=${GLIBC:-2.17}
JOBS=${JOBS:-$(sysctl -n hw.ncpu 2>/dev/null || nproc)}
HERE=$(cd "$(dirname "$0")" && pwd)
WORK=${WORK:-$HERE/work}
mkdir -p "$WORK" "$OUTDIR"
WORK=$(cd "$WORK" && pwd); OUTDIR=$(cd "$OUTDIR" && pwd)

# Dev packages whose headers/.pc files SDL's configure inspects. Everything is loaded by dlopen at runtime.
# libusb is intentionally NOT installed (HIDAPI uses the hidraw backend; no libusb dependency).
PKGS="libx11-dev libxext-dev libxrandr-dev libxcursor-dev libxfixes-dev libxi-dev libxss-dev libxtst-dev
 libxrender-dev libxkbcommon-dev libwayland-dev wayland-protocols libdecor-0-dev libasound2-dev libpulse-dev
 libpipewire-0.3-dev libjack-jackd2-dev libsndio-dev libdbus-1-dev libudev-dev libibus-1.0-dev libdrm-dev
 libgbm-dev libegl-dev libgl-dev libgles-dev libvulkan-dev libxcb1-dev libxdmcp-dev libxau-dev x11proto-dev
 libffi-dev libfribidi-dev libthai-dev pkgconf"

PKGS=$(echo $PKGS)   # collapse to a single line
SRC_TGZ=$WORK/SDL-$TAG.tar.gz
SRC=$WORK/SDL-$TAG
TOOLS_IMG=libsdl-linux-tools:$VER

log() { printf '\n== %s\n' "$*"; }

# ---------------------------------------------------------------- 1. source
if [ ! -f "$SRC_TGZ" ]; then
  log "download $TAG"
  curl -fsSL -A libsdl-build ${GITHUB_TOKEN:+-H "Authorization: Bearer $GITHUB_TOKEN"} \
    -o "$SRC_TGZ.part" "https://github.com/libsdl-org/SDL/archive/refs/tags/$TAG.tar.gz"
  mv "$SRC_TGZ.part" "$SRC_TGZ"
fi
if [ ! -d "$SRC" ]; then
  mkdir -p "$SRC.tmp" && tar -xzf "$SRC_TGZ" -C "$SRC.tmp" --strip-components=1 && mv "$SRC.tmp" "$SRC"
fi
SRC_SHA=$(shasum -a 256 "$SRC_TGZ" | cut -d' ' -f1)

# ---------------------------------------------------------------- 2. tools image (native arch)
log "tools image"
APT_PREP='true'
if [ -n "${DEBIAN_SNAPSHOT:-}" ]; then
  APT_PREP="rm -f /etc/apt/sources.list.d/*; echo 'deb http://snapshot.debian.org/archive/debian/$DEBIAN_SNAPSHOT/ trixie main' > /etc/apt/sources.list; echo 'Acquire::Check-Valid-Until false;' > /etc/apt/apt.conf.d/99snap"
fi
docker build -q -t "$TOOLS_IMG" - >/dev/null <<DOCKERFILE
FROM $DEBIAN_IMAGE
RUN $APT_PREP; apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq --no-install-recommends \\
    libwayland-bin binutils-x86-64-linux-gnu binutils-aarch64-linux-gnu >/dev/null
DOCKERFILE

# ---------------------------------------------------------------- 3. wayland-scanner wrapper (runs in docker)
mkdir -p "$WORK/wrap"
cat > "$WORK/wrap/wayland-scanner" <<WRAP
#!/bin/sh
# WORK is mounted at the same path so file arguments resolve unchanged.
exec docker run --rm -v "$WORK:$WORK" $TOOLS_IMG wayland-scanner "\$@"
WRAP
chmod +x "$WORK/wrap/wayland-scanner"

# ---------------------------------------------------------------- 4. toolchain file
cat > "$WORK/toolchain.cmake" <<'TOOLCHAIN'
# -DARCH=x86_64|aarch64 -DZIG_TRIPLE=<arch>-linux-gnu.<glibc> -DSYSROOT=<dir>
set(CMAKE_SYSTEM_NAME Linux)
set(CMAKE_SYSTEM_PROCESSOR ${ARCH})
file(WRITE ${CMAKE_BINARY_DIR}/zcc "#!/bin/sh\nexec zig cc -target ${ZIG_TRIPLE} \"$@\"\n")
file(WRITE ${CMAKE_BINARY_DIR}/zcxx "#!/bin/sh\nexec zig c++ -target ${ZIG_TRIPLE} \"$@\"\n")
file(WRITE ${CMAKE_BINARY_DIR}/zar "#!/bin/sh\nexec zig ar \"$@\"\n")
file(WRITE ${CMAKE_BINARY_DIR}/zranlib "#!/bin/sh\nexec zig ranlib \"$@\"\n")
execute_process(COMMAND chmod +x ${CMAKE_BINARY_DIR}/zcc ${CMAKE_BINARY_DIR}/zcxx ${CMAKE_BINARY_DIR}/zar ${CMAKE_BINARY_DIR}/zranlib)
set(CMAKE_C_COMPILER ${CMAKE_BINARY_DIR}/zcc)
set(CMAKE_CXX_COMPILER ${CMAKE_BINARY_DIR}/zcxx)
set(CMAKE_AR ${CMAKE_BINARY_DIR}/zar)
set(CMAKE_RANLIB ${CMAKE_BINARY_DIR}/zranlib)
# Headers/pkg-config data come from the Debian sysroot; libc is zig's own (do NOT pass --sysroot).
set(CMAKE_FIND_ROOT_PATH ${SYSROOT})
set(CMAKE_LIBRARY_ARCHITECTURE ${ARCH}-linux-gnu)
set(CMAKE_FIND_ROOT_PATH_MODE_PROGRAM NEVER)
set(CMAKE_FIND_ROOT_PATH_MODE_LIBRARY ONLY)
set(CMAKE_FIND_ROOT_PATH_MODE_INCLUDE ONLY)
set(CMAKE_FIND_ROOT_PATH_MODE_PACKAGE ONLY)
# Never pick anything from the host.
set(CMAKE_IGNORE_PREFIX_PATH /opt/homebrew /usr/local /opt/local)
set(CMAKE_C_FLAGS_INIT "-isystem ${SYSROOT}/usr/include -isystem ${SYSROOT}/usr/include/${ARCH}-linux-gnu -ffile-prefix-map=${SYSROOT}=/sysroot -fdebug-prefix-map=${SYSROOT}=/sysroot")
set(CMAKE_SHARED_LINKER_FLAGS_INIT "-L${SYSROOT}/usr/lib/${ARCH}-linux-gnu")
TOOLCHAIN

# ---------------------------------------------------------------- 5. per-arch build
build_arch() { # <goarch> <march>
  local goarch=$1 march=$2
  local build_key=$VER-$GLIBC
  if [ -n "${DEBIAN_SNAPSHOT:-}" ]; then build_key=$build_key-$DEBIAN_SNAPSHOT; fi
  local arch_dir=$WORK/$build_key/$goarch
  local sysroot_key=${DEBIAN_SNAPSHOT:-default}
  local sysroot=$WORK/sysroot-$sysroot_key/$goarch
  local triple=$march-linux-gnu.$GLIBC
  mkdir -p "$arch_dir"

  if [ ! -f "$sysroot/.done" ]; then
    log "sysroot linux/$goarch (slow when emulated)"
    rm -rf "$sysroot"; mkdir -p "$sysroot"
    docker run --rm --platform linux/$goarch -v "$sysroot:/out" "$DEBIAN_IMAGE" sh -c "
      set -e; $APT_PREP
      apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq --no-install-recommends $PKGS >/dev/null
      mkdir -p /out/usr/lib /out/usr/share
      # netfilter headers differ only by case (ipt_ECN.h/ipt_ecn.h) and collide on case-insensitive hosts; SDL does not use them.
      tar -C /usr --exclude='include/linux/netfilter*' -cf - include | tar -C /out/usr -xf -
      cp -a /usr/lib/$march-linux-gnu /out/usr/lib/
      cp -a /usr/share/pkgconfig /out/usr/share/ 2>/dev/null || true
      cp -a /usr/share/wayland /usr/share/wayland-protocols /out/usr/share/
      dpkg-query -W -f='\${Package} \${Version}\n' > /out/packages.txt"
    touch "$sysroot/.done"
  fi

  log "configure + build linux/$goarch ($triple)"
  local B=$arch_dir/build
  # pkg-config is hermetic: only the sysroot is searched. wayland-scanner comes from the docker wrapper.
  PATH=$WORK/wrap:$PATH \
  PKG_CONFIG_LIBDIR=$sysroot/usr/lib/$march-linux-gnu/pkgconfig:$sysroot/usr/share/pkgconfig \
  PKG_CONFIG_SYSROOT_DIR=$sysroot \
  cmake -S "$SRC" -B "$B" -G Ninja \
    -DCMAKE_TOOLCHAIN_FILE="$WORK/toolchain.cmake" -DARCH=$march -DZIG_TRIPLE=$triple -DSYSROOT="$sysroot" \
    -DCMAKE_BUILD_TYPE=Release -DSDL_SHARED=ON -DSDL_STATIC=OFF -DSDL_TESTS=OFF -DSDL_EXAMPLES=OFF -DSDL_INSTALL=OFF \
    -DSDL_REVISION="SDL-$TAG" -DSDL_VENDOR_INFO="libsdl zig build" \
    -DSDL_UNIX_CONSOLE_BUILD=OFF -DSDL_RPATH=OFF -DSDL_HIDAPI_LIBUSB=OFF \
    -DSDL_X11=ON -DSDL_WAYLAND=ON -DSDL_KMSDRM=ON -DSDL_OFFSCREEN=ON \
    -DSDL_X11_SHARED=ON -DSDL_WAYLAND_SHARED=ON -DSDL_KMSDRM_SHARED=ON -DSDL_LIBDECOR_SHARED=ON \
    -DSDL_ALSA=ON -DSDL_ALSA_SHARED=ON -DSDL_PULSEAUDIO=ON -DSDL_PULSEAUDIO_SHARED=ON \
    -DSDL_PIPEWIRE=ON -DSDL_PIPEWIRE_SHARED=ON -DSDL_JACK=ON -DSDL_JACK_SHARED=ON \
    -DSDL_SNDIO=ON -DSDL_SNDIO_SHARED=ON -DSDL_VULKAN=ON -DSDL_OPENGL=ON -DSDL_OPENGLES=ON \
    > "$arch_dir/configure.log" 2>&1 || { tail -40 "$arch_dir/configure.log"; exit 1; }
  cmake --build "$B" -j"$JOBS" > "$arch_dir/build.log" 2>&1 || { tail -40 "$arch_dir/build.log"; exit 1; }

  log "strip linux/$goarch"
  local real; real=$(ls "$B"/libSDL3.so.0.*.* | head -1)
  cp "$real" "$OUTDIR/SDL3_linux_$goarch.so"
  docker run --rm -v "$OUTDIR:/o" "$TOOLS_IMG" $march-linux-gnu-strip --strip-unneeded "/o/SDL3_linux_$goarch.so"
}

verify_arch() { # <goarch> <march>; prints facts, FAIL lines on violations
  local goarch=$1 march=$2
  local f=/o/SDL3_linux_$goarch.so
  local rpt; rpt=$(docker run --rm -v "$OUTDIR:/o:ro" "$TOOLS_IMG" sh -c "
    P=$march-linux-gnu
    \$P-readelf -h $f | grep -E 'Machine|Type'
    \$P-readelf -d $f | grep -E 'SONAME|NEEDED|RPATH|RUNPATH'
    echo GLIBCMAX \$(\$P-objdump -T $f | grep -o 'GLIBC_[0-9.]*' | sort -uV | tail -1)
    echo EXPORTS \$(\$P-objdump -T $f | grep -c ' SDL_')
  ")
  printf '%s\n' "$rpt"
  grep -q 'SONAME.*\[libSDL3.so.0\]' <<<"$rpt" || echo "FAIL: SONAME"
  local bad; bad=$(grep NEEDED <<<"$rpt" | grep -Ev '\[(libc|libm|libdl|libpthread|librt|ld-linux[^]]*)\.so[.0-9]*\]' || true)
  [ -z "$bad" ] || echo "FAIL: unexpected NEEDED: $bad"
  local gmax; gmax=$(awk '/GLIBCMAX/{print $2}' <<<"$rpt" | sed 's/GLIBC_//')
  [ -n "$gmax" ] && [ "$(printf '%s\n2.17\n' "$gmax" | sort -V | tail -1)" = 2.17 ] || echo "FAIL: GLIBC_$gmax > 2.17"
  if grep -q 'RPATH\|RUNPATH' <<<"$rpt"; then echo "FAIL: RPATH/RUNPATH present"; fi
}

build_arch amd64 x86_64
build_arch arm64 aarch64

log "verify"
{
  echo "SDL $TAG  source tarball sha256 $SRC_SHA"
  echo "base image $DEBIAN_IMAGE ${DEBIAN_SNAPSHOT:+snapshot $DEBIAN_SNAPSHOT}; zig $(zig version); glibc baseline $GLIBC"
  for a in "amd64 x86_64" "arm64 aarch64"; do
    set -- $a
    echo "--- linux/$1"
    verify_arch "$1" "$2"
    printf 'size %s bytes, gzip -9 %s bytes\n' "$(wc -c < "$OUTDIR/SDL3_linux_$1.so" | tr -d ' ')" "$(gzip -9nc "$OUTDIR/SDL3_linux_$1.so" | wc -c | tr -d ' ')"
  done
} 2>&1 | tee "$OUTDIR/build-report.txt"
( cd "$OUTDIR" && shasum -a 256 SDL3_linux_amd64.so SDL3_linux_arm64.so | tee SHA256SUMS )
if grep -q '^FAIL' "$OUTDIR/build-report.txt"; then echo "VERIFICATION FAILED"; exit 1; fi
echo "OK"
