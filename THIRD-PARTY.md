# Third-party notices

This file records third-party code and data redistributed by Clavis. It is
included with source and the portable ZIP produced by `scripts/package.sh` so
that the corresponding notices remain available to recipients of the
application.

## Embedded data

| Asset | Version / source | License | Use | Modified? |
| --- | --- | --- | --- | --- |
| **rime-ice** (`cn_dicts/base.dict.yaml`) | [iDvel/rime-ice](https://github.com/iDvel/rime-ice), source snapshot used by the dictionary build | [Apache License 2.0](https://www.apache.org/licenses/LICENSE-2.0) | Mandarin candidate words embedded in `pkg/core/inputengine/data/common.clvdict` | The upstream file was not modified. Clavis extracted entries that are pure Hanzi and selected weights in `[20000, 600000)`, then compiled the selected entries into its own binary dictionary. This filtered extraction is a derivative dataset and retains the upstream license notice. |
| Unicode Unihan Mandarin readings | [Unicode UCD / Unihan](https://www.unicode.org/Public/UCD/latest/ucd/Unihan.zip), generated table in `pkg/core/engine/pinyin_table.go` | [Unicode License](https://www.unicode.org/license.html) | Hanzi-to-Pinyin readings and initials | The table is generated from the Unicode data; the upstream data files are not modified. |

### rime-ice Apache-2.0 notice

The Apache License 2.0 text is available at
<https://www.apache.org/licenses/LICENSE-2.0>. That link is the authoritative
full text and is incorporated here instead of duplicating the license text.
The rime-ice source attribution and this notice must remain with Clavis
source and binary distributions. The portable ZIP includes this file and
`LICENSE` beside `Clavis.app`; the DMG is the MyGo-generated installer image
for that app and its release should be distributed together with this file. No patent, copyright, or NOTICE statement
from the rime-ice repository is removed by the extraction process.

## Go modules

The following entries are the direct and indirect modules declared in
`go.mod` at the time this notice was written. URLs point to the module's
canonical source repository or project page. Go's standard library is not
listed.

| Module | Version | License | Use | Modified? |
| --- | --- | --- | --- | --- |
| [BurntSushi/toml](https://github.com/BurntSushi/toml) | v1.6.0 | MIT | TOML configuration parsing and writing | No |
| [ebitengine/purego](https://github.com/ebitengine/purego) | v0.11.1 | Apache-2.0 | Zero-CGo native platform calls | No |
| [egoist/mygo](https://github.com/egoist/mygo) | v0.9.0 | MIT | Desktop application runtime, UI, packaging, and plugins | No |
| [golang.org/x/sys](https://go.googlesource.com/sys) | v0.48.0 | BSD-3-Clause | Windows and low-level OS APIs | No |
| [ProtonMail/go-crypto](https://github.com/ProtonMail/go-crypto) | v1.4.1 | BSD-3-Clause | Transitive cryptographic support | No |
| [cloudflare/circl](https://github.com/cloudflare/circl) | v1.6.2 | BSD-3-Clause | Transitive cryptographic support | No |
| [go-text/typesetting](https://github.com/go-text/typesetting) | v0.3.5 | Unlicense OR BSD-3-Clause | Transitive text shaping and font support | No |
| [klauspost/compress](https://github.com/klauspost/compress) | v1.18.6 | BSD-3-Clause | Transitive compression support | No |
| [ulikunitz/xz](https://github.com/ulikunitz/xz) | v0.5.15 | BSD-3-Clause | Transitive XZ decompression support | No |
| [go.digitalxero.dev/rpm](https://go.digitalxero.dev/rpm) | v0.3.4 | MIT | Transitive RPM packaging support used by MyGo | No |
| [golang.org/x/crypto](https://go.googlesource.com/crypto) | v0.57.0 | BSD-3-Clause | Transitive cryptographic support | No |
| [golang.org/x/image](https://go.googlesource.com/image) | v0.46.0 | BSD-3-Clause | Transitive image support | No |
| [golang.org/x/net](https://go.googlesource.com/net) | v0.59.0 | BSD-3-Clause | Transitive networking support | No |
| [golang.org/x/text](https://go.googlesource.com/text) | v0.43.0 | BSD-3-Clause | Transitive text and locale support | No |

The license texts and copyright notices for redistributed module code are
available in each module's upstream repository and module source distribution.
For BSD-3-Clause dependencies, Clavis preserves the required copyright,
conditions, and disclaimer notices through this inventory and the upstream
license files. Apache-2.0 dependencies retain their license and attribution
requirements; no source files from those modules were modified by Clavis.

This inventory is not a replacement for the complete license text. Where a
license is linked above, the linked upstream license is the governing text.
