package money

import (
	"errors"
	"math"
	"math/bits"
)

var (
	// ErrNegativeResult means a subtraction would go below zero.
	ErrNegativeResult = errors.New("money: negative result")
	// ErrNegativeRatio means a ratio's numerator or denominator is below
	// zero.
	ErrNegativeRatio = errors.New("money: negative ratio")
	// ErrZeroDivisor means a ratio's denominator is zero.
	ErrZeroDivisor = errors.New("money: zero divisor")
)

// Add returns a + b. Both must be at least zero, and the sum must fit in an
// int64.
func Add(a, b Paise) (Paise, error) {
	if a < 0 || b < 0 {
		return 0, ErrNegativeAmount
	}
	if a > math.MaxInt64-b {
		return 0, ErrOverflow
	}
	return a + b, nil
}

// Sum returns the total of amounts, zero for none. Every amount must be at
// least zero, and every partial sum must fit in an int64, which for
// non-negative amounts is the same as the total fitting.
func Sum(amounts ...Paise) (Paise, error) {
	// Reject every negative amount first, so the error doesn't depend on
	// where in the list the bad amount sits.
	for _, a := range amounts {
		if a < 0 {
			return 0, ErrNegativeAmount
		}
	}
	var total Paise
	for _, a := range amounts {
		var err error
		if total, err = Add(total, a); err != nil {
			return 0, err
		}
	}
	return total, nil
}

// Sub returns a − b. Both must be at least zero, and b must not exceed a.
func Sub(a, b Paise) (Paise, error) {
	if a < 0 || b < 0 {
		return 0, ErrNegativeAmount
	}
	if b > a {
		return 0, ErrNegativeResult
	}
	return a - b, nil
}

// MulDiv returns amount × num ÷ den, rounded half-up to the paisa. The product
// is held in 128 bits and divided once, so a ratio such as 5,000 ÷ 8,000 is
// never rounded on its own first. It fails for a negative input, a zero
// denominator, or a result that doesn't fit in an int64; it never panics.
func MulDiv(amount Paise, num, den int64) (Paise, error) {
	switch {
	case amount < 0:
		return 0, ErrNegativeAmount
	case num < 0 || den < 0:
		return 0, ErrNegativeRatio
	case den == 0:
		return 0, ErrZeroDivisor
	}
	hi, lo := bits.Mul64(uint64(amount), uint64(num))
	if hi >= uint64(den) {
		// The quotient would need more than 64 bits, and Div64 would panic.
		return 0, ErrOverflow
	}
	q, r := bits.Div64(hi, lo, uint64(den))
	// den < 2^63, so r < 2^63 and 2r can't wrap.
	if 2*r >= uint64(den) {
		if q >= math.MaxInt64 {
			return 0, ErrOverflow
		}
		q++
	}
	if q > math.MaxInt64 {
		return 0, ErrOverflow
	}
	return Paise(q), nil
}
