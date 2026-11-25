package documents

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/claims"
	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/documents/fixtures"
)

// fakeRecords holds one document and records the verdict it's given.
type fakeRecords struct {
	doc     claims.Document
	verdict *claims.Verdict
}

func (f *fakeRecords) DocumentForCheck(context.Context, uuid.UUID, uuid.UUID) (claims.Document, error) {
	return f.doc, nil
}

func (f *fakeRecords) RecordCheck(_ context.Context, _, _ uuid.UUID, v claims.Verdict) (claims.Document, error) {
	f.verdict = &v
	d := f.doc
	if v.Reason == nil {
		d.Status = claims.DocumentAccepted
		pages := v.PageCount
		d.PageCount, d.PDFSHA256 = &pages, v.PDFSHA256
	} else {
		d.Status, d.RejectionReason = claims.DocumentRejected, v.Reason
	}
	return d, nil
}

// fakeObjects is an in-memory object store.
type fakeObjects struct {
	mu      sync.Mutex
	objects map[string][]byte
	types   map[string]string
}

func newFakeObjects() *fakeObjects {
	return &fakeObjects{objects: map[string][]byte{}, types: map[string]string{}}
}

func (f *fakeObjects) Open(_ context.Context, key string) (io.ReadCloser, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, ok := f.objects[key]
	if !ok {
		return nil, ErrNoObject
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

func (f *fakeObjects) Put(_ context.Context, key, contentType string, body io.Reader, size int64) error {
	b, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	if int64(len(b)) != size {
		return errors.New("size mismatch")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.objects[key], f.types[key] = b, contentType
	return nil
}

func TestCheckerVerdicts(t *testing.T) {
	p := testPDFium(t)
	pdf := fixtures.TextPDF(3)
	jpg := fixtures.JPEG(t, 400, 300, 6)
	pngBytes := fixtures.PNG(t, 300, 200)
	tests := []struct {
		name        string
		contentType string
		declared    []byte // what the client declared and complete verified
		stored      []byte // what's in quarantine now; nil means nothing
		reason      claims.RejectionReason
		pages       int32
	}{
		{"pdf", "application/pdf", pdf, pdf, "", 3},
		{"jpeg orientation 6", "image/jpeg", jpg, jpg, "", 1},
		{"png", "image/png", pngBytes, pngBytes, "", 1},
		{"replaced after complete", "application/pdf", pdf, fixtures.TextPDF(4), claims.RejectChecksumMismatch, 0},
		{"deleted after complete", "application/pdf", pdf, nil, claims.RejectMissingObject, 0},
		{"a png declared as a pdf", "application/pdf", pngBytes, pngBytes, claims.RejectTypeMismatch, 0},
		{"a pdf declared as a jpeg", "image/jpeg", pdf, pdf, claims.RejectTypeMismatch, 0},
		{"user password", "application/pdf", fixtures.EncryptedPDF(t, "synthetic-user"), nil, claims.RejectEncrypted, 0},
		{"empty user password", "application/pdf", fixtures.EncryptedPDF(t, ""), nil, claims.RejectEncrypted, 0},
		{"truncated", "application/pdf", pdf[:len(pdf)/2], nil, claims.RejectUnreadable, 0},
		{"over 500 pages", "application/pdf", fixtures.TextPDF(501), nil, claims.RejectTooManyPages, 0},
		{"over 50 megapixels", "image/png", fixtures.HugePNG(10_000, 5_001), nil, claims.RejectImageTooLarge, 0},
		{"mirrored photo", "image/jpeg", fixtures.JPEG(t, 40, 30, 5), nil, claims.RejectUnreadable, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			claimID, docID := uuid.New(), uuid.New()
			sum := sha256.Sum256(tt.declared)
			records := &fakeRecords{doc: claims.Document{
				ID: docID, ClaimID: claimID, Status: claims.DocumentUploaded, ContentType: tt.contentType,
				SizeBytes: int64(len(tt.declared)), SHA256: sum[:], ObjectKey: "quarantine/" + claimID.String() + "/" + docID.String(),
			}}
			objects := newFakeObjects()
			stored := tt.stored
			if stored == nil && tt.name != "deleted after complete" {
				stored = tt.declared
			}
			if stored != nil {
				objects.objects[records.doc.ObjectKey] = stored
			}

			d, err := NewChecker(records, objects, p).Check(t.Context(), claimID, docID)
			if err != nil {
				t.Fatalf("check: %v", err)
			}
			if tt.reason != "" {
				if d.Status != claims.DocumentRejected || *d.RejectionReason != tt.reason {
					t.Fatalf("got %s %v, want rejected %s", d.Status, d.RejectionReason, tt.reason)
				}
				for key := range objects.objects {
					if strings.HasPrefix(key, "claims/") {
						t.Errorf("a rejected file wrote %s", key)
					}
				}
				return
			}
			if d.Status != claims.DocumentAccepted || *d.PageCount != tt.pages {
				t.Fatalf("got %s with %v pages, want accepted with %d", d.Status, d.PageCount, tt.pages)
			}
			original := objects.objects[OriginalKey(claimID, docID)]
			if !bytes.Equal(original, tt.declared) || objects.types[OriginalKey(claimID, docID)] != tt.contentType {
				t.Error("the original isn't the verified bytes with their type")
			}
			readPDF := original
			if tt.contentType != "application/pdf" {
				readPDF = objects.objects[PDFKey(claimID, docID)]
				if !bytes.HasPrefix(readPDF, []byte("%PDF-")) {
					t.Fatal("no document.pdf beside the photo")
				}
			}
			if want := sha256.Sum256(readPDF); !bytes.Equal(d.PDFSHA256, want[:]) {
				t.Error("pdf_sha256 isn't the hash of the PDF later steps read")
			}
		})
	}
}

// A file already decided returns as stored, without reading the object store.
func TestCheckerReturnsAStoredVerdict(t *testing.T) {
	reason := claims.RejectEncrypted
	records := &fakeRecords{doc: claims.Document{Status: claims.DocumentRejected, RejectionReason: &reason}}
	d, err := NewChecker(records, newFakeObjects(), testPDFium(t)).Check(t.Context(), uuid.New(), uuid.New())
	if err != nil || d.Status != claims.DocumentRejected || *d.RejectionReason != reason || records.verdict != nil {
		t.Fatalf("got %s %v, err %v, recorded %v", d.Status, d.RejectionReason, err, records.verdict)
	}
}
