# Third-party image tools

Z-Pic includes separate native command-line executables and runs them through
`os/exec`. The darwin/arm64 tools are embedded in the Go application and extracted
temporarily at runtime. No commercial pngquant license is included.

| Component | Version | License | Full text |
| --- | --- | --- | --- |
| Oxipng | 10.2.1 | MIT | `licenses/oxipng-MIT.txt` |
| pngquant | 3.0.3 | GPL-3.0-or-later; includes original permissive notices | `licenses/pngquant-COPYRIGHT.txt` |
| libimagequant | pngquant's pinned submodule (`6e980576…`) | GPL-3.0-or-later | `licenses/libimagequant-COPYRIGHT.txt` |
| libwebp / cwebp | 1.6.0 | BSD-3-Clause and upstream patent grant | `licenses/libwebp-COPYING`, `licenses/libwebp-PATENTS`, `licenses/libwebp-AUTHORS` |
| MozJPEG (cjpeg / jpegtran) | 4.1.5 | IJG / Modified 3-Clause BSD / zlib | `licenses/mozjpeg-LICENSE.md`, `licenses/mozjpeg-README.ijg`, `licenses/mozjpeg-README-mozilla.txt` |
| libavif (avifenc / avifdec) | 1.4.2 | BSD-2-Clause | `licenses/libavif-LICENSE` |
| libaom | 3.14.1 | BSD-2-Clause and upstream patent grant | `licenses/libaom-LICENSE`, `licenses/libaom-PATENTS`, `licenses/libaom-AUTHORS` |
| libpng (statically linked by AVIF tools) | 1.6.58 | PNG Reference Library License v2 | `licenses/libpng-LICENSE` |
| Rust runtime / standard library | Rust 1.96.0 build | MIT / Apache-2.0 and bundled notices | `licenses/rust-*` |
| Transitive Rust and native dependencies | frozen Cargo.lock versions | See dependency licenses | `licenses/pngtools-dependency-licenses.txt` and repository source archive |

Oxipng official source: https://github.com/oxipng/oxipng, tag `v10.2.1`,
commit `36f3ef8aac65ecf1761739ea2f2e530281f1312b`.

pngquant official source: https://github.com/kornelski/pngquant, tag `3.0.3`,
commit `53a332a58f44357b6b41842a54d74aa1e245913d`. libimagequant source:
https://github.com/ImageOptim/libimagequant, commit
`6e9805761851f1a8320380b9f563961f892ec6ba`. The upstream pngquant project also
offers commercial licensing separately; this distribution uses its GPL terms.

The tools are provided WITHOUT WARRANTY; see their complete license terms.
The complete frozen tool sources, vendored dependencies, and offline build
instructions accompany the source repository and release distribution in
`scripts/sources/pngtools-source.tar.gz` (kept outside the `.app` runtime
bundle to avoid shipping ~39 MB of source archives inside the executable app).
They permit inspection, modification, and rebuilding of the shipped executables.
No upstream source patches were applied.
`scripts/prepare-pngtools.sh` rebuilds the assets using those sources;
`scripts/PNGTOOLS.md` documents preparation, runtime flags, and platform limits.
The archive also includes a copy of those build instructions and script.

Retain this notice, the `licenses/` directory, and equivalent access to
`scripts/sources/pngtools-source.tar.gz` when distributing the bundled tools.
Binary hashes and exact build/compiler pins are recorded alongside each
target's assets in `internal/pngtools/assets/<goos>_<goarch>/manifest.json`.

## WebP encoder

The separately embedded cwebp executable is built from libwebp 1.6.0, upstream
commit `4fa21912338357f89e4fd51cf2368325b59e9bd9`. Its unmodified official source
archive is tracked in the repository at `scripts/sources/libwebp-1.6.0.tar.gz`:
https://storage.googleapis.com/downloads.webmproject.org/releases/webp/libwebp-1.6.0.tar.gz

Source SHA-256: `e4ab7009bf0629fd11982d4c2aa83964cf244cffba7347ecd39019a9e38c4564`.
The value is a locally computed digest of that download, not an upstream signed
checksum. Full license, patent and author notices are included alongside it.

The darwin/arm64 encoder statically links libwebp and demux libraries, with
optional PNG/JPEG/TIFF/GIF/UI libraries disabled. Only system dynamic libraries
are required. `scripts/prepare-webptools.sh` rebuilds it offline and
`scripts/WEBPTOOLS.md` records flags and verification. Asset hashes, compiler,
SDK and build flags are recorded in
`internal/webptools/assets/darwin_arm64/manifest.json`.

## MozJPEG tools

The separately embedded `cjpeg` and `jpegtran` executables are built from
MozJPEG 4.1.5, upstream tag `v4.1.5`:
https://github.com/mozilla/mozjpeg/archive/refs/tags/v4.1.5.tar.gz

Its unmodified official source archive is tracked in the repository at
`scripts/sources/mozjpeg-4.1.5.tar.gz`.
Source SHA-256: `9fcbb7171f6ac383f5b391175d6fb3acde5e64c4c4727274eade84ed0998fcc1`.
The value is a locally computed digest of that download, not an upstream signed
checksum. Full upstream license and attribution notices are included in
`licenses/mozjpeg-LICENSE.md`, `licenses/mozjpeg-README.ijg`, and
`licenses/mozjpeg-README-mozilla.txt`.

The darwin/arm64 executables statically link libjpeg-turbo/MozJPEG with NEON
SIMD enabled and optional PNG/TurboJPEG/Java support disabled. Only system
dynamic libraries are required. `scripts/prepare-jpegtools.sh` rebuilds them
offline and `scripts/JPEGTOOLS.md` records flags and verification. Asset hashes,
compiler, SDK and CMake flags are recorded in
`internal/jpegtools/assets/darwin_arm64/manifest.json`.

## AVIF tools

The separately embedded `avifenc` and `avifdec` executables are built from
libavif 1.4.2 (tag `v1.4.2`) statically linked with libaom 3.14.1 (tag
`v3.14.1`), libpng 1.6.58 (tag `v1.6.58`), and MozJPEG 4.1.5 (tag `v4.1.5`).
Their unmodified official source archives are tracked in `scripts/sources/`:

- `libavif-1.4.2.tar.gz`: https://github.com/AOMediaCodec/libavif/archive/refs/tags/v1.4.2.tar.gz
  - Source SHA-256: `2b645287340ba5a631d268b551dc2d72bd73ac33335962dd36dcdb6d8366921d`
- `libaom-3.14.1.tar.gz`: https://aomedia.googlesource.com/aom/+archive/refs/tags/v3.14.1.tar.gz
  - Source SHA-256: `44bf90dbd23e734d50e70a8c41c285193922938bd0d3bc2ee56764d181d55ef5`
- `libpng-1.6.58.tar.gz`: https://github.com/pnggroup/libpng/archive/refs/tags/v1.6.58.tar.gz
  - Source SHA-256: `a9d4df463d36a6e5f9c29bd6f4967312d17e996c1854f3511f833924eb1993cf`
- `mozjpeg-4.1.5.tar.gz`: https://github.com/mozilla/mozjpeg/archive/refs/tags/v4.1.5.tar.gz
  - Source SHA-256: `9fcbb7171f6ac383f5b391175d6fb3acde5e64c4c4727274eade84ed0998fcc1`

The SHA-256 values are locally computed digests of those downloads, not upstream
signed checksums. Full upstream license, patent, and author notices are included
in `licenses/libavif-LICENSE`, `licenses/libaom-LICENSE`,
`licenses/libaom-PATENTS`, `licenses/libaom-AUTHORS`, `licenses/libpng-LICENSE`,
and `licenses/mozjpeg-*`.

The darwin/arm64 `avifenc` links an encoder-only static libaom and `avifdec`
links a decoder-only static libaom, with only system dynamic libraries required.
`scripts/prepare-aviftools.sh` rebuilds them offline and `scripts/AVIFTOOLS.md`
records flags and verification. Asset hashes, compiler, SDK, and CMake flags are
recorded in `internal/aviftools/assets/darwin_arm64/manifest.json`.
