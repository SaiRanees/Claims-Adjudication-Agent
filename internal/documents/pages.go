package documents

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"image"
	"io"
	"time"
	"unicode"

	"github.com/klippa-app/go-pdfium"
	"github.com/klippa-app/go-pdfium/requests"
	"github.com/pdfcpu/pdfcpu/pkg/api"
)

// TextLayerMinChars is how many characters that aren't white space a page's
// text needs before the page counts as having a text layer. Fewer, and the
// page is treated as a scan: its values can't be grounded in its text.
const TextLayerMinChars = 20

// PageDerivation is the derivation version of a one-page PDF split from an
// accepted file (ADR 0014). A model request names the page by its file's
// SHA-256, its page number and this version, never by the split bytes,
// which pdfcpu stamps with the time. Bump it by hand when the way a page is
// split changes, and re-record.
const PageDerivation = 1

// The fixed size a page is rendered at for its fingerprint: the largest
// image that fits, keeping the page's shape. An A4 page renders at 601 by
// 850 pixels.
const (
	FingerprintMaxWidth  = 850
	FingerprintMaxHeight = 850
)

// fingerprintVersion joins every fingerprint, so a change to how one is
// computed can't match an old one.
const fingerprintVersion = "claims-agent/page-fingerprint/v1\n"

// SplitDeadline bounds pdfcpu's splitting one file into pages.
const SplitDeadline = 30 * time.Second

// TextSpan is a run of text on one line in one font, with its box in points
// as PDFium reports it, measured from the page's top left.
type TextSpan struct {
	Text   string  `json:"text"`
	Left   float64 `json:"left"`
	Top    float64 `json:"top"`
	Right  float64 `json:"right"`
	Bottom float64 `json:"bottom"`
}

// PageRead is what PDFium reads from one page.
type PageRead struct {
	Number int // from 1
	// Text is the page's plain text, as the generator's self-check reads it.
	Text  string
	Spans []TextSpan
	// HasTextLayer: Text holds at least TextLayerMinChars characters that
	// aren't white space.
	HasTextLayer bool
	// Fingerprint is the SHA-256 of the page rendered in grayscale at the
	// fixed size, as lowercase hex. Two pages that render the same have the
	// same fingerprint, whatever their bytes.
	Fingerprint string
}

// Page is one prepared page of an accepted file.
type Page struct {
	PageRead
	// PDF is the page alone, split by pdfcpu. Its bytes differ on every run;
	// only its content is stable.
	PDF []byte
}

// HasTextLayer reports whether text has at least TextLayerMinChars
// characters that aren't white space.
func HasTextLayer(text string) bool {
	n := 0
	for _, r := range text {
		if !unicode.IsSpace(r) {
			n++
			if n >= TextLayerMinChars {
				return true
			}
		}
	}
	return false
}

// Prepare splits an accepted file into one-page PDFs and reads each page
// with PDFium: its text with positions, whether it has a text layer and its
// fingerprint. A file PDFium or pdfcpu can't read gives ErrUnreadable.
func Prepare(ctx context.Context, p *PDFium, data []byte) ([]Page, error) {
	reads, err := p.ReadPages(ctx, data)
	if err != nil {
		return nil, err
	}
	split, err := SplitPages(ctx, data, SplitDeadline)
	if err != nil {
		return nil, err
	}
	if len(split) != len(reads) {
		return nil, fmt.Errorf("%w: pdfcpu split %d pages, PDFium read %d", ErrUnreadable, len(split), len(reads))
	}
	pages := make([]Page, len(reads))
	for i := range reads {
		pages[i] = Page{PageRead: reads[i], PDF: split[i]}
	}
	return pages, nil
}

// ReadPages opens data as PageCount does and reads every page: its text,
// its text with positions, and its fingerprint.
func (p *PDFium) ReadPages(ctx context.Context, data []byte) ([]PageRead, error) {
	var pages []PageRead
	err := p.with(ctx, func(inst pdfium.Pdfium) error {
		doc, err := open(inst, data)
		if err != nil {
			return err
		}
		defer closeDocument(inst, doc)
		count, err := inst.FPDF_GetPageCount(&requests.FPDF_GetPageCount{Document: doc})
		if err != nil {
			return fmt.Errorf("%w: page count: %w", ErrUnreadable, err)
		}
		if count.PageCount < 1 {
			return fmt.Errorf("%w: no pages", ErrUnreadable)
		}
		for i := range count.PageCount {
			page := requests.Page{ByIndex: &requests.PageByIndex{Document: doc, Index: i}}
			pr, err := readPage(inst, page)
			if err != nil {
				return fmt.Errorf("%w: page %d: %w", ErrUnreadable, i+1, err)
			}
			pr.Number = i + 1
			pages = append(pages, pr)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return pages, nil
}

func readPage(inst pdfium.Pdfium, page requests.Page) (PageRead, error) {
	text, err := inst.GetPageText(&requests.GetPageText{Page: page})
	if err != nil {
		return PageRead{}, fmt.Errorf("text: %w", err)
	}
	structured, err := inst.GetPageTextStructured(&requests.GetPageTextStructured{
		Page: page, Mode: requests.GetPageTextStructuredModeRects,
	})
	if err != nil {
		return PageRead{}, fmt.Errorf("text positions: %w", err)
	}
	spans := make([]TextSpan, 0, len(structured.Rects))
	for _, r := range structured.Rects {
		spans = append(spans, TextSpan{
			Text: r.Text, Left: r.PointPosition.Left, Top: r.PointPosition.Top,
			Right: r.PointPosition.Right, Bottom: r.PointPosition.Bottom,
		})
	}
	fp, err := fingerprint(inst, page)
	if err != nil {
		return PageRead{}, err
	}
	return PageRead{Text: text.Text, Spans: spans, HasTextLayer: HasTextLayer(text.Text), Fingerprint: fp}, nil
}

// fingerprint renders the page in grayscale at the fixed size and hashes
// the image's size and pixels, row by row.
func fingerprint(inst pdfium.Pdfium, page requests.Page) (string, error) {
	r, err := inst.RenderPageInPixels(&requests.RenderPageInPixels{
		Page: page, Width: FingerprintMaxWidth, Height: FingerprintMaxHeight,
		ImageFormat: requests.RenderImageFormatGrayscale,
	})
	if err != nil {
		return "", fmt.Errorf("render: %w", err)
	}
	// In WebAssembly mode the pixels live in the module until Cleanup.
	defer r.Cleanup()
	img, ok := r.Result.RenderedImage.(*image.Gray)
	if !ok {
		return "", fmt.Errorf("render: got %T, want a grayscale image", r.Result.RenderedImage)
	}
	return grayHash(img), nil
}

// grayHash is the SHA-256 of the version, the image's width and height, and
// its pixels row by row (never the stride's padding).
func grayHash(img *image.Gray) string {
	h := sha256.New()
	_, _ = io.WriteString(h, fingerprintVersion)
	b := img.Bounds()
	var size [8]byte
	binary.BigEndian.PutUint32(size[:4], uint32(b.Dx())) //nolint:gosec // an image's size is never negative
	binary.BigEndian.PutUint32(size[4:], uint32(b.Dy())) //nolint:gosec // as above
	h.Write(size[:])
	for y := b.Min.Y; y < b.Max.Y; y++ {
		off := img.PixOffset(b.Min.X, y)
		h.Write(img.Pix[off : off+b.Dx()])
	}
	return hex.EncodeToString(h.Sum(nil))
}

// SplitPages splits data into one PDF per page with pdfcpu, under deadline.
// Run it only on a file PDFium has accepted: pdfcpu parses outside the
// sandbox (ADR 0013). A failure, or a panic pdfcpu doesn't recover itself,
// is ErrUnreadable.
func SplitPages(ctx context.Context, data []byte, deadline time.Duration) ([][]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()
	type result struct {
		pages [][]byte
		err   error
	}
	done := make(chan result, 1)
	go func() {
		var r result
		defer func() {
			if p := recover(); p != nil {
				r = result{err: fmt.Errorf("%w: pdfcpu panic: %v", ErrUnreadable, p)}
			}
			done <- r
		}()
		r.pages, r.err = split(ctx, data)
	}()
	select {
	case r := <-done:
		return r.pages, r.err
	case <-ctx.Done():
		return nil, fmt.Errorf("%w: pdfcpu: %w", ErrUnreadable, ctx.Err())
	}
}

func split(ctx context.Context, data []byte) ([][]byte, error) {
	var pages [][]byte
	digest := func(r io.Reader, n int) error {
		if n != len(pages)+1 {
			return fmt.Errorf("page %d came after page %d", n, len(pages))
		}
		b, err := io.ReadAll(r)
		if err != nil {
			return err
		}
		pages = append(pages, b)
		return nil
	}
	err := api.ExtractPages(ctx, bytes.NewReader(data), nil, digest, pdfcpuConfig())
	switch {
	case err != nil:
		return nil, fmt.Errorf("%w: pdfcpu split: %w", ErrUnreadable, err)
	case len(pages) == 0:
		return nil, fmt.Errorf("%w: pdfcpu split no pages", ErrUnreadable)
	}
	return pages, nil
}
