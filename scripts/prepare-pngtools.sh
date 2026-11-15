#!/usr/bin/env bash
# Build only the embedded PNG CLI assets, never the application.
# Source and every Cargo dependency are shipped in licenses/pngtools-source.tar.gz.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
work_dir="$(mktemp -d "${TMPDIR:-/tmp}/zpic-pngtools-build.XXXXXX")"
trap 'rm -rf "$work_dir"' EXIT

case "$(uname -s)" in
  Darwin) target_os=darwin ;;
  Linux) target_os=linux ;;
  *) echo "Unsupported build host: use a native macOS or Linux host." >&2; exit 1 ;;
esac
case "$(uname -m)" in
  arm64|aarch64) target_arch=arm64 ;;
  x86_64|amd64) target_arch=amd64 ;;
  *) echo "Unsupported architecture." >&2; exit 1 ;;
esac
for tool in cargo rustc python3 tar; do
  command -v "$tool" >/dev/null || { echo "Missing build tool: $tool" >&2; exit 1; }
done
if [[ "$target_os" == darwin ]]; then
  for tool in clang otool codesign; do
    command -v "$tool" >/dev/null || { echo "Missing Apple build tool: $tool" >&2; exit 1; }
  done
  export MACOSX_DEPLOYMENT_TARGET=11.0
fi

if [[ -n "${PNGTOOLS_SOURCE_DIR:-}" ]]; then
  source_dir="$(cd "$PNGTOOLS_SOURCE_DIR" && pwd)"
else
  tar -xzf "$repo_root/licenses/pngtools-source.tar.gz" -C "$work_dir"
  source_dir="$work_dir/pngtools-source"
fi

# Check the upstream package versions before using the shipped frozen locks.
python3 - "$source_dir" <<'PY'
import pathlib, sys, tomllib
root = pathlib.Path(sys.argv[1])
for name, version in (("oxipng", "10.2.1"), ("pngquant", "3.0.3")):
    manifest = tomllib.loads((root / name / "Cargo.toml").read_text())
    if manifest["package"]["version"] != version:
        raise SystemExit(f"Wrong {name} source version; expected {version}")
PY

# Run from each project so Cargo discovers its relative offline vendor config.
# No cargo install, pkg-config libraries, or runtime Homebrew dependency is used.
(
  cd "$source_dir/oxipng"
  CARGO_TARGET_DIR="$work_dir/oxipng-target" cargo build --release --frozen --offline
)
(
  cd "$source_dir/pngquant"
  CARGO_TARGET_DIR="$work_dir/pngquant-target" cargo build --release --frozen --offline --features static,z-static
)

for name in oxipng pngquant; do
  binary="$work_dir/$name-target/release/$name"
  if [[ "$target_os" == darwin ]]; then
    # Sign only the tool asset. The application is packaged/signed separately.
    codesign --force --sign - "$binary"
    codesign --verify --strict "$binary"
    otool -L "$binary"
    python3 - "$binary" <<'PY'
import subprocess, sys
lines = subprocess.check_output(["otool", "-L", sys.argv[1]], text=True).splitlines()[1:]
for line in lines:
    dep = line.strip().split(" (")[0]
    if not dep.startswith(("/usr/lib/", "/System/Library/")):
        raise SystemExit(f"Non-system dynamic dependency: {dep}")
PY
  fi
  env -i PATH= "$binary" --version
done

asset_dir="$repo_root/internal/pngtools/assets/${target_os}_${target_arch}"
mkdir -p "$asset_dir"
for name in oxipng pngquant; do
  install -m 755 "$work_dir/$name-target/release/$name" "$asset_dir/$name"
done
python3 - "$asset_dir" <<'PY'
import hashlib, json, pathlib, subprocess, sys
root = pathlib.Path(sys.argv[1])
manifest = {
    "oxipng": {"version": "10.2.1", "commit": "36f3ef8aac65ecf1761739ea2f2e530281f1312b"},
    "pngquant": {"version": "3.0.3", "commit": "53a332a58f44357b6b41842a54d74aa1e245913d",
                 "libimagequant_commit": "6e9805761851f1a8320380b9f563961f892ec6ba"},
    "rustc": subprocess.check_output(["rustc", "--version"], text=True).strip(),
}
for name in ("oxipng", "pngquant"):
    manifest[name]["sha256"] = hashlib.sha256((root / name).read_bytes()).hexdigest()
(root / "manifest.json").write_text(json.dumps(manifest, indent=2) + "\n")
PY
echo "Prepared native assets: $asset_dir"
echo "Rebuild the Go application to embed them. No cross-compilation is performed."
