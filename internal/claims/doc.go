// Package claims owns the claim lifecycle: creating drafts, submitting them, and
// the status changes the workflow drives. Every change that a workflow must hear
// about writes its outbox row in the same transaction (ADR-0003).
package claims
