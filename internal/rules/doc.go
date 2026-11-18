// Package rules is the adjudication engine. Given the facts of a claim and a
// rule pack, it computes each line item's payable amount and the adjustments
// that explain it, each citing a clause of the policy wording.
//
// The engine is pure: no I/O, clock or randomness, so the same inputs always
// give the same result and any decision can be replayed (ADR-0005).
// .golangci.yml enforces this with depguard and forbidigo.
//
// A rule pack encodes one policy wording: its limits, the clauses they cite
// and the order of the steps. ParsePack reads a pack strictly from bytes; the
// rulepacks package embeds the files and hands them over. A Registry picks
// the pack for a claim through its policy period.
package rules
