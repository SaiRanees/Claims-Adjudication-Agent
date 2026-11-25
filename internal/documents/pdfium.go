package documents

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/klippa-app/go-pdfium"
	pdfiumerrors "github.com/klippa-app/go-pdfium/errors"
	"github.com/klippa-app/go-pdfium/references"
	"github.com/klippa-app/go-pdfium/requests"
	"github.com/klippa-app/go-pdfium/webassembly"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/experimental"
)

// Verdicts PDFium can reach about a file. Any other error is an
// infrastructure error, which the caller retries.
var (
	// ErrEncrypted: the file has a security handler, whether or not its user
	// password is empty.
	ErrEncrypted = errors.New("pdf is encrypted")
	// ErrUnreadable: PDFium can't load the file or one of its pages, ran out
	// of sandbox memory doing so, or ran past its deadline.
	ErrUnreadable = errors.New("pdf is unreadable")
)

// PDFiumConfig sizes the sandbox.
type PDFiumConfig struct {
	// Workers is how many files can be read at once.
	Workers int
	// MemoryLimitMiB caps each sandbox's linear memory. An upload is at most
	// 25 MiB and is copied in, so 256 leaves PDFium room to parse it.
	MemoryLimitMiB uint32
	// Deadline bounds reading one file, from taking a worker to closing it.
	Deadline time.Duration
}

// DefaultPDFiumConfig is what the worker runs with; pdfium_test.go measures
// the timings it rests on.
var DefaultPDFiumConfig = PDFiumConfig{Workers: 2, MemoryLimitMiB: 256, Deadline: 30 * time.Second}

// PDFium reads PDFs inside go-pdfium's WebAssembly build of PDFium. The
// sandbox mounts no directory, has a memory limit, and is killed when a read
// runs past its deadline (ADR 0013).
type PDFium struct {
	pool     pdfium.Pool
	deadline time.Duration
}

// NewPDFium compiles PDFium and starts the pool. Compiling takes seconds, so
// a process makes one and shares it.
func NewPDFium(cfg PDFiumConfig) (*PDFium, error) {
	if cfg.Workers < 1 || cfg.MemoryLimitMiB == 0 || cfg.Deadline <= 0 {
		return nil, fmt.Errorf("pdfium: invalid config %+v", cfg)
	}
	runtime := wazero.NewRuntimeConfig().
		// The bundled module needs exception handling (go-pdfium's default).
		WithCoreFeatures(api.CoreFeaturesV2 | experimental.CoreFeaturesExceptionHandling).
		WithMemoryLimitPages(cfg.MemoryLimitMiB * 16). // 64 KiB pages
		// Lets Kill stop a call in progress.
		WithCloseOnContextDone(true)
	pool, err := webassembly.Init(webassembly.Config{
		MaxIdle:  cfg.Workers,
		MaxTotal: cfg.Workers,
		// An empty FSConfig mounts nothing. Left nil, go-pdfium mounts the
		// host's root (on Windows, the working directory's drive).
		FSConfig:      wazero.NewFSConfig(),
		RuntimeConfig: runtime,
		Stdout:        io.Discard,
		Stderr:        io.Discard,
	})
	if err != nil {
		return nil, fmt.Errorf("pdfium: start pool: %w", err)
	}
	return &PDFium{pool: pool, deadline: cfg.Deadline}, nil
}

// Close stops the pool.
func (p *PDFium) Close() error { return p.pool.Close() }

// PageCount opens data, rejects it if it has a security handler, and loads
// every page, which makes PDFium parse each page's content. A bad file gives
// ErrEncrypted or ErrUnreadable; any other error means the sandbox failed.
// A file with more than maxPages pages returns its count without loading
// them.
func (p *PDFium) PageCount(ctx context.Context, data []byte, maxPages int) (int, error) {
	var pages int
	err := p.with(ctx, func(inst pdfium.Pdfium) error {
		var err error
		pages, err = read(inst, data, maxPages)
		return err
	})
	if err != nil {
		return 0, err
	}
	return pages, nil
}

// PageTexts opens data as PageCount does and returns each page's plain
// text, in page order. The generator's self-check reads its packets back
// with it.
func (p *PDFium) PageTexts(ctx context.Context, data []byte) ([]string, error) {
	var texts []string
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
		for i := range count.PageCount {
			t, err := inst.GetPageText(&requests.GetPageText{
				Page: requests.Page{ByIndex: &requests.PageByIndex{Document: doc, Index: i}},
			})
			if err != nil {
				return fmt.Errorf("%w: page %d text: %w", ErrUnreadable, i+1, err)
			}
			texts = append(texts, t.Text)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return texts, nil
}

// with runs fn on a worker under the deadline. A bad file gives
// ErrEncrypted or ErrUnreadable; any other error means the sandbox failed.
func (p *PDFium) with(ctx context.Context, fn func(pdfium.Pdfium) error) error {
	ctx, cancel := context.WithTimeout(ctx, p.deadline)
	defer cancel()

	inst, err := p.pool.GetInstanceWithContext(ctx)
	if err != nil {
		return fmt.Errorf("pdfium: take a worker: %w", err)
	}
	// Kill cancels the worker's context, which closes the module mid-call
	// because the runtime has WithCloseOnContextDone.
	stop := context.AfterFunc(ctx, func() { _ = inst.Kill() })

	readErr := fn(inst)

	if !stop() {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return fmt.Errorf("%w: deadline", ErrUnreadable)
		}
		return fmt.Errorf("pdfium: %w", ctx.Err())
	}
	if readErr != nil && !errors.Is(readErr, ErrEncrypted) && !errors.Is(readErr, ErrUnreadable) {
		// A trap (out of sandbox memory, for one) can leave the module in
		// any state, so it's thrown away rather than closed.
		_ = inst.Kill()
		return fmt.Errorf("%w: %w", ErrUnreadable, readErr)
	}
	if err := inst.Close(); err != nil && readErr == nil {
		return fmt.Errorf("pdfium: close worker: %w", err)
	}
	return readErr
}

// open loads data and rejects it if it has a security handler.
func open(inst pdfium.Pdfium, data []byte) (references.FPDF_DOCUMENT, error) {
	doc, err := inst.OpenDocument(&requests.OpenDocument{File: &data})
	switch {
	case errors.Is(err, pdfiumerrors.ErrPassword):
		return "", ErrEncrypted
	case err != nil:
		return "", fmt.Errorf("%w: open: %w", ErrUnreadable, err)
	}
	sec, err := inst.FPDF_GetSecurityHandlerRevision(&requests.FPDF_GetSecurityHandlerRevision{Document: doc.Document})
	if err != nil {
		closeDocument(inst, doc.Document)
		return "", fmt.Errorf("%w: security handler: %w", ErrUnreadable, err)
	}
	if sec.SecurityHandlerRevision != -1 {
		closeDocument(inst, doc.Document)
		return "", ErrEncrypted
	}
	return doc.Document, nil
}

func closeDocument(inst pdfium.Pdfium, doc references.FPDF_DOCUMENT) {
	_, _ = inst.FPDF_CloseDocument(&requests.FPDF_CloseDocument{Document: doc})
}

func read(inst pdfium.Pdfium, data []byte, maxPages int) (int, error) {
	doc, err := open(inst, data)
	if err != nil {
		return 0, err
	}
	defer closeDocument(inst, doc)

	count, err := inst.FPDF_GetPageCount(&requests.FPDF_GetPageCount{Document: doc})
	if err != nil {
		return 0, fmt.Errorf("%w: page count: %w", ErrUnreadable, err)
	}
	if count.PageCount < 1 {
		return 0, fmt.Errorf("%w: no pages", ErrUnreadable)
	}
	if count.PageCount > maxPages {
		return count.PageCount, nil
	}
	for i := range count.PageCount {
		page, err := inst.FPDF_LoadPage(&requests.FPDF_LoadPage{Document: doc, Index: i})
		if err != nil {
			return 0, fmt.Errorf("%w: page %d: %w", ErrUnreadable, i+1, err)
		}
		_, _ = inst.FPDF_ClosePage(&requests.FPDF_ClosePage{Page: page.Page})
	}
	return count.PageCount, nil
}
