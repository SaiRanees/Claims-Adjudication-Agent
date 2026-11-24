package documents

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/google/uuid"

	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/claims"
)

// UploadTTL is how long an upload URL works.
const UploadTTL = 15 * time.Minute

// Records is the database side of uploads, which claims.Store provides: each method runs
// in its own transaction under the claim's row lock.
type Records interface {
	RegisterDocument(ctx context.Context, nd claims.NewDocument) (claims.Document, bool, error)
	Document(ctx context.Context, principal string, claimID, documentID uuid.UUID) (claims.Claim, claims.Document, error)
	RecordUpload(ctx context.Context, principal string, claimID, documentID uuid.UUID, reason *claims.RejectionReason) (claims.Document, error)
}

// ObjectStore is the part of Objects the service uses.
type ObjectStore interface {
	PresignPut(ctx context.Context, key, contentType string, size int64, ttl time.Duration) (Upload, error)
	Size(ctx context.Context, key string) (int64, error)
	Open(ctx context.Context, key string) (io.ReadCloser, error)
}

// Service runs the upload protocol: register a file and hand out a signed URL for it, then
// complete it by checking the stored object against what was declared. Object-store I/O
// happens outside any database transaction, so no claim's row stays locked while it runs.
type Service struct {
	records Records
	objects ObjectStore
}

// NewService returns a Service that keeps files' records in records and their bytes in objects.
func NewService(records Records, objects ObjectStore) *Service {
	return &Service{records: records, objects: objects}
}

// Registered is a registered file and how to upload it.
type Registered struct {
	Document claims.Document
	Upload   Upload
	// Created is false when the file was already registered and still waits for its upload.
	Created bool
}

// Register records a file the claim's submitter is about to upload (see
// claims.Store.RegisterDocument) and signs a URL for its upload, once the registration
// has committed. A re-registration gets a fresh URL.
func (s *Service) Register(ctx context.Context, nd claims.NewDocument) (Registered, error) {
	d, created, err := s.records.RegisterDocument(ctx, nd)
	if err != nil {
		return Registered{}, err
	}
	up, err := s.objects.PresignPut(ctx, d.ObjectKey, d.ContentType, d.SizeBytes, UploadTTL)
	if err != nil {
		return Registered{}, err
	}
	return Registered{Document: d, Upload: up, Created: created}, nil
}

// Complete checks a file's stored object against its declared size and SHA-256, then
// records it as uploaded or rejected (see claims.Store.RecordUpload). A file already past
// pending_upload is returned as it is, without reading the store again. Once the claim
// no longer takes documents, it's claims.ErrNotAcceptingDocuments.
//
// An error reading the store leaves the file pending_upload, so complete can be retried.
func (s *Service) Complete(ctx context.Context, principal string, claimID, documentID uuid.UUID) (claims.Document, error) {
	c, d, err := s.records.Document(ctx, principal, claimID, documentID)
	if err != nil {
		return claims.Document{}, err
	}
	if !c.AcceptsDocuments() {
		return claims.Document{}, claims.ErrNotAcceptingDocuments
	}
	if d.Status != claims.DocumentPendingUpload {
		return d, nil
	}
	reason, err := s.inspect(ctx, d)
	if err != nil {
		return claims.Document{}, err
	}
	return s.records.RecordUpload(ctx, principal, claimID, documentID, reason)
}

// inspect reads the object's size, then streams it through SHA-256. It returns why the
// object doesn't match its declaration, or nil when it does.
func (s *Service) inspect(ctx context.Context, d claims.Document) (*claims.RejectionReason, error) {
	reject := func(r claims.RejectionReason) (*claims.RejectionReason, error) { return &r, nil }

	size, err := s.objects.Size(ctx, d.ObjectKey)
	switch {
	case errors.Is(err, ErrNoObject):
		return reject(claims.RejectMissingObject)
	case err != nil:
		return nil, err
	case size != d.SizeBytes:
		return reject(claims.RejectSizeMismatch)
	}

	body, err := s.objects.Open(ctx, d.ObjectKey)
	switch {
	case errors.Is(err, ErrNoObject):
		return reject(claims.RejectMissingObject)
	case err != nil:
		return nil, err
	}
	defer func() { _ = body.Close() }()
	h := sha256.New()
	// One byte past the declared size is enough to tell that the object is longer: it can
	// have changed since its size was read.
	n, err := io.Copy(h, io.LimitReader(body, d.SizeBytes+1))
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", d.ObjectKey, err)
	}
	if n != d.SizeBytes {
		return reject(claims.RejectSizeMismatch)
	}
	if !bytes.Equal(h.Sum(nil), d.SHA256) {
		return reject(claims.RejectChecksumMismatch)
	}
	return nil, nil
}
