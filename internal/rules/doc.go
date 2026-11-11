// Package rules is the adjudication engine. Given the facts of a claim and a
// rule pack, it computes each line item's payable amount and the adjustments
// that explain it, each citing a clause of the policy wording.
//
// The engine is pure: no I/O, clock or randomness, so the same inputs always
// give the same result and any decision can be replayed (ADR-0005).
// .golangci.yml enforces this with depguard and forbidigo.
package rules
