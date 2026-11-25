package documents

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"

	"github.com/google/uuid"

	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/claims"
)

// MaxPages is the most pages a PDF may have.
const MaxPages = 500

// CheckRecords is the database side of the worker's check, which claims.Store provides.
type CheckRecords interface {
	DocumentForCheck(ctx context.Context, claimID, documentID uuid.UUID) (claims.Document, error)
	RecordCheck(ctx context.Context, claimID, documentID uuid.UUID, v claims.Verdict) (claims.Document, error)
}

// CheckObjects is the part of Objects the check uses.
type CheckObjects interface {
	Open(ctx context.Context, key string) (io.ReadCloser, error)
	Put(ctx context.Context, key, contentType string, body io.Reader, size int64) error
}

// PageCounter reads a PDF's page count in a sandbox; PDFium provides it.
type PageCounter interface {
	PageCount(ctx context.Context, data []byte, maxPages int) (int, error)
}

// Checker decides whether an uploaded file is usable. A bad file is a verdict, never an
// error: an error means the database, the object store or the sandbox failed, and the
// caller retries.
type Checker struct {
	records CheckRecords
	objects CheckObjects
	pdfium  PageCounter
}

// NewChecker returns a Checker.
func NewChecker(records CheckRecords, objects CheckObjects, pdfium PageCounter) *Checker {
	return &Checker{records: records, objects: objects, pdfium: pdfium}
}

// OriginalKey is where an accepted file's bytes are kept: the ones the check verified.
func OriginalKey(claimID, documentID uuid.UUID) string {
	return "claims/" + claimID.String() + "/" + documentID.String() + "/original"
}

// PDFKey is where a photo's one-page PDF is kept, beside its original.
func PDFKey(claimID, documentID uuid.UUID) string {
	return "claims/" + claimID.String() + "/" + documentID.String() + "/document.pdf"
}

// Check checks one uploaded file and commits the verdict. A file already accepted or
// rejected returns as it is stored, so a retry after a crash changes nothing.
func (c *Checker) Check(ctx context.Context, claimID, documentID uuid.UUID) (claims.Document, error) {
	d, err := c.records.DocumentForCheck(ctx, claimID, documentID)
	if err != nil {
		return claims.Document{}, err
	}
	switch d.Status {
	case claims.DocumentAccepted, claims.DocumentRejected:
		return d, nil
	case claims.DocumentPendingUpload:
		return claims.Document{}, claims.ErrNotUploaded
	case claims.DocumentUploaded:
	}

	v, err := c.judge(ctx, d)
	if err != nil {
		return claims.Document{}, err
	}
	return c.records.RecordCheck(ctx, claimID, documentID, v)
}

// Reject records reason for a file the check couldn't finish (check_failed).
func (c *Checker) Reject(ctx context.Context, claimID, documentID uuid.UUID, reason claims.RejectionReason) (claims.Document, error) {
	return c.records.RecordCheck(ctx, claimID, documentID, claims.Verdict{Reason: &reason})
}

func rejected(r claims.RejectionReason) (claims.Verdict, error) {
	return claims.Verdict{Reason: &r}, nil
}

// judge reads the file from quarantine and decides. Any accepted bytes are written under
// claims/ before it returns.
func (c *Checker) judge(ctx context.Context, d claims.Document) (claims.Verdict, error) {
	// The upload URL is still valid after complete, so the object may have been replaced:
	// the bytes are read and hashed again, and only these bytes are used from here on.
	data, err := c.read(ctx, d)
	if errors.Is(err, ErrNoObject) {
		return rejected(claims.RejectMissingObject)
	}
	if err != nil {
		return claims.Verdict{}, err
	}
	sum := sha256.Sum256(data)
	if int64(len(data)) != d.SizeBytes || !bytes.Equal(sum[:], d.SHA256) {
		return rejected(claims.RejectChecksumMismatch)
	}
	if !magicMatches(d.ContentType, data) {
		return rejected(claims.RejectTypeMismatch)
	}

	pdf := data
	if d.ContentType != "application/pdf" {
		_, _, rotate, err := ImageHeader(data)
		switch {
		case errors.Is(err, ErrImageTooLarge):
			return rejected(claims.RejectImageTooLarge)
		case err != nil:
			return rejected(claims.RejectUnreadable)
		}
		if pdf, err = PhotoToPDF(ctx, data, rotate, PhotoDeadline); err != nil {
			return rejected(claims.RejectUnreadable)
		}
	}

	pages, err := c.pdfium.PageCount(ctx, pdf, MaxPages)
	switch {
	case errors.Is(err, ErrEncrypted):
		return rejected(claims.RejectEncrypted)
	case errors.Is(err, ErrUnreadable):
		return rejected(claims.RejectUnreadable)
	case err != nil:
		return claims.Verdict{}, err
	case pages > MaxPages:
		return rejected(claims.RejectTooManyPages)
	}

	// Written from the verified bytes, never by copying the quarantine key. A retry writes
	// the same keys again.
	if err := c.objects.Put(ctx, OriginalKey(d.ClaimID, d.ID), d.ContentType, bytes.NewReader(data), int64(len(data))); err != nil {
		return claims.Verdict{}, err
	}
	if d.ContentType != "application/pdf" {
		if err := c.objects.Put(ctx, PDFKey(d.ClaimID, d.ID), "application/pdf", bytes.NewReader(pdf), int64(len(pdf))); err != nil {
			return claims.Verdict{}, err
		}
	}
	pdfSum := sha256.Sum256(pdf)
	return claims.Verdict{PageCount: int32(pages), PDFSHA256: pdfSum[:]}, nil //nolint:gosec // at most MaxPages
}

// read returns the quarantined object, reading at most one byte past its declared size.
func (c *Checker) read(ctx context.Context, d claims.Document) ([]byte, error) {
	body, err := c.objects.Open(ctx, d.ObjectKey)
	if err != nil {
		return nil, err
	}
	defer func() { _ = body.Close() }()
	data, err := io.ReadAll(io.LimitReader(body, d.SizeBytes+1))
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", d.ObjectKey, err)
	}
	return data, nil
}

// magicMatches reports whether data starts as its declared type must. A PDF's header may
// follow up to 1,024 bytes of junk, as readers allow.
func magicMatches(contentType string, data []byte) bool {
	switch contentType {
	case "application/pdf":
		return bytes.Contains(data[:min(len(data), 1024)], []byte("%PDF-"))
	case "image/jpeg":
		return bytes.HasPrefix(data, []byte{0xFF, 0xD8, 0xFF})
	case "image/png":
		return bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n"))
	}
	return false
}
