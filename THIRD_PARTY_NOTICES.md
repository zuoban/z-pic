# Third-party PNG tools

Z-Pic includes separate native command-line executables and runs them through
`os/exec`. The darwin/arm64 tools are embedded in the Go application and extracted
temporarily at runtime. No commercial pngquant license is included.

| Component | Version | License | Full text |
| --- | --- | --- | --- |
| Oxipng | 10.2.1 | MIT | `licenses/oxipng-MIT.txt` |
| pngquant | 3.0.3 | GPL-3.0-or-later; includes original permissive notices | `licenses/pngquant-COPYRIGHT.txt` |
| libimagequant | pngquant's pinned submodule (`6e980576…`) | GPL-3.0-or-later | `licenses/libimagequant-COPYRIGHT.txt` |
| Rust runtime / standard library | Rust 1.96.0 build | MIT / Apache-2.0 and bundled notices | `licenses/rust-*` |
| Transitive Rust and native dependencies | frozen Cargo.lock versions | See dependency licenses | `licenses/pngtools-dependency-licenses.txt` and source archive |

Oxipng official source: https://github.com/oxipng/oxipng, tag `v10.2.1`,
commit `36f3ef8aac65ecf1761739ea2f2e530281f1312b`.

pngquant official source: https://github.com/kornelski/pngquant, tag `3.0.3`,
commit `53a332a58f44357b6b41842a54d74aa1e245913d`. libimagequant source:
https://github.com/ImageOptim/libimagequant, commit
`6e9805761851f1a8320380b9f563961f892ec6ba`. The upstream pngquant project also
offers commercial licensing separately; this distribution uses its GPL terms.

The tools are provided WITHOUT WARRANTY; see their complete license terms.
The complete frozen tool sources, vendored dependencies, and offline build
instructions accompany this distribution in `licenses/pngtools-source.tar.gz`.
They are included to permit inspection, modification, and rebuilding of the
shipped executables. No upstream source patches were applied.
`scripts/prepare-pngtools.sh` rebuilds the assets using those sources;
`scripts/PNGTOOLS.md` documents preparation, runtime flags, and platform limits.
The archive also includes a copy of those build instructions and script.

Retain this notice, the full `licenses/` directory (including the corresponding
source archive), and the relevant license terms when distributing the bundled
tools. Binary hashes and exact build/compiler pins are recorded alongside
each target's assets in `internal/pngtools/assets/<goos>_<goarch>/manifest.json`.
