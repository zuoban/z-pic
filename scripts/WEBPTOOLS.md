# WebP tool preparation

`bash scripts/prepare-webptools.sh` builds only the embedded cwebp 1.6.0 tool
on a native darwin/arm64 host. It never builds or signs the application.
Requires the Apple Command Line Tools (clang, SDK, make, strip, otool, codesign,
xcrun), Python 3, tar, and shasum. No Homebrew libraries, Go dependencies,
network access, downloads, or package manager are used during preparation.

The checked-in official source is `licenses/libwebp-1.6.0.tar.gz`:

- Official release: https://storage.googleapis.com/downloads.webmproject.org/releases/webp/libwebp-1.6.0.tar.gz
- Release tag: `v1.6.0`, commit `4fa21912338357f89e4fd51cf2368325b59e9bd9`.
- Source SHA-256: `e4ab7009bf0629fd11982d4c2aa83964cf244cffba7347ecd39019a9e38c4564`.
- Unmodified upstream notices are copied to `licenses/libwebp-COPYING`,
  `licenses/libwebp-PATENTS`, and `licenses/libwebp-AUTHORS`.

The script verifies the archive hash before extraction, creates a fresh
temporary source/build directory, and removes it on exit. It configures with:

```text
--disable-shared --enable-static
--disable-png --disable-jpeg --disable-tiff --disable-gif
--disable-sdl --disable-gl --disable-wic
--enable-libwebpdemux --disable-libwebpmux --enable-threading
```

It uses `CFLAGS="-O2 -arch arm64 -mmacosx-version-min=11.0"`,
`LDFLAGS="-arch arm64 -mmacosx-version-min=11.0"`, the clang and SDK selected by
`xcrun`, then `make -j2`, `strip -x`, and `codesign --force --sign -`.
Static libwebp, libsharpyuv, and libwebpdemux are linked into cwebp. Demux support
is required by upstream `imageio/webpdec.c` for WebP input and metadata
extraction; mux is unnecessary for cwebp's output writer. PNG, JPEG, TIFF, GIF,
SDL, OpenGL, and WIC support are disabled.

The resulting asset is `internal/webptools/assets/darwin_arm64/cwebp`.
Preparation checks its version with an empty environment, verifies its ad hoc
signature, and rejects every `otool -L` dependency outside `/usr/lib/` or
`/System/Library/`. The manifest records the asset SHA-256, source hash, flags,
compiler, and SDK. Run `bash scripts/prepare-webptools.sh --verify` to verify the
checked-in archive, asset checksum, signature, and version without rebuilding.
Preparation is repeatable from the pinned offline source; binary hashes may
differ across compiler/SDK versions and build paths. This is not a claim of
bit-for-bit reproducibility.

Only darwin/arm64 ships. Other Go targets compile and return an actionable
missing-bundle error. No system executable lookup, runtime executable override,
or fallback encoder is provided.

Runtime invocation, confirmed against `/opt/homebrew/bin/cwebp` 1.6.0 and the
bundled tool:

```text
cwebp -q <0..100> -m 4 -alpha_q 100 -metadata all -quiet -o - -- -
```

The original WebP is streamed directly to stdin, and stdout contains WebP.
`-- -` must be last. Alpha is encoded losslessly; RGB is lossy at the selected
quality. `-metadata all` copies EXIF (including orientation), ICC, and XMP
payloads without stripping them or applying orientation. The Go wrapper checks
the RIFF/WEBP signature, the 64 MiB input cap, and quality bounds; the caller
validates complete static input/output, dimensions, pixel limits, and native
encoding dimension limits (16383 per axis). Animated input is unsupported.
The wrapper returns the encoder output even when it is larger; the caller
decides whether the original should be retained.

Each call stages and reuses the embedded executable inside a content-addressed
private 0700 directory under the user cache root (verifying permissions, size,
and SHA-256 integrity on every acquisition, falling back to an ephemeral 0700 directory removed after
process completion when the user cache directory is unavailable).
The absolute executable path is used with no shell, PATH lookup, inherited
environment, or dynamic-library override. The child receives only `LC_ALL=C`.
The context budget includes extraction and is capped at two minutes.
Stored stdout is capped at 64 MiB; any excess cancels the process and discards
partial output. Stored stderr is capped at 32 KiB and excess is drained.
`WaitDelay=1s` bounds pipe cleanup. Cancellation/deadline errors wrap the
corresponding context error. Process errors and empty stdout are failures.

Validation:

```sh
go test ./internal/webptools -count=1
go test -race ./internal/webptools -count=1
go vet ./internal/webptools
bash scripts/prepare-webptools.sh --verify
```

Tests use the Go test executable as a child for cancellation, deadline, process
errors, missing executables, exact output limits, overflow termination, empty
output, and bounded/drained stderr. Integration tests use only the embedded
asset, with PATH cleared, and check version, checksum, private extraction and
cleanup, actual WebP recompression, 19×13 dimensions, every alpha sample, and
exact EXIF/ICC/XMP preservation including EXIF orientation 6.

`internal/webptools/testdata/alpha-metadata.webp` is a generated fixture, not
third-party artwork. Its 19×13 RGBA pixels are:
`R=(31*x+7*y)%256`, `G=(9*x+29*y)%256`, `B=(19*x+11*y)%256`,
`A=(17*x+23*y)%256`. It was encoded once using installed cwebp 1.6.0 with
`-lossless -exact -quiet -o - -- -`, then given an extended WebP header and
synthetic ICC, EXIF orientation 6, and XMP payloads. The ICC payload is an
opaque marker for byte-preservation testing, not a colorimetrically valid
profile. No test requires Homebrew or a system WebP tool.
