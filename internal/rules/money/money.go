package money

import (
	"errors"
	"math"
	"math/bits"
	"slices"
)

// Paise is an amount of money in paise (1 rupee is 100 paise).
type Paise int64

// BasisPoints is a rate in hundredths of a percent: 5% is 500 and 100% is
// FullRate.
type BasisPoints int64

// FullRate is 100% in basis points.
const FullRate BasisPoints = 10_000

var (
	// ErrNegativeAmount means an amount below zero was passed in.
	ErrNegativeAmount = errors.New("money: negative amount")
	// ErrNegativeRate means a rate below zero was passed in.
	ErrNegativeRate = errors.New("money: negative rate")
	// ErrNegativeWeight means an allocation weight below zero was passed in.
	ErrNegativeWeight = errors.New("money: negative weight")
	// ErrNoWeight means the weights were empty or all zero, so there is no
	// proportion to split by.
	ErrNoWeight = errors.New("money: no weight to allocate by")
	// ErrOverflow means the result, or the sum of the weights, does not fit in
	// an int64.
	ErrOverflow = errors.New("money: overflows int64")
)

// Percent returns amount × rate / 100%, rounded half-up to the paisa. It is
// exact for every non-negative amount and rate, and fails only when the result
// does not fit in an int64.
func Percent(amount Paise, rate BasisPoints) (Paise, error) {
	if amount < 0 {
		return 0, ErrNegativeAmount
	}
	if rate < 0 {
		return 0, ErrNegativeRate
	}
	hi, lo := bits.Mul64(uint64(amount), uint64(rate))
	if hi >= uint64(FullRate) {
		// The quotient would need more than 64 bits.
		return 0, ErrOverflow
	}
	q, r := bits.Div64(hi, lo, uint64(FullRate))
	if 2*r >= uint64(FullRate) {
		q++ // q < 2^64 / FullRate here, so this can't wrap.
	}
	if q > math.MaxInt64 {
		return 0, ErrOverflow
	}
	return Paise(q), nil
}

// Allocate splits total into parts proportional to weights by the
// largest-remainder method. The parts always add up to total, each part is
// less than one paisa from its exact share, and a zero weight gets nothing.
// Ties between equal remainders go to the earlier index, so the result is
// deterministic.
//
// Each share is computed in 128 bits, so any total and weights work as long
// as the weights add up to no more than math.MaxInt64.
func Allocate(total Paise, weights []int64) ([]Paise, error) {
	if total < 0 {
		return nil, ErrNegativeAmount
	}
	// Reject every negative weight before summing, so the error doesn't
	// depend on where in the list the bad weight sits.
	ws := make([]uint64, len(weights))
	for i, w := range weights {
		if w < 0 {
			return nil, ErrNegativeWeight
		}
		ws[i] = uint64(w)
	}
	var sum uint64
	for _, w := range ws {
		sum += w // Each term is below 2^63, so one step can't wrap.
		if sum > math.MaxInt64 {
			return nil, ErrOverflow
		}
	}
	if sum == 0 {
		return nil, ErrNoWeight
	}

	parts := make([]Paise, len(ws))
	rems := make([]uint64, len(ws))
	var floored uint64
	for i, w := range ws {
		// total × w / sum ≤ total < 2^63, so hi < sum and Div64 can't panic.
		hi, lo := bits.Mul64(uint64(total), w)
		q, r := bits.Div64(hi, lo, sum)
		parts[i] = Paise(q) //nolint:gosec // q ≤ total ≤ MaxInt64 because w ≤ sum.
		rems[i] = r
		floored += q
	}

	// The paise lost to flooring number fewer than the lines with a non-zero
	// remainder, so they never reach a line whose share was exact.
	left := uint64(total) - floored
	order := make([]int, len(weights))
	for i := range order {
		order[i] = i
	}
	slices.SortStableFunc(order, func(a, b int) int {
		switch {
		case rems[a] > rems[b]:
			return -1
		case rems[a] < rems[b]:
			return 1
		}
		return 0
	})
	for _, i := range order[:left] {
		parts[i]++
	}
	return parts, nil
}
