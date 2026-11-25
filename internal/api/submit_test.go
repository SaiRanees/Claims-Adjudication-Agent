package api_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/api"
	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/claims"
	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/platform"
)

func submitCall(t *testing.T, role platform.Role, subject string) call {
	t.Helper()
	return call{method: http.MethodPost, path: "/v1/claims/" + newClaimID.String() + "/submit",
		auth: "Bearer " + token(t, role, subject)}
}

func TestSubmitClaim(t *testing.T) {
	tests := []struct {
		name      string
		role      platform.Role
		subject   string
		submitErr error
		status    int
		code      string
	}{
		{"its submitter", platform.RoleSubmitter, "alice", nil, http.StatusAccepted, ""},
		{"another submitter", platform.RoleSubmitter, "bob", nil, http.StatusNotFound, "not_found"},
		{"a reviewer", platform.RoleReviewer, "rev-1", nil, http.StatusForbidden, "role_not_allowed"},
		{"checks pending", platform.RoleSubmitter, "alice", claims.ErrChecksPending, http.StatusConflict, "document_checks_pending"},
		{"nothing accepted", platform.RoleSubmitter, "alice", claims.ErrNoAcceptedDocuments, http.StatusConflict, "no_accepted_documents"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, _ := newAPI(t, &fakeClaims{claim: storedClaim("alice"), submitErr: tt.submitErr})
			rec := do(h, submitCall(t, tt.role, tt.subject))
			if tt.code != "" {
				problemOf(t, rec, tt.status, tt.code)
				return
			}
			if rec.Code != tt.status {
				t.Fatalf("status = %d, want %d; body %s", rec.Code, tt.status, rec.Body)
			}
		})
	}
}

// Submitting shares the per-caller limit with registering files.
func TestSubmitClaimIsRateLimited(t *testing.T) {
	h, _ := newAPIWith(t, &fakeClaims{claim: storedClaim("alice")}, &fakeDocuments{},
		api.NewRateLimiter(1, time.Hour, time.Now))
	if rec := do(h, submitCall(t, platform.RoleSubmitter, "alice")); rec.Code != http.StatusAccepted {
		t.Fatalf("first submit: %d %s", rec.Code, rec.Body)
	}
	rec := do(h, submitCall(t, platform.RoleSubmitter, "alice"))
	problemOf(t, rec, http.StatusTooManyRequests, "rate_limited")
	if rec.Header().Get("Retry-After") == "" {
		t.Error("no Retry-After")
	}
}
