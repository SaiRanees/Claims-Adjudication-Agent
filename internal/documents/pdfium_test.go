package documents

import (
	"bytes"
	"context"
	"errors"
	"image"
	"sync"
	"testing"
	"time"

	"github.com/klippa-app/go-pdfium/requests"
)

// The proof session 2 asks for before anything relies on go-pdfium and
// pdfcpu: PDFium reads bytes in a sandbox that mounts nothing, a stream that
// inflates past the memory limit is unreadable rather than growing the
// process, a deadline stops a long parse, and pdfcpu turns a JPEG and a PNG
// into PDFs. TestCheckTimings measures what the pool size and deadlines rest
// on.

var (
	sharedOnce   sync.Once
	sharedPDFium *PDFium
	sharedErr    error
)

// testPDFium is one pool for the package's tests: compiling PDFium takes
// seconds.
func testPDFium(t *testing.T) *PDFium {
	t.Helper()
	sharedOnce.Do(func() { sharedPDFium, sharedErr = NewPDFium(DefaultPDFiumConfig) })
	if sharedErr != nil {
		t.Fatalf("start pdfium: %v", sharedErr)
	}
	return sharedPDFium
}

func TestPDFiumPageCount(t *testing.T) {
	p := testPDFium(t)
	ten := textPDF(10)
	tests := []struct {
		name     string
		data     []byte
		maxPages int
		pages    int
		err      error
	}{
		{"ten pages", ten, 500, 10, nil},
		{"over the page cap counts without loading", ten, 9, 10, nil},
		{"user password", encryptedPDF(t, "synthetic-user"), 500, 0, ErrEncrypted},
		{"empty user password", encryptedPDF(t, ""), 500, 0, ErrEncrypted},
		{"truncated", ten[:len(ten)/3], 500, 0, ErrUnreadable},
		{"not a pdf", []byte("%PDF-1.7\nhello"), 500, 0, ErrUnreadable},
		{"empty", nil, 500, 0, ErrUnreadable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pages, err := p.PageCount(t.Context(), tt.data, tt.maxPages)
			if !errors.Is(err, tt.err) || (tt.err == nil && err != nil) {
				t.Fatalf("err = %v, want %v", err, tt.err)
			}
			if pages != tt.pages {
				t.Errorf("pages = %d, want %d", pages, tt.pages)
			}
		})
	}
}

// The sandbox mounts nothing, so a PDF can't reach the host's files. PDFium
// opens a path through the module's filesystem; with an empty FSConfig there
// is none, even for a file that exists next to this test.
func TestPDFiumMountsNothing(t *testing.T) {
	p := testPDFium(t)
	inst, err := p.pool.GetInstanceWithContext(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = inst.Close() }()
	for _, path := range []string{"pdfium_test.go", "/pdfium_test.go", "/etc/hosts", "C:\\Windows\\win.ini", "/"} {
		if _, err := inst.OpenDocument(&requests.OpenDocument{FilePath: &path}); err == nil {
			t.Errorf("opened %q through the sandbox", path)
		}
	}
}

// A content stream that inflates to 1 GiB can't fit in a 256 MiB sandbox:
// it comes back unreadable, and the pool still works afterwards.
func TestPDFiumInflateBomb(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a 1 GiB inflate bomb")
	}
	p := testPDFium(t)
	bomb := bombPDF(t, 1<<30)
	t.Logf("bomb: %d bytes on disk, 1 GiB inflated", len(bomb))
	start := time.Now()
	_, err := p.PageCount(t.Context(), bomb, 500)
	t.Logf("bomb verdict after %v: %v", time.Since(start), err)
	if !errors.Is(err, ErrUnreadable) {
		t.Fatalf("err = %v, want ErrUnreadable", err)
	}
	if pages, err := p.PageCount(t.Context(), textPDF(2), 500); err != nil || pages != 2 {
		t.Fatalf("after the bomb: pages %d, err %v", pages, err)
	}
}

// A deadline stops a parse in progress, and the pool replaces the killed
// worker.
func TestPDFiumDeadline(t *testing.T) {
	// 64 MiB of whitespace fits a 1 GiB sandbox, and takes PDFium seconds
	// to inflate and scan.
	slow := bombPDF(t, 64<<20)
	full, err := NewPDFium(PDFiumConfig{Workers: 1, MemoryLimitMiB: 1024, Deadline: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = full.Close() }()
	start := time.Now()
	if _, err := full.PageCount(t.Context(), slow, 500); err != nil {
		t.Fatalf("slow PDF without a short deadline: %v", err)
	}
	unbounded := time.Since(start)
	t.Logf("slow PDF parses in %v", unbounded)

	const deadline = 200 * time.Millisecond
	if unbounded < 5*deadline {
		t.Fatalf("slow PDF parses in %v, too fast to test a %v deadline", unbounded, deadline)
	}
	short, err := NewPDFium(PDFiumConfig{Workers: 1, MemoryLimitMiB: 1024, Deadline: deadline})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = short.Close() }()
	start = time.Now()
	_, err = short.PageCount(t.Context(), slow, 500)
	took := time.Since(start)
	t.Logf("with a %v deadline: %v after %v", deadline, err, took)
	if !errors.Is(err, ErrUnreadable) {
		t.Fatalf("err = %v, want ErrUnreadable", err)
	}
	if took > deadline+time.Second {
		t.Errorf("took %v, deadline %v", took, deadline)
	}
	// One worker, killed: the next read needs a fresh one.
	if pages, err := short.PageCount(t.Context(), textPDF(1), 500); err != nil || pages != 1 {
		t.Fatalf("after the kill: pages %d, err %v", pages, err)
	}
}

func TestImageHeader(t *testing.T) {
	tests := []struct {
		name          string
		data          []byte
		width, height int
		rotate        int
		err           error
	}{
		{"jpeg without exif", jpegWithOrientation(t, 40, 30, 0), 40, 30, 0, nil},
		{"jpeg orientation 1", jpegWithOrientation(t, 40, 30, 1), 40, 30, 0, nil},
		{"jpeg orientation 3", jpegWithOrientation(t, 40, 30, 3), 40, 30, 180, nil},
		{"jpeg orientation 6", jpegWithOrientation(t, 40, 30, 6), 40, 30, 90, nil},
		{"jpeg orientation 8", jpegWithOrientation(t, 40, 30, 8), 40, 30, 270, nil},
		{"jpeg mirrored", jpegWithOrientation(t, 40, 30, 2), 0, 0, 0, ErrBadImage},
		{"jpeg orientation 9", jpegWithOrientation(t, 40, 30, 9), 0, 0, 0, ErrBadImage},
		{"png", pngImage(t, 40, 30), 40, 30, 0, nil},
		{"png over 50 megapixels", hugePNG(10_000, 5_001), 10_000, 5_001, 0, ErrImageTooLarge},
		{"png at 50 megapixels", hugePNG(10_000, 5_000), 10_000, 5_000, 0, nil},
		{"not an image", []byte("hello"), 0, 0, 0, ErrBadImage},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w, h, rot, err := ImageHeader(tt.data)
			if !errors.Is(err, tt.err) || (tt.err == nil && err != nil) {
				t.Fatalf("err = %v, want %v", err, tt.err)
			}
			if w != tt.width || h != tt.height || rot != tt.rotate {
				t.Errorf("got %dx%d rotate %d, want %dx%d rotate %d", w, h, rot, tt.width, tt.height, tt.rotate)
			}
		})
	}
}

// pdfcpu turns a JPEG and a PNG into one-page PDFs PDFium accepts, and an
// orientation-6 photo renders with its top bar at the top.
func TestPhotoToPDF(t *testing.T) {
	p := testPDFium(t)
	tests := []struct {
		name string
		data []byte
	}{
		{"jpeg", jpegWithOrientation(t, 400, 300, 0)},
		{"jpeg orientation 6", jpegWithOrientation(t, 400, 300, 6)},
		{"png", pngImage(t, 400, 300)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, rotate, err := ImageHeader(tt.data)
			if err != nil {
				t.Fatal(err)
			}
			pdf, err := PhotoToPDF(t.Context(), tt.data, rotate, 10*time.Second)
			if err != nil {
				t.Fatalf("to pdf: %v", err)
			}
			if !bytes.HasPrefix(pdf, []byte("%PDF-")) {
				t.Fatalf("not a PDF: %q", pdf[:min(len(pdf), 16)])
			}
			if pages, err := p.PageCount(t.Context(), pdf, 500); err != nil || pages != 1 {
				t.Fatalf("pdfium: pages %d, err %v", pages, err)
			}
			img := render(t, p, pdf)
			b := img.Bounds()
			if rotate == 90 {
				// Upright: portrait, bar along the top.
				if b.Dx() >= b.Dy() {
					t.Errorf("rendered %dx%d, want portrait", b.Dx(), b.Dy())
				}
				if !dark(img, b.Dx()/2, b.Dy()/20) || dark(img, b.Dx()/2, b.Dy()-b.Dy()/20) || dark(img, b.Dx()/20, b.Dy()/2) {
					t.Error("the bar isn't along the top")
				}
				return
			}
			if b.Dx() <= b.Dy() {
				t.Errorf("rendered %dx%d, want landscape", b.Dx(), b.Dy())
			}
			if !dark(img, b.Dx()/20, b.Dy()/2) || dark(img, b.Dx()-b.Dx()/20, b.Dy()/2) {
				t.Error("the bar isn't along the left")
			}
		})
	}
}

func dark(img image.Image, x, y int) bool {
	r, g, b, _ := img.At(x, y).RGBA()
	return (r+g+b)/3 < 0x8000
}

// render draws page 1 of pdf at 36 DPI and copies it out of the sandbox.
func render(t *testing.T, p *PDFium, pdf []byte) image.Image {
	t.Helper()
	inst, err := p.pool.GetInstanceWithContext(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = inst.Close() }()
	doc, err := inst.OpenDocument(&requests.OpenDocument{File: &pdf})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = inst.FPDF_CloseDocument(&requests.FPDF_CloseDocument{Document: doc.Document}) }()
	r, err := inst.RenderPageInDPI(&requests.RenderPageInDPI{
		Page: requests.Page{ByIndex: &requests.PageByIndex{Document: doc.Document, Index: 0}},
		DPI:  36,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Cleanup()
	src := r.Result.RenderedImage
	out := image.NewRGBA(src.Bounds())
	for y := src.Bounds().Min.Y; y < src.Bounds().Max.Y; y++ {
		for x := src.Bounds().Min.X; x < src.Bounds().Max.X; x++ {
			out.Set(x, y, src.At(x, y))
		}
	}
	return out
}

// TestCheckTimings measures a check on a 10-page PDF and on a photo, which
// DefaultPDFiumConfig's worker count and deadline, and the photo deadline,
// rest on. It fails if either is anywhere near its deadline.
func TestCheckTimings(t *testing.T) {
	p := testPDFium(t)
	ten := textPDF(10)
	jpg := jpegWithOrientation(t, 4000, 3000, 6) // a 12 MP phone photo
	measure := func(name string, limit time.Duration, f func(ctx context.Context) error) {
		const runs = 5
		var worst time.Duration
		for range runs {
			start := time.Now()
			if err := f(t.Context()); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			worst = max(worst, time.Since(start))
		}
		t.Logf("%s: worst of %d runs %v (limit %v)", name, runs, worst, limit)
		if worst > limit/10 {
			t.Errorf("%s took %v, more than a tenth of its %v deadline", name, worst, limit)
		}
	}
	measure("10-page PDF", DefaultPDFiumConfig.Deadline, func(ctx context.Context) error {
		_, err := p.PageCount(ctx, ten, 500)
		return err
	})
	measure("12 MP photo", PhotoDeadline, func(ctx context.Context) error {
		_, _, rot, err := ImageHeader(jpg)
		if err != nil {
			return err
		}
		pdf, err := PhotoToPDF(ctx, jpg, rot, PhotoDeadline)
		if err != nil {
			return err
		}
		_, err = p.PageCount(ctx, pdf, 500)
		return err
	})
}
