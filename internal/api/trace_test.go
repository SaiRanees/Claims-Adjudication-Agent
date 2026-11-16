package api_test

import (
	"net/http"
	"testing"

	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/platform"
)

// A caller's W3C trace context, as a traceparent header carries it.
const (
	callerTraceID     = "4bf92f3577b34da6a3ce929d0e0e4736"
	callerTraceparent = "00-" + callerTraceID + "-00f067aa0ba902b7-01"
)

func TestProblemsCarryTheRequestsTraceID(t *testing.T) {
	h, _ := newAPI(t, &fakeClaims{})
	traced := map[string]string{"traceparent": callerTraceparent}
	tests := []struct {
		name   string
		call   call
		status int
		code   string
	}{
		{
			name:   "unknown path, refused by the contract",
			call:   call{method: http.MethodGet, path: "/v1/nothing-here", headers: traced},
			status: http.StatusNotFound,
			code:   "not_found",
		},
		{
			name:   "missing token, refused before validation",
			call:   call{method: http.MethodGet, path: "/v1/claims/01931111-2222-7333-8444-555555555555", headers: traced},
			status: http.StatusUnauthorized,
			code:   "missing_token",
		},
		{
			name: "invalid body, refused by validation",
			call: call{method: http.MethodPost, path: "/v1/claims", auth: "Bearer " + token(t, platform.RoleSubmitter, "member-1"),
				body: `{}`, headers: map[string]string{"traceparent": callerTraceparent, "Idempotency-Key": "k"}},
			status: http.StatusBadRequest,
			code:   "validation_failed",
		},
		{
			name: "wrong role, refused by the handler",
			call: call{method: http.MethodPost, path: "/v1/claims", auth: "Bearer " + token(t, platform.RoleReviewer, "staff-1"),
				body: validBody, headers: map[string]string{"traceparent": callerTraceparent, "Idempotency-Key": "k"}},
			status: http.StatusForbidden,
			code:   "role_not_allowed",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := problemOf(t, do(h, tt.call), tt.status, tt.code)
			if p.TraceID == nil || *p.TraceID != callerTraceID {
				t.Errorf("traceId = %v, want %s", p.TraceID, callerTraceID)
			}
		})
	}
}

func TestProblemsOmitTheTraceIDWithoutATrace(t *testing.T) {
	// The tests install no tracer provider, so a request without a traceparent has no trace.
	h, _ := newAPI(t, &fakeClaims{})
	p := problemOf(t, do(h, call{method: http.MethodGet, path: "/v1/nothing-here"}), http.StatusNotFound, "not_found")
	if p.TraceID != nil {
		t.Errorf("traceId = %q, want none", *p.TraceID)
	}
}
