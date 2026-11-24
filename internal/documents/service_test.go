package documents_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/claims"
	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/documents"
)

// fakeRecords stands in for claims.Store and records each verdict it's given.
type fakeRecords struct {
	claim      claims.Claim
	doc        claims.Document
	registered claims.Document
	created    bool
	verdicts   []*claims.RejectionReason
}

func (f *fakeRecords) RegisterDocument(context.Context, claims.NewDocument) (claims.Document, bool, error) {
	return f.registered, f.created, nil
}

func (f *fakeRecords) Document(context.Context, string, uuid.UUID, uuid.UUID) (claims.Claim, claims.Document, error) {
	return f.claim, f.doc, nil
}

func (f *fakeRecords) RecordUpload(_ context.Context, _ string, _, _ uuid.UUID, reason *claims.RejectionReason) (claims.Document, error) {
	f.verdicts = append(f.verdicts, reason)
	return f.doc, nil
}

// fakeObjects stands in for Objects: HEAD answers size, GET answers body.
type fakeObjects struct {
	size     int64
	sizeErr  error
	body     []byte
	openErr  error
	reads    int
	presigns []string
}

func (f *fakeObjects) PresignPut(_ context.Context, key, contentType string, size int64, ttl time.Duration) (documents.Upload, error) {
	f.presigns = append(f.presigns, key+" "+contentType+" "+time.Duration(size).String()+" "+ttl.String())
	return documents.Upload{URL: "http://store/" + key}, nil
}

func (f *fakeObjects) Size(context.Context, string) (int64, error) {
	f.reads++
	return f.size, f.sizeErr
}

func (f *fakeObjects) Open(context.Context, string) (io.ReadCloser, error) {
	f.reads++
	if f.openErr != nil {
		return nil, f.openErr
	}
	return io.NopCloser(bytes.NewReader(f.body)), nil
}

var file = []byte("%PDF-1.4\n% SYNTHETIC TEST DOCUMENT\n")

func pending() claims.Document {
	sum := sha256.Sum256(file)
	return claims.Document{
		ID: uuid.MustParse("01930000-0000-7000-8000-0000000000d1"), Status: claims.DocumentPendingUpload,
		ContentType: "application/pdf", SizeBytes: int64(len(file)), SHA256: sum[:], ObjectKey: "quarantine/c/d",
	}
}

func draftClaim() claims.Claim { return claims.Claim{Status: claims.StatusDraft} }

func TestCompleteJudgesTheStoredObject(t *testing.T) {
	longer := append(append([]byte{}, file...), 'x')
	other := bytes.ToLower(file) // the same length, other bytes
	boom := errors.New("store unreachable")
	n := int64(len(file))
	tests := []struct {
		name    string
		objects fakeObjects
		want    claims.RejectionReason // "" for a match
		wantErr error
	}{
		{"a match", fakeObjects{size: n, body: file}, "", nil},
		{"no object", fakeObjects{sizeErr: documents.ErrNoObject}, claims.RejectMissingObject, nil},
		{"another size", fakeObjects{size: n + 1, body: longer}, claims.RejectSizeMismatch, nil},
		{"deleted after its size was read", fakeObjects{size: n, openErr: documents.ErrNoObject}, claims.RejectMissingObject, nil},
		{"longer when read", fakeObjects{size: n, body: longer}, claims.RejectSizeMismatch, nil},
		{"shorter when read", fakeObjects{size: n, body: file[:n-1]}, claims.RejectSizeMismatch, nil},
		{"other bytes", fakeObjects{size: n, body: other}, claims.RejectChecksumMismatch, nil},
		{"the store fails on HEAD", fakeObjects{sizeErr: boom}, "", boom},
		{"the store fails on GET", fakeObjects{size: n, openErr: boom}, "", boom},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			records := &fakeRecords{claim: draftClaim(), doc: pending()}
			objects := tc.objects
			svc := documents.NewService(records, &objects)
			_, err := svc.Complete(context.Background(), "alice", uuid.Nil, records.doc.ID)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
				if len(records.verdicts) != 0 {
					t.Fatal("a store failure was recorded as a verdict; the file must stay pending_upload")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(records.verdicts) != 1 {
				t.Fatalf("%d verdicts recorded, want 1", len(records.verdicts))
			}
			got := records.verdicts[0]
			switch {
			case tc.want == "" && got != nil:
				t.Errorf("verdict = %s, want a match", *got)
			case tc.want != "" && (got == nil || *got != tc.want):
				t.Errorf("verdict = %v, want %s", got, tc.want)
			}
		})
	}
}

func TestCompleteLeavesTheStoreAloneWhenThereIsNothingToJudge(t *testing.T) {
	uploaded := pending()
	uploaded.Status = claims.DocumentUploaded
	tests := []struct {
		name    string
		claim   claims.Claim
		doc     claims.Document
		wantErr error
	}{
		{"a file already uploaded", draftClaim(), uploaded, nil},
		{"a claim already submitted", claims.Claim{Status: claims.StatusSubmitted}, pending(), claims.ErrNotAcceptingDocuments},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			records := &fakeRecords{claim: tc.claim, doc: tc.doc}
			objects := &fakeObjects{}
			got, err := documents.NewService(records, objects).Complete(context.Background(), "alice", uuid.Nil, tc.doc.ID)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if err == nil && got.Status != tc.doc.Status {
				t.Errorf("status = %s, want %s", got.Status, tc.doc.Status)
			}
			if objects.reads != 0 || len(records.verdicts) != 0 {
				t.Errorf("%d store reads and %d verdicts, want none", objects.reads, len(records.verdicts))
			}
		})
	}
}

func TestRegisterSignsTheRegisteredFilesUpload(t *testing.T) {
	records := &fakeRecords{registered: pending(), created: true}
	objects := &fakeObjects{}
	res, err := documents.NewService(records, objects).Register(context.Background(), claims.NewDocument{})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Created || res.Document.ID != pending().ID || res.Upload.URL != "http://store/quarantine/c/d" {
		t.Errorf("Register = %+v", res)
	}
	want := "quarantine/c/d application/pdf " + time.Duration(len(file)).String() + " 15m0s"
	if len(objects.presigns) != 1 || objects.presigns[0] != want {
		t.Errorf("presigned %q, want [%q]", objects.presigns, want)
	}
}
