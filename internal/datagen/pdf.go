package datagen

import (
	_ "embed" // the fonts
	"fmt"

	"github.com/signintech/gopdf"
)

// Static Noto Sans, which has the ₹ sign PDF's built-in fonts lack. Where
// each file came from, and its SHA-256, is in fonts/SOURCES.md.
var (
	//go:embed fonts/NotoSans-Regular.ttf
	notoRegular []byte
	//go:embed fonts/NotoSans-Bold.ttf
	notoBold []byte
)

// The font families a page draws with.
const (
	fontRegular = "regular"
	fontBold    = "bold"
)

// newPDF starts an A4 document in points with both fonts loaded, and with
// nothing that varies between runs: no protection (the only place gopdf
// reads the clock or a random source), no creation date, and uncompressed
// content streams, so a change to compress/flate can't move them. gopdf
// always compresses the embedded font subset, so the pinned packet hashes
// (session 4) are what notice a Go upgrade that changes flate's output.
func newPDF() (*gopdf.GoPdf, error) {
	pdf := &gopdf.GoPdf{}
	pdf.Start(gopdf.Config{Unit: gopdf.UnitPT, PageSize: *gopdf.PageSizeA4})
	pdf.SetNoCompression()
	if err := pdf.AddTTFFontData(fontRegular, notoRegular); err != nil {
		return nil, fmt.Errorf("datagen: load regular font: %w", err)
	}
	if err := pdf.AddTTFFontData(fontBold, notoBold); err != nil {
		return nil, fmt.Errorf("datagen: load bold font: %w", err)
	}
	return pdf, nil
}
