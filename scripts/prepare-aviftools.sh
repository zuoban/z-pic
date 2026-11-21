#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

libavif_version="1.4.2"
libaom_version="3.14.1"
libpng_version="1.6.58"
mozjpeg_version="4.1.5"

libavif_sha="2b645287340ba5a631d268b551dc2d72bd73ac33335962dd36dcdb6d8366921d"
libaom_sha="44bf90dbd23e734d50e70a8c41c285193922938bd0d3bc2ee56764d181d55ef5"
libpng_sha="a9d4df463d36a6e5f9c29bd6f4967312d17e996c1854f3511f833924eb1993cf"
mozjpeg_sha="9fcbb7171f6ac383f5b391175d6fb3acde5e64c4c4727274eade84ed0998fcc1"

libavif_archive="$repo_root/scripts/sources/libavif-${libavif_version}.tar.gz"
libaom_archive="$repo_root/scripts/sources/libaom-${libaom_version}.tar.gz"
libpng_archive="$repo_root/scripts/sources/libpng-${libpng_version}.tar.gz"
mozjpeg_archive="$repo_root/scripts/sources/mozjpeg-${mozjpeg_version}.tar.gz"

asset_dir="$repo_root/internal/aviftools/assets/darwin_arm64"

[[ "$(uname -s)/$(uname -m)" == Darwin/arm64 ]] || {
  echo "Use a native darwin/arm64 host." >&2
  exit 1
}
for tool in shasum python3 codesign; do
  command -v "$tool" >/dev/null || { echo "Missing required tool: $tool" >&2; exit 1; }
done

{
  printf '%s  %s\n' "$libavif_sha" "$libavif_archive"
  printf '%s  %s\n' "$libaom_sha" "$libaom_archive"
  printf '%s  %s\n' "$libpng_sha" "$libpng_archive"
  printf '%s  %s\n' "$mozjpeg_sha" "$mozjpeg_archive"
} | shasum -a 256 -c -

if [[ "${1:-}" == --verify ]]; then
  python3 - "$asset_dir" "$libavif_version" "$libaom_version" "$libpng_version" "$mozjpeg_version" \
    "$libavif_sha" "$libaom_sha" "$libpng_sha" "$mozjpeg_sha" <<'PY'
import hashlib, json, pathlib, sys
root = pathlib.Path(sys.argv[1])
manifest = json.loads((root / "manifest.json").read_text())
assert manifest["version"] == sys.argv[2]
assert manifest["sources"]["libavif"]["sha256"] == sys.argv[6]
assert manifest["sources"]["libaom"]["version"] == sys.argv[3]
assert manifest["sources"]["libaom"]["sha256"] == sys.argv[7]
assert manifest["sources"]["libpng"]["version"] == sys.argv[4]
assert manifest["sources"]["libpng"]["sha256"] == sys.argv[8]
assert manifest["sources"]["mozjpeg"]["version"] == sys.argv[5]
assert manifest["sources"]["mozjpeg"]["sha256"] == sys.argv[9]
for name in ("avifenc", "avifdec"):
    assert manifest["binaries"][name]["sha256"] == hashlib.sha256((root / name).read_bytes()).hexdigest()
PY
  for name in avifenc avifdec; do
    codesign --verify --strict "$asset_dir/$name"
    env -i "$asset_dir/$name" --version 2>&1 | head -n 1
  done
  exit 0
fi
[[ $# == 0 ]] || { echo "Usage: $0 [--verify]" >&2; exit 1; }

for tool in cmake clang clang++ make tar strip otool xcrun perl; do
  command -v "$tool" >/dev/null || { echo "Missing build tool: $tool" >&2; exit 1; }
done

work_dir="$(mktemp -d "${TMPDIR:-/tmp}/zpic-aviftools-build.XXXXXX")"
trap 'rm -rf "$work_dir"' EXIT

tar -xzf "$libavif_archive" -C "$work_dir"
tar -xzf "$libaom_archive" -C "$work_dir"
tar -xzf "$libpng_archive" -C "$work_dir"
tar -xzf "$mozjpeg_archive" -C "$work_dir"

avif_src="$work_dir/libavif-${libavif_version}"
aom_src="$work_dir/libaom-${libaom_version}"
png_src="$work_dir/libpng-${libpng_version}"
jpeg_src="$work_dir/mozjpeg-${mozjpeg_version}"

# Prevent libavif's unused avifgainmaputil target from attempting a network FetchContent.
mkdir -p "$avif_src/ext/libargparse/build" "$avif_src/ext/libargparse/src"
touch "$avif_src/ext/libargparse/build/libargparse.a"

cmake_bin="$(command -v cmake)"
jobs="$(sysctl -n hw.ncpu 2>/dev/null || echo 4)"
cc_bin="$(xcrun --find clang)"
cxx_bin="$(xcrun --find clang++)"
export SDKROOT="$(xcrun --show-sdk-path)"

common_cflags="-O2 -arch arm64 -mmacosx-version-min=11.0"
common_cxxflags="-O2 -arch arm64 -mmacosx-version-min=11.0"
common_ldflags="-arch arm64 -mmacosx-version-min=11.0 -Wl,-dead_strip"

run_cmake() {
  env -i PATH=/usr/bin:/bin:/usr/sbin:/sbin LC_ALL=C \
    SDKROOT="$SDKROOT" \
    "$cmake_bin" "$@"
}

# 1. Build static libpng against macOS SDK zlib.
png_build="$work_dir/build-png"
png_stage="$work_dir/stage-png"
run_cmake -S "$png_src" -B "$png_build" \
  -DCMAKE_BUILD_TYPE=Release \
  -DCMAKE_OSX_ARCHITECTURES=arm64 \
  -DCMAKE_OSX_DEPLOYMENT_TARGET=11.0 \
  -DCMAKE_C_COMPILER="$cc_bin" \
  -DCMAKE_C_FLAGS="$common_cflags" \
  -DCMAKE_IGNORE_PREFIX_PATH="/opt/homebrew;/usr/local" \
  -DCMAKE_DISABLE_FIND_PACKAGE_PkgConfig=ON \
  -DCMAKE_INSTALL_PREFIX="$png_stage" \
  -DPNG_SHARED=OFF \
  -DPNG_STATIC=ON \
  -DPNG_TESTS=OFF \
  -DPNG_TOOLS=OFF \
  -DPNG_FRAMEWORK=OFF \
  -DPNG_HARDWARE_OPTIMIZATIONS=ON
run_cmake --build "$png_build" --target png_static -j"$jobs"
run_cmake --install "$png_build"

# 2. Build static libjpeg from MozJPEG.
jpeg_build="$work_dir/build-jpeg"
jpeg_stage="$work_dir/stage-jpeg"
run_cmake -S "$jpeg_src" -B "$jpeg_build" \
  -DCMAKE_POLICY_VERSION_MINIMUM=3.5 \
  -DCMAKE_BUILD_TYPE=Release \
  -DCMAKE_OSX_ARCHITECTURES=arm64 \
  -DCMAKE_OSX_DEPLOYMENT_TARGET=11.0 \
  -DCMAKE_C_COMPILER="$cc_bin" \
  -DCMAKE_C_FLAGS="$common_cflags" \
  -DCMAKE_IGNORE_PREFIX_PATH="/opt/homebrew;/usr/local" \
  -DCMAKE_DISABLE_FIND_PACKAGE_PkgConfig=ON \
  -DCMAKE_INSTALL_PREFIX="$jpeg_stage" \
  -DENABLE_SHARED=OFF \
  -DENABLE_STATIC=ON \
  -DWITH_TURBOJPEG=OFF \
  -DWITH_JAVA=OFF \
  -DPNG_SUPPORTED=OFF \
  -DWITH_SIMD=ON \
  -DREQUIRE_SIMD=ON
run_cmake --build "$jpeg_build" --target jpeg-static -j"$jobs"
mkdir -p "$jpeg_stage/include" "$jpeg_stage/lib"
cp "$jpeg_build/libjpeg.a" "$jpeg_stage/lib/libjpeg.a"
cp "$jpeg_build/jconfig.h" "$jpeg_src/jpeglib.h" "$jpeg_src/jmorecfg.h" "$jpeg_src/jerror.h" "$jpeg_stage/include/"

# 3. Build encode-only static libaom (for avifenc) and decode-only static libaom (for avifdec).
aom_common_flags=(
  -DCMAKE_BUILD_TYPE=Release
  -DCMAKE_OSX_ARCHITECTURES=arm64
  -DCMAKE_OSX_DEPLOYMENT_TARGET=11.0
  -DCMAKE_C_COMPILER="$cc_bin"
  -DCMAKE_CXX_COMPILER="$cxx_bin"
  -DCMAKE_C_FLAGS="$common_cflags"
  -DCMAKE_CXX_FLAGS="$common_cxxflags"
  -DCMAKE_IGNORE_PREFIX_PATH="/opt/homebrew;/usr/local"
  -DCMAKE_DISABLE_FIND_PACKAGE_PkgConfig=ON
  -DBUILD_SHARED_LIBS=OFF
  -DCONFIG_PIC=1
  -DENABLE_DOCS=0
  -DENABLE_EXAMPLES=0
  -DENABLE_TESTDATA=0
  -DENABLE_TESTS=0
  -DENABLE_TOOLS=0
  -DCONFIG_AV1_HIGHBITDEPTH=1
)

aom_enc_build="$work_dir/build-aom-enc"
run_cmake -S "$aom_src" -B "$aom_enc_build" \
  "${aom_common_flags[@]}" \
  -DCONFIG_AV1_ENCODER=1 \
  -DCONFIG_AV1_DECODER=0
run_cmake --build "$aom_enc_build" --target aom -j"$jobs"

aom_dec_build="$work_dir/build-aom-dec"
run_cmake -S "$aom_src" -B "$aom_dec_build" \
  "${aom_common_flags[@]}" \
  -DCONFIG_AV1_ENCODER=0 \
  -DCONFIG_AV1_DECODER=1
run_cmake --build "$aom_dec_build" --target aom -j"$jobs"

# 4. Build static avifenc and avifdec.
avif_common_flags=(
  -DCMAKE_BUILD_TYPE=Release
  -DCMAKE_OSX_ARCHITECTURES=arm64
  -DCMAKE_OSX_DEPLOYMENT_TARGET=11.0
  -DCMAKE_C_COMPILER="$cc_bin"
  -DCMAKE_CXX_COMPILER="$cxx_bin"
  -DCMAKE_C_FLAGS="$common_cflags"
  -DCMAKE_CXX_FLAGS="$common_cxxflags"
  -DCMAKE_EXE_LINKER_FLAGS="$common_ldflags"
  -DCMAKE_IGNORE_PREFIX_PATH="/opt/homebrew;/usr/local"
  -DCMAKE_DISABLE_FIND_PACKAGE_PkgConfig=ON
  -DBUILD_SHARED_LIBS=OFF
  -DAVIF_BUILD_APPS=ON
  -DAVIF_BUILD_TESTS=OFF
  -DAVIF_BUILD_EXAMPLES=OFF
  -DAVIF_BUILD_MAN_PAGES=OFF
  -DAVIF_CODEC_AOM=SYSTEM
  -DAVIF_LIBYUV=OFF
  -DAVIF_LIBSHARPYUV=OFF
  -DAVIF_LIBXML2=OFF
  -DAVIF_ZLIBPNG=SYSTEM
  -DPNG_PNG_INCLUDE_DIR="$png_stage/include/libpng16"
  -DPNG_LIBRARY="$png_stage/lib/libpng16.a"
  -DAVIF_JPEG=SYSTEM
  -DJPEG_INCLUDE_DIR="$jpeg_stage/include"
  -DJPEG_LIBRARY="$jpeg_stage/lib/libjpeg.a"
  -DAOM_INCLUDE_DIR="$aom_src"
)

avif_enc_build="$work_dir/build-avif-enc"
run_cmake -S "$avif_src" -B "$avif_enc_build" \
  "${avif_common_flags[@]}" \
  -DAVIF_CODEC_AOM_ENCODE=ON \
  -DAVIF_CODEC_AOM_DECODE=OFF \
  -DAOM_LIBRARY="$aom_enc_build/libaom.a"
run_cmake --build "$avif_enc_build" --target avifenc -j"$jobs"

avif_dec_build="$work_dir/build-avif-dec"
run_cmake -S "$avif_src" -B "$avif_dec_build" \
  "${avif_common_flags[@]}" \
  -DAVIF_CODEC_AOM_ENCODE=OFF \
  -DAVIF_CODEC_AOM_DECODE=ON \
  -DAOM_LIBRARY="$aom_dec_build/libaom.a"
run_cmake --build "$avif_dec_build" --target avifdec -j"$jobs"

mkdir -p "$asset_dir"
for pair in "avifenc:$avif_enc_build/avifenc" "avifdec:$avif_dec_build/avifdec"; do
  name="${pair%%:*}"
  binary="${pair#*:}"
  strip -x "$binary"
  codesign --force --sign - "$binary"
  codesign --verify --strict "$binary"
  python3 - "$binary" "$name" "$libavif_version" <<'PY'
import subprocess, sys
binary, name, version = sys.argv[1], sys.argv[2], sys.argv[3]
for line in subprocess.check_output(["otool", "-L", binary], text=True).splitlines()[1:]:
    dependency = line.strip().split(" (")[0]
    if not dependency.startswith(("/usr/lib/", "/System/Library/")):
        raise SystemExit(f"Non-system dynamic dependency in {name}: {dependency}")
proc = subprocess.run([binary, "--version"], text=True, env={}, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
out = (proc.stdout or "").strip()
if f"Version: {version}" not in out or "aom" not in out:
    raise SystemExit(f"Wrong {name} version output: {out!r}")
PY
  install -m 755 "$binary" "$asset_dir/$name"
done

install -m 644 "$avif_src/LICENSE" "$repo_root/licenses/libavif-LICENSE"
install -m 644 "$aom_src/LICENSE" "$repo_root/licenses/libaom-LICENSE"
install -m 644 "$aom_src/PATENTS" "$repo_root/licenses/libaom-PATENTS"
install -m 644 "$aom_src/AUTHORS" "$repo_root/licenses/libaom-AUTHORS"
install -m 644 "$png_src/LICENSE" "$repo_root/licenses/libpng-LICENSE"

python3 - "$asset_dir" "$libavif_version" "$libaom_version" "$libpng_version" "$mozjpeg_version" \
  "$libavif_sha" "$libaom_sha" "$libpng_sha" "$mozjpeg_sha" <<'PY'
import hashlib, json, pathlib, subprocess, sys
root = pathlib.Path(sys.argv[1])
binaries = {}
for name in ("avifenc", "avifdec"):
    data = (root / name).read_bytes()
    binaries[name] = {
        "sha256": hashlib.sha256(data).hexdigest(),
        "size": len(data),
    }
manifest = {
    "version": sys.argv[2],
    "sources": {
        "libavif": {
            "version": sys.argv[2],
            "url": f"https://github.com/AOMediaCodec/libavif/archive/refs/tags/v{sys.argv[2]}.tar.gz",
            "sha256": sys.argv[6],
        },
        "libaom": {
            "version": sys.argv[3],
            "url": f"https://storage.googleapis.com/aom-releases/libaom-{sys.argv[3]}.tar.gz",
            "sha256": sys.argv[7],
        },
        "libpng": {
            "version": sys.argv[4],
            "url": f"https://github.com/pnggroup/libpng/archive/refs/tags/v{sys.argv[4]}.tar.gz",
            "sha256": sys.argv[8],
        },
        "mozjpeg": {
            "version": sys.argv[5],
            "url": f"https://github.com/mozilla/mozjpeg/archive/refs/tags/v{sys.argv[5]}.tar.gz",
            "sha256": sys.argv[9],
        },
    },
    "binaries": binaries,
    "cflags": "-O2 -arch arm64 -mmacosx-version-min=11.0",
    "ldflags": "-arch arm64 -mmacosx-version-min=11.0 -Wl,-dead_strip",
    "compiler": subprocess.check_output(["clang", "--version"], text=True).splitlines()[0],
    "sdk": subprocess.check_output(["xcrun", "--show-sdk-version"], text=True).strip(),
}
(root / "manifest.json").write_text(json.dumps(manifest, indent=2) + "\n")
PY

for name in avifenc avifdec; do
  otool -L "$asset_dir/$name"
  ls -lh "$asset_dir/$name"
done
echo "Prepared native asset: $asset_dir"
