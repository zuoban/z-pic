#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source_sha=e4ab7009bf0629fd11982d4c2aa83964cf244cffba7347ecd39019a9e38c4564
archive="$repo_root/scripts/sources/libwebp-1.6.0.tar.gz"
asset_dir="$repo_root/internal/webptools/assets/darwin_arm64"

[[ "$(uname -s)/$(uname -m)" == Darwin/arm64 ]] || {
  echo "Use a native darwin/arm64 host." >&2
  exit 1
}
for tool in clang make tar shasum python3 strip otool codesign xcrun; do
  command -v "$tool" >/dev/null || { echo "Missing build tool: $tool" >&2; exit 1; }
done
printf '%s  %s\n' "$source_sha" "$archive" | shasum -a 256 -c -
if [[ "${1:-}" == --verify ]]; then
  python3 - "$asset_dir" "$source_sha" <<'PY'
import hashlib, json, pathlib, sys
root = pathlib.Path(sys.argv[1])
manifest = json.loads((root / "manifest.json").read_text())
assert manifest["version"] == "1.6.0"
assert manifest["source_sha256"] == sys.argv[2]
assert manifest["sha256"] == hashlib.sha256((root / "cwebp").read_bytes()).hexdigest()
PY
  codesign --verify --strict "$asset_dir/cwebp"
  env -i "$asset_dir/cwebp" -version
  exit 0
fi
[[ $# == 0 ]] || { echo "Usage: $0 [--verify]" >&2; exit 1; }

work_dir="$(mktemp -d "${TMPDIR:-/tmp}/zpic-webptools-build.XXXXXX")"
trap 'rm -rf "$work_dir"' EXIT
tar -xzf "$archive" -C "$work_dir"
source_dir="$work_dir/libwebp-1.6.0"
build_dir="$work_dir/build"
mkdir -p "$build_dir"
configure_flags=(
  --disable-shared --enable-static
  --disable-png --disable-jpeg --disable-tiff --disable-gif
  --disable-sdl --disable-gl --disable-wic
  --enable-libwebpdemux --disable-libwebpmux --enable-threading
)
(
  cd "$build_dir"
  export SDKROOT="$(xcrun --show-sdk-path)"
  env -i PATH=/usr/bin:/bin:/usr/sbin:/sbin LC_ALL=C \
    SDKROOT="$SDKROOT" \
    CC="$(xcrun --find clang)" CFLAGS="-O2 -arch arm64 -mmacosx-version-min=11.0" \
    LDFLAGS="-arch arm64 -mmacosx-version-min=11.0" \
    "$source_dir/configure" "${configure_flags[@]}"
  make -j2
)
binary="$build_dir/examples/cwebp"
strip -x "$binary"
codesign --force --sign - "$binary"
codesign --verify --strict "$binary"
python3 - "$binary" <<'PY'
import subprocess, sys
binary = sys.argv[1]
for line in subprocess.check_output(["otool", "-L", binary], text=True).splitlines()[1:]:
    dependency = line.strip().split(" (")[0]
    if not dependency.startswith(("/usr/lib/", "/System/Library/")):
        raise SystemExit(f"Non-system dynamic dependency: {dependency}")
version = subprocess.check_output([binary, "-version"], text=True, env={})
if version.splitlines()[0] != "1.6.0":
    raise SystemExit(f"Wrong cwebp version: {version}")
PY
mkdir -p "$asset_dir"
install -m 755 "$binary" "$asset_dir/cwebp"
for name in COPYING PATENTS AUTHORS; do
  install -m 644 "$source_dir/$name" "$repo_root/licenses/libwebp-$name"
done
python3 - "$asset_dir" "$source_sha" "${configure_flags[@]}" <<'PY'
import hashlib, json, pathlib, subprocess, sys
root = pathlib.Path(sys.argv[1])
manifest = {
    "version": "1.6.0",
    "source_url": "https://storage.googleapis.com/downloads.webmproject.org/releases/webp/libwebp-1.6.0.tar.gz",
    "source_sha256": sys.argv[2],
    "sha256": hashlib.sha256((root / "cwebp").read_bytes()).hexdigest(),
    "configure_flags": sys.argv[3:],
    "cflags": "-O2 -arch arm64 -mmacosx-version-min=11.0",
    "ldflags": "-arch arm64 -mmacosx-version-min=11.0",
    "compiler": subprocess.check_output(["clang", "--version"], text=True).splitlines()[0],
    "sdk": subprocess.check_output(["xcrun", "--show-sdk-version"], text=True).strip(),
}
(root / "manifest.json").write_text(json.dumps(manifest, indent=2) + "\n")
PY
otool -L "$asset_dir/cwebp"
echo "Prepared native asset: $asset_dir"
