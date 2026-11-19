# MozJPEG tool preparation

`bash scripts/prepare-jpegtools.sh` builds only the embedded MozJPEG 4.1.5 tools
(`cjpeg` and `jpegtran`) on a native darwin/arm64 host. It never builds or signs
the application. Requires CMake, Apple Command Line Tools (clang, SDK, make,
strip, otool, codesign, xcrun), Python 3, tar, and shasum. No Homebrew runtime
libraries, Go dependencies, or network access are used during preparation.

The checked-in official source is `scripts/sources/mozjpeg-4.1.5.tar.gz`:

- Official release: https://github.com/mozilla/mozjpeg/archive/refs/tags/v4.1.5.tar.gz
- Release tag: `v4.1.5`.
- Source SHA-256: `9fcbb7171f6ac383f5b391175d6fb3acde5e64c4c4727274eade84ed0998fcc1`.
- Unmodified upstream notices are copied to `licenses/mozjpeg-LICENSE.md`,
  `licenses/mozjpeg-README.ijg`, and `licenses/mozjpeg-README-mozilla.txt`.

The script verifies the archive hash before extraction, creates a fresh
temporary source/build directory, and removes it on exit. It configures CMake
with:

```text
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
```

It builds targets `cjpeg-static` and `jpegtran-static` with
`CFLAGS="-O2 -arch arm64 -mmacosx-version-min=11.0"`,
`LDFLAGS="-arch arm64 -mmacosx-version-min=11.0"`, the clang and SDK selected by
`xcrun`, then runs `strip -x` and `codesign --force --sign -`. Only system
dynamic libraries (`/usr/lib/` and `/System/Library/`) are permitted.

The resulting assets are `internal/jpegtools/assets/darwin_arm64/cjpeg` and
`internal/jpegtools/assets/darwin_arm64/jpegtran`. Run
`bash scripts/prepare-jpegtools.sh --verify` to verify the checked-in archive,
asset checksums, signatures, and versions without rebuilding.

Only darwin/arm64 ships native MozJPEG binaries. Other Go targets compile cleanly
with `jpegtools.Available() == false`, and `internal/compress` falls back to Go's
standard-library `image/jpeg` for lossy JPEG encoding (and retains original bytes
for lossless JPEG).

Runtime invocations:

```text
cjpeg -quality <1..100> -optimize -progressive [-grayscale]
jpegtran -optimize -progressive -copy <all|icc|none> [-perfect <transform>]
```

- `cjpeg` reads streamed P5 (PGM grayscale) or P6 (PPM RGB) pixel data from stdin
  and writes progressive, trellis-quantized JPEG to stdout.
- `jpegtran` reads raw JPEG from stdin and writes losslessly optimized
  progressive JPEG to stdout. When stripping EXIF on a rotated JPEG in lossless
  mode, `-perfect` with the matching rotation/flip is used so non-MCU-aligned
  images fail cleanly rather than cropping edge pixels.
- Both tools use `internal/toolcache` for content-addressed private 0700 staging
  with SHA-256 and permission verification on every acquisition, `LC_ALL=C` only,
  a 2-minute timeout, 64 MiB stdout cap (with optional early termination when
  output reaches `MaxOutputBytes`), and 32 KiB bounded/drained stderr.

Validation:

```sh
go test ./internal/jpegtools -count=1
go test -race ./internal/jpegtools -count=1
go vet ./internal/jpegtools
bash scripts/prepare-jpegtools.sh --verify
```
