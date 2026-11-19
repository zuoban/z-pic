#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source_sha=9fcbb7171f6ac383f5b391175d6fb3acde5e64c4c4727274eade84ed0998fcc1
archive="$repo_root/scripts/sources/mozjpeg-4.1.5.tar.gz"
asset_dir="$repo_root/internal/jpegtools/assets/darwin_arm64"

[[ "$(uname -s)/$(uname -m)" == Darwin/arm64 ]] || {
  echo "Use a native darwin/arm64 host." >&2
  exit 1
}
for tool in shasum python3 codesign; do
  command -v "$tool" >/dev/null || { echo "Missing required tool: $tool" >&2; exit 1; }
done
printf '%s  %s\n' "$source_sha" "$archive" | shasum -a 256 -c -
if [[ "${1:-}" == --verify ]]; then
  python3 - "$asset_dir" "$source_sha" <<'PY'
import hashlib, json, pathlib, sys
root = pathlib.Path(sys.argv[1])
manifest = json.loads((root / "manifest.json").read_text())
assert manifest["version"] == "4.1.5"
assert manifest["source_sha256"] == sys.argv[2]
for name in ("cjpeg", "jpegtran"):
    assert manifest["binaries"][name]["sha256"] == hashlib.sha256((root / name).read_bytes()).hexdigest()
PY
  for name in cjpeg jpegtran; do
    codesign --verify --strict "$asset_dir/$name"
    env -i "$asset_dir/$name" -version 2>&1 | head -n 1
  done
  exit 0
fi
[[ $# == 0 ]] || { echo "Usage: $0 [--verify]" >&2; exit 1; }

for tool in cmake clang make tar strip otool xcrun; do
  command -v "$tool" >/dev/null || { echo "Missing build tool: $tool" >&2; exit 1; }
done

work_dir="$(mktemp -d "${TMPDIR:-/tmp}/zpic-jpegtools-build.XXXXXX")"
trap 'rm -rf "$work_dir"' EXIT
tar -xzf "$archive" -C "$work_dir"
source_dir="$work_dir/mozjpeg-4.1.5"
build_dir="$work_dir/build"
mkdir -p "$build_dir"

cmake_bin="$(command -v cmake)"
cmake_flags=(
  -DCMAKE_POLICY_VERSION_MINIMUM=3.5
  -DCMAKE_BUILD_TYPE=Release
  -DCMAKE_OSX_ARCHITECTURES=arm64
  -DCMAKE_OSX_DEPLOYMENT_TARGET=11.0
  -DENABLE_SHARED=OFF
  -DENABLE_STATIC=ON
  -DWITH_TURBOJPEG=OFF
  -DWITH_JAVA=OFF
  -DPNG_SUPPORTED=OFF
  -DWITH_SIMD=ON
  -DREQUIRE_SIMD=ON
)

(
  export SDKROOT="$(xcrun --show-sdk-path)"
  env -i PATH=/usr/bin:/bin:/usr/sbin:/sbin LC_ALL=C \
    SDKROOT="$SDKROOT" \
    "$cmake_bin" -S "$source_dir" -B "$build_dir" \
      -DCMAKE_C_COMPILER="$(xcrun --find clang)" \
      -DCMAKE_C_FLAGS="-O2 -arch arm64 -mmacosx-version-min=11.0" \
      -DCMAKE_EXE_LINKER_FLAGS="-arch arm64 -mmacosx-version-min=11.0" \
      "${cmake_flags[@]}"
  env -i PATH=/usr/bin:/bin:/usr/sbin:/sbin LC_ALL=C \
    SDKROOT="$SDKROOT" \
    "$cmake_bin" --build "$build_dir" --target cjpeg-static jpegtran-static -j2
)

mkdir -p "$asset_dir"
for name in cjpeg jpegtran; do
  binary="$build_dir/${name}-static"
  strip -x "$binary"
  codesign --force --sign - "$binary"
  codesign --verify --strict "$binary"
  python3 - "$binary" "$name" <<'PY'
import subprocess, sys
binary, name = sys.argv[1], sys.argv[2]
for line in subprocess.check_output(["otool", "-L", binary], text=True).splitlines()[1:]:
    dependency = line.strip().split(" (")[0]
    if not dependency.startswith(("/usr/lib/", "/System/Library/")):
        raise SystemExit(f"Non-system dynamic dependency in {name}: {dependency}")
proc = subprocess.run([binary, "-version"], text=True, env={}, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
out = (proc.stdout or "").strip()
if "mozjpeg version 4.1.5" not in out:
    raise SystemExit(f"Wrong {name} version output: {out!r}")
PY
  install -m 755 "$binary" "$asset_dir/$name"
done

for name in LICENSE.md README.ijg README-mozilla.txt; do
  install -m 644 "$source_dir/$name" "$repo_root/licenses/mozjpeg-$name"
done

python3 - "$asset_dir" "$source_sha" "${cmake_flags[@]}" <<'PY'
import hashlib, json, pathlib, subprocess, sys
root = pathlib.Path(sys.argv[1])
binaries = {}
for name in ("cjpeg", "jpegtran"):
    data = (root / name).read_bytes()
    binaries[name] = {
        "sha256": hashlib.sha256(data).hexdigest(),
        "size": len(data),
    }
manifest = {
    "version": "4.1.5",
    "source_url": "https://github.com/mozilla/mozjpeg/archive/refs/tags/v4.1.5.tar.gz",
    "source_sha256": sys.argv[2],
    "binaries": binaries,
    "cmake_flags": sys.argv[3:],
    "cflags": "-O2 -arch arm64 -mmacosx-version-min=11.0",
    "ldflags": "-arch arm64 -mmacosx-version-min=11.0",
    "compiler": subprocess.check_output(["clang", "--version"], text=True).splitlines()[0],
    "sdk": subprocess.check_output(["xcrun", "--show-sdk-version"], text=True).strip(),
}
(root / "manifest.json").write_text(json.dumps(manifest, indent=2) + "\n")
PY

for name in cjpeg jpegtran; do
  otool -L "$asset_dir/$name"
done
echo "Prepared native asset: $asset_dir"
