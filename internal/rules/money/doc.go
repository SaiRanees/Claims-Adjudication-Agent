// Package money does rupee arithmetic in int64 paise, with percentages as
// integer basis points (5% is 500). It never uses floating point. Rounding is
// half-up to the paisa, and splitting an amount uses the largest-remainder
// method, so the parts always add up to the whole (ADR-0005).
package money
