# AVIF tool preparation

`bash scripts/prepare-aviftools.sh` builds only the embedded libavif 1.4.2 tools
(`avifenc` and `avifdec`) backed by libaom 3.14.1 on a native darwin/arm64 host.
It never builds or signs the application. Requires CMake, Apple Command Line
Tools (clang, clang++, SDK, make, strip, otool, codesign, xcrun), Perl, Python 3,
tar, and shasum. No Homebrew runtime libraries, Go dependencies, or network
access are used during preparation.

The checked-in official source archives in `scripts/sources/` are:

- `libavif-1.4.2.tar.gz` (`v1.4.2`)
  - Official release: https://github.com/AOMediaCodec/libavif/archive/refs/tags/v1.4.2.tar.gz
  - Source SHA-256: `2b645287340ba5a631d268b551dc2d72bd73ac33335962dd36dcdb6d8366921d`
- `libaom-3.14.1.tar.gz` (`v3.14.1`)
  - Official release: https://aomedia.googlesource.com/aom/+archive/refs/tags/v3.14.1.tar.gz
  - Source SHA-256: `44bf90dbd23e734d50e70a8c41c285193922938bd0d3bc2ee56764d181d55ef5`
- `libpng-1.6.58.tar.gz` (`v1.6.58`)
  - Official release: https://github.com/pnggroup/libpng/archive/refs/tags/v1.6.58.tar.gz
  - Source SHA-256: `a9d4df463d36a6e5f9c29bd6f4967312d17e996c1854f3511f833924eb1993cf`
- `mozjpeg-4.1.5.tar.gz` (`v4.1.5`, shared with `scripts/prepare-jpegtools.sh`)
  - Official release: https://github.com/mozilla/mozjpeg/archive/refs/tags/v4.1.5.tar.gz
  - Source SHA-256: `9fcbb7171f6ac383f5b391175d6fb3acde5e64c4c4727274eade84ed0998fcc1`

Unmodified upstream notices are stored in `licenses/libavif-LICENSE`,
`licenses/libaom-LICENSE`, `licenses/libaom-AUTHORS`, `licenses/libaom-PATENTS`,
`licenses/libpng-LICENSE`, and `licenses/mozjpeg-*`.

The script verifies all four archive hashes before extraction, creates a fresh
temporary build directory removed on exit, ignores `/opt/homebrew` and
`/usr/local`, and disables `pkg-config`. To keep each binary small and strictly
scoped:

1. Builds static `libpng` against macOS SDK `libz` (`-DPNG_SHARED=OFF -DPNG_STATIC=ON`).
2. Builds static `libjpeg` from MozJPEG (`-DENABLE_SHARED=OFF -DENABLE_STATIC=ON -DWITH_SIMD=ON`).
3. Builds an encoder-only static `libaom` (`-DCONFIG_AV1_ENCODER=1 -DCONFIG_AV1_DECODER=0`)
   and links `avifenc` against it (`-DAVIF_CODEC_AOM=SYSTEM -DAVIF_CODEC_AOM_ENCODE=ON -DAVIF_CODEC_AOM_DECODE=OFF`).
4. Builds a decoder-only static `libaom` (`-DCONFIG_AV1_ENCODER=0 -DCONFIG_AV1_DECODER=1`)
   and links `avifdec` against it (`-DAVIF_CODEC_AOM=SYSTEM -DAVIF_CODEC_AOM_ENCODE=OFF -DAVIF_CODEC_AOM_DECODE=ON`).

All binaries target `arm64` with `-mmacosx-version-min=11.0`, are stripped with
`strip -x`, and ad-hoc signed with `codesign --force --sign -`. Only system
dynamic libraries (`/usr/lib/` and `/System/Library/`) are permitted.

The resulting assets are `internal/aviftools/assets/darwin_arm64/avifenc` and
`internal/aviftools/assets/darwin_arm64/avifdec`, with hashes and build metadata
recorded in `internal/aviftools/assets/darwin_arm64/manifest.json`. Run
`bash scripts/prepare-aviftools.sh --verify` to verify archive hashes, asset
checksums, signatures, and reported versions without rebuilding.

Only darwin/arm64 ships native AVIF binaries. Other Go targets compile cleanly
with `aviftools.Available() == false`, and AVIF encoding/decoding returns
`aviftools.ErrUnsupportedPlatform`.

Runtime invocations:

```text
avifenc --stdin --input-format png -s <0..10> -j <jobs> [-q <1..100> --qalpha 100 | --lossless] [--icc <file>] [--ignore-exif] [--ignore-xmp] [--ignore-icc] <out.avif>
avifdec --depth 8 --png-compress 1 --size-limit 32000000 -j <jobs> <in.avif> <out.png>
```

- `avifenc` reads PNG input from stdin, writes AVIF to a private `0700`
  temporary output file, and applies metadata flags matching the active policy.
- `avifdec` reads a validated static AVIF file from a private `0700` temporary
  directory, enforces the 32,000,000 pixel limit, applies container `clap`/`irot`/`imir`
  transforms, and writes an 8-bit PNG carrying preserved `iCCP`/`eXIf`/XMP metadata.
- Both tools use `internal/toolcache` for content-addressed private `0700`
  staging with SHA-256 and permission verification on every acquisition,
  `LC_ALL=C` only, a 2-minute timeout, 64 MiB input/output caps, and 32 KiB
  bounded/drained stderr.

Validation:

```sh
go test ./internal/aviftools -count=1
go test -race ./internal/aviftools -count=1
go vet ./internal/aviftools
bash scripts/prepare-aviftools.sh --verify
```
