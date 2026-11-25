# Fonts

Static (not variable) Noto Sans, unhinted, which has the ₹ sign (U+20B9) that PDF's built-in fonts lack. The generator embeds them with `go:embed` (`fonts.go`). They are licensed under the SIL Open Font License 1.1, in `OFL.txt`. Downloaded on 25 November 2026.

| File | Source | SHA-256 |
| --- | --- | --- |
| `NotoSans-Regular.ttf` | https://raw.githubusercontent.com/notofonts/notofonts.github.io/d14cc173a70dc57962ed2d4ad278fc9df83bf9f3/fonts/NotoSans/unhinted/ttf/NotoSans-Regular.ttf | `f3961a9cde016d41a4879aecda1474d3a36d6bf54fa0e4643de029cc2248b0e8` |
| `NotoSans-Bold.ttf` | https://raw.githubusercontent.com/notofonts/notofonts.github.io/d14cc173a70dc57962ed2d4ad278fc9df83bf9f3/fonts/NotoSans/unhinted/ttf/NotoSans-Bold.ttf | `87cb2d84472a7d66da659ee47b6cdb9552326e8c128245231f191b6ac72529d9` |
| `OFL.txt` | https://raw.githubusercontent.com/notofonts/latin-greek-cyrillic/ffaec0a756877220dbf718ce08b8479a0be82479/OFL.txt | `cee9892f9f0cc8fe882c9e9537ee6a89621d86ee7ceaf70b02e2b2b1c25c061a` |

`fonts_test.go` pins these hashes, so a changed font file fails a test before it can move a packet's bytes. Replacing a font changes every packet, so it needs a new generator version.
