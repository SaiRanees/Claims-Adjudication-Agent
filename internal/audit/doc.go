// Package audit appends hash-chained events to audit_events and verifies the
// chains. Each event's hash covers the previous event's hash and the event's
// canonical content. The database enforces the links (no gaps, forks or
// dangling links); the verifier recomputes every hash and reports a break.
package audit
