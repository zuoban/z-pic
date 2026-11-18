# PNG tool preparation

Run `bash scripts/prepare-pngtools.sh` from any directory using a native macOS
or Linux arm64/amd64 host. Requires Rust/Cargo (Oxipng requires Rust >= 1.88),
a C compiler and platform SDK, Python >= 3.11, tar, and standard Unix build
utilities. The checked-in assets were built with Rust 1.96.0. On macOS the script
uses deployment target 11.0, verifies only Apple system dylibs are linked,
and ad hoc signs and verifies each **tool**; it does not build or sign the app.

The default build is offline. It extracts `licenses/pngtools-source.tar.gz`,
which includes exact upstream sources, frozen Cargo.lock files, all vendored
Cargo dependencies, license files, and relative Cargo source configuration.
For an already extracted copy, set `PNGTOOLS_SOURCE_DIR` to the extracted
`pngtools-source` directory. Cargo uses a fresh temporary target directory,
removed on exit. Toolchains and platform SDKs must already be installed.
Source pinning makes preparation repeatable; different compilers/SDKs/paths
may produce different binary hashes. This is not a bit-for-bit reproducible
build claim.

Run `bash scripts/prepare-pngtools.sh --verify` to verify the shipped source
archive digest, binary digests against the manifest, pinned tool versions and
macOS signatures without building or requiring Rust/Cargo. CI runs this check.
The source digest pins the repository's vendored snapshot; it does not
independently attest to the origin of every vendored dependency.

The pins are:

| Tool | Official tag | Commit |
| --- | --- | --- |
| Oxipng | `v10.2.1` | `36f3ef8aac65ecf1761739ea2f2e530281f1312b` |
| pngquant | `3.0.3` | `53a332a58f44357b6b41842a54d74aa1e245913d` |
| pngquant libimagequant submodule | pinned by pngquant | `6e9805761851f1a8320380b9f563961f892ec6ba` |

Official source repositories are https://github.com/oxipng/oxipng and
https://github.com/kornelski/pngquant (libimagequant:
https://github.com/ImageOptim/libimagequant). No upstream code has been patched.
Oxipng builds its default features; pngquant adds `static,z-static`, statically
linking libpng, zlib, and Little CMS. Upstream pngquant has no committed lockfile;
the generated lock included in the source bundle freezes the dependencies used
for the shipped binary. Full transitive license texts are under `licenses/`.

The script installs into `internal/pngtools/assets/<goos>_<goarch>/` and records
versions, source commits, compiler, and binary SHA-256 in `manifest.json`.
`embed.FS` automatically includes added native assets when the Go app is rebuilt.
Only **darwin/arm64** assets ship initially. Other targets compile and fail with
an actionable Chinese missing-bundle error; they do not use PATH or fall back
to a Go encoder. Linux builds use the host's system C runtime and require native
QA before distribution. Windows preparation is not automated by this shell
script: use the same frozen sources and static features with a native Windows
toolchain, place `oxipng.exe` and `pngquant.exe` under `assets/windows_<goarch>/`,
and validate dependencies and execution before distribution.

Runtime commands (stdin/stdout contain raw PNG bytes):

- Oxipng: `-o 2 --threads 2 --stdout -`. Adds `--nx` only for inputs containing
  `sBIT`, `bKGD`, or `hIST` so their payloads remain valid and retained.
  Other inputs permit normal lossless representation/palette/interlace changes.
  Never passes `--alpha`, `--scale16`, or a stripping option. Metadata can move
  to a valid chunk position; ICC data may be losslessly recompressed.
  `caBX`/`iDOT` inputs return unchanged because changing file bytes invalidates
  their signatures/offsets (confirmed in upstream `src/png/mod.rs`).
- pngquant default: `--quality=65-85 --speed=3 --skip-if-larger --output - 256 -`.
  The selected image-quality level supplies `--quality=<min>-<max>`,
  `--speed=<speed>`, and the positional color cap. Valid ranges: minimum
  0–100, target 1–100 with minimum <= target, colors 2–256, speed 1–11.
  The all-zero options value uses the defaults; partially specified invalid
  configurations fail before execution. The UI adjusts all PNG parameters with
  image quality; presets and legacy custom values are documented in README.md.
  There is no separate lossless mode.
  Like Optimize, quantization returns `caBX`/`iDOT` files unchanged.
  Exit 98/99 discards stdout and returns the exact original input without error.
  Caller then runs Optimize and validates dimensions/output PNG.

Each invocation stages and reuses the selected binary inside a content-addressed
0700 cache directory under the user cache root (verifying permissions, size, and
SHA-256 integrity on every acquisition, falling back to an ephemeral 0700 directory removed after Wait
when the user cache directory is unavailable). No shell or inherited PATH is
used. Runtime is capped at two minutes per tool, stdout at 64 MiB + 1 sentinel
byte (overflow cancels the process), and stored stderr at 32 KiB (excess drained).
`CommandContext` propagates cancellation/deadline errors via wrapping;
`WaitDelay=1s` bounds pipe cleanup. Successful outputs without savings return
the original input. The caller validates PNG structure, dimensions, decoded
pixels as appropriate, and the 32 MP input limit.

Verify with `go test ./internal/pngtools -count=1`, `go test -race
./internal/pngtools`, and `go vet ./internal/pngtools`. Native integration tests
run with an empty PATH, exercise hidden RGB/16-bit samples, metadata, real
quantization and optimization, private extraction and cleanup. Helper-process
tests cover 98/99 partial output, tool errors, missing executables, cancellation,
deadlines, empty output, and bounded pipes.
