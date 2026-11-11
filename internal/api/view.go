package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/api/apigen"
	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/claims"
)

// renderCreated is the response to a create. The store keeps it under the idempotency key,
// so a retry gets the same body.
func renderCreated(c claims.Claim) (claims.Response, error) {
	body, err := json.Marshal(claimView(c, nil))
	if err != nil {
		return claims.Response{}, fmt.Errorf("encoding the claim: %w", err)
	}
	return claims.Response{Status: http.StatusCreated, Body: body}, nil
}

// requestHash is SHA-256 over the operation and the decoded request. Hashing the decoded
// request, re-encoded in the generated struct's fixed field order, means a retry that
// formats the same JSON differently still counts as the same request.
func requestHash(operation string, request any) ([32]byte, error) {
	canonical, err := json.Marshal(request)
	if err != nil {
		return [32]byte{}, fmt.Errorf("encoding the request for its hash: %w", err)
	}
	return sha256.Sum256(append([]byte(operation+"\n"), canonical...)), nil
}

func claimView(c claims.Claim, docs []claims.Document) apigen.Claim {
	view := apigen.Claim{
		ID:                 c.ID,
		MemberID:           c.MemberID,
		Status:             apigen.ClaimStatus(c.Status),
		AdmittedOn:         openapi_types.Date{Time: c.AdmittedOn},
		DischargedOn:       openapi_types.Date{Time: c.DischargedOn},
		HospitalName:       c.HospitalName,
		ClaimedAmountPaise: c.ClaimedAmountPaise,
		Version:            c.Version,
		Documents:          make([]apigen.Document, 0, len(docs)),
		Timeline:           timeline(c),
		CreatedAt:          c.CreatedAt.UTC(),
		UpdatedAt:          c.UpdatedAt.UTC(),
		SubmittedAt:        utc(c.SubmittedAt),
		DecidedAt:          utc(c.DecidedAt),
	}
	for _, d := range docs {
		view.Documents = append(view.Documents, documentView(d))
	}
	return view
}

// timeline shows when the claim was created and, once it has moved on, its current status.
// The status changes in between will come from the audit log, which records every one.
func timeline(c claims.Claim) []apigen.ClaimStatusEvent {
	events := []apigen.ClaimStatusEvent{{Status: apigen.ClaimStatusDraft, Version: 1, At: c.CreatedAt.UTC()}}
	if c.Status != claims.StatusDraft {
		events = append(events, apigen.ClaimStatusEvent{Status: apigen.ClaimStatus(c.Status), Version: c.Version, At: c.UpdatedAt.UTC()})
	}
	return events
}

func documentView(d claims.Document) apigen.Document {
	view := apigen.Document{
		ID:          d.ID,
		ClaimID:     d.ClaimID,
		Status:      apigen.DocumentStatus(d.Status),
		FileName:    d.FileName,
		ContentType: apigen.DocumentContentType(d.ContentType),
		SizeBytes:   d.SizeBytes,
		Sha256:      hex.EncodeToString(d.SHA256),
		PageCount:   d.PageCount,
		CreatedAt:   d.CreatedAt.UTC(),
		UploadedAt:  utc(d.UploadedAt),
	}
	if d.RejectionReason != nil {
		reason := apigen.DocumentRejectionReason(*d.RejectionReason)
		view.RejectionReason = &reason
	}
	return view
}

func utc(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}
