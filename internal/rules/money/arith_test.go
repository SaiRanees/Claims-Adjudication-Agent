package money_test

import (
	"errors"
	"math"
	"math/big"
	"testing"

	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/rules/money"
)

func TestAdd(t *testing.T) {
	tests := []struct {
		name    string
		a, b    money.Paise
		want    money.Paise
		wantErr error
	}{
		{name: "zeros", a: 0, b: 0, want: 0},
		{name: "small", a: 12_345, b: 655, want: 13_000},
		{name: "up to the maximum", a: maxP - 1, b: 1, want: maxP},
		{name: "one over the maximum", a: maxP, b: 1, wantErr: money.ErrOverflow},
		{name: "maximum twice", a: maxP, b: maxP, wantErr: money.ErrOverflow},
		{name: "negative first", a: -1, b: 5, wantErr: money.ErrNegativeAmount},
		{name: "negative second", a: 5, b: -1, wantErr: money.ErrNegativeAmount},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := money.Add(tt.a, tt.b)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Add(%d, %d) error = %v, want %v", tt.a, tt.b, err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("Add(%d, %d) = %d, want %d", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestSum(t *testing.T) {
	tests := []struct {
		name    string
		amounts []money.Paise
		want    money.Paise
		wantErr error
	}{
		{name: "none", amounts: nil, want: 0},
		{name: "one", amounts: []money.Paise{7}, want: 7},
		{name: "several", amounts: []money.Paise{3_200_000, 10_000_000, 3_000_000, 1_000_000, 200_000}, want: 17_400_000},
		{name: "exactly the maximum", amounts: []money.Paise{maxP - 2, 1, 1}, want: maxP},
		{name: "overflow", amounts: []money.Paise{maxP, 1, 0}, wantErr: money.ErrOverflow},
		// A negative amount is reported even when an earlier pair overflows.
		{name: "negative after an overflow", amounts: []money.Paise{maxP, maxP, -1}, wantErr: money.ErrNegativeAmount},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := money.Sum(tt.amounts...)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Sum(%v) error = %v, want %v", tt.amounts, err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("Sum(%v) = %d, want %d", tt.amounts, got, tt.want)
			}
		})
	}
}

func TestSub(t *testing.T) {
	tests := []struct {
		name    string
		a, b    money.Paise
		want    money.Paise
		wantErr error
	}{
		{name: "to zero", a: 500, b: 500, want: 0},
		{name: "small", a: 3_200_000, b: 1_200_000, want: 2_000_000},
		{name: "from the maximum", a: maxP, b: maxP - 1, want: 1},
		{name: "below zero", a: 499, b: 500, wantErr: money.ErrNegativeResult},
		{name: "negative first", a: -1, b: 0, wantErr: money.ErrNegativeAmount},
		{name: "negative second", a: 5, b: -1, wantErr: money.ErrNegativeAmount},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := money.Sub(tt.a, tt.b)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Sub(%d, %d) error = %v, want %v", tt.a, tt.b, err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("Sub(%d, %d) = %d, want %d", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestMulDiv(t *testing.T) {
	tests := []struct {
		name     string
		amount   money.Paise
		num, den int64
		want     money.Paise
		wantErr  error
	}{
		{name: "worked example ratio", amount: 10_000_000, num: 2_000_000, den: 3_200_000, want: 6_250_000},
		{name: "ratio of one", amount: 12_345, num: 7, den: 7, want: 12_345},
		{name: "zero amount", amount: 0, num: 5, den: 8, want: 0},
		{name: "zero numerator", amount: 12_345, num: 0, den: 8, want: 0},
		{name: "two thirds rounds up", amount: 1_000_000, num: 600_000, den: 900_000, want: 666_667},
		{name: "one third rounds down", amount: 1_000_000, num: 300_000, den: 900_000, want: 333_333},
		{name: "exactly half rounds up", amount: 1, num: 1, den: 2, want: 1},
		{name: "just under half rounds down", amount: 4_999, num: 1, den: 10_000, want: 0},
		{name: "ratio not rounded first", amount: 4_000_000, num: 1_000_000, den: 1_000_200, want: 3_999_200},
		{name: "128-bit product", amount: maxP, num: math.MaxInt64, den: math.MaxInt64, want: maxP},
		{name: "maximum halved rounds up", amount: maxP, num: 1, den: 2, want: 4_611_686_018_427_387_904},
		{name: "quotient over int64", amount: maxP, num: 2, den: 1, wantErr: money.ErrOverflow},
		{name: "quotient over 64 bits", amount: maxP, num: math.MaxInt64, den: 1, wantErr: money.ErrOverflow},
		// (2^32 − 1)(2^32 + 1) ÷ 2 is MaxInt64 + ½, which rounds up to 2^63.
		{name: "rounds up past int64", amount: 4_294_967_295, num: 4_294_967_297, den: 2, wantErr: money.ErrOverflow},
		{name: "half below the maximum", amount: 4_294_967_295, num: 4_294_967_297, den: 3, want: 6_148_914_691_236_517_205},
		{name: "zero divisor", amount: 5, num: 1, den: 0, wantErr: money.ErrZeroDivisor},
		{name: "zero over zero", amount: 0, num: 0, den: 0, wantErr: money.ErrZeroDivisor},
		{name: "negative amount", amount: -1, num: 1, den: 2, wantErr: money.ErrNegativeAmount},
		{name: "negative numerator", amount: 1, num: -1, den: 2, wantErr: money.ErrNegativeRatio},
		{name: "negative denominator", amount: 1, num: 1, den: -2, wantErr: money.ErrNegativeRatio},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := money.MulDiv(tt.amount, tt.num, tt.den)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("MulDiv(%d, %d, %d) error = %v, want %v", tt.amount, tt.num, tt.den, err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("MulDiv(%d, %d, %d) = %d, want %d", tt.amount, tt.num, tt.den, got, tt.want)
			}
		})
	}
}

func FuzzAdd(f *testing.F) {
	f.Add(int64(12_345), int64(655))
	f.Add(int64(math.MaxInt64), int64(1))
	f.Add(int64(-1), int64(1))
	f.Fuzz(func(t *testing.T, a, b int64) {
		got, err := money.Add(money.Paise(a), money.Paise(b))
		want := new(big.Int).Add(big.NewInt(a), big.NewInt(b))
		var wantErr error
		switch {
		case a < 0 || b < 0:
			wantErr = money.ErrNegativeAmount
		case !want.IsInt64():
			wantErr = money.ErrOverflow
		}
		if !errors.Is(err, wantErr) {
			t.Fatalf("Add(%d, %d) error = %v, want %v", a, b, err, wantErr)
		}
		if wantErr == nil && int64(got) != want.Int64() {
			t.Fatalf("Add(%d, %d) = %d, want %s", a, b, got, want)
		}
	})
}

func FuzzSum(f *testing.F) {
	f.Add(int64(1), int64(2), int64(3), int64(4), uint8(4))
	f.Add(int64(math.MaxInt64), int64(1), int64(0), int64(0), uint8(2))
	f.Add(int64(math.MaxInt64), int64(math.MaxInt64), int64(-1), int64(0), uint8(3))
	f.Fuzz(func(t *testing.T, a0, a1, a2, a3 int64, n uint8) {
		raw := []int64{a0, a1, a2, a3}[:int(n%5)]
		amounts := make([]money.Paise, len(raw))
		want := new(big.Int)
		var wantErr error
		for i, a := range raw {
			amounts[i] = money.Paise(a)
			if a < 0 {
				wantErr = money.ErrNegativeAmount
			}
			want.Add(want, big.NewInt(a))
		}
		if wantErr == nil && !want.IsInt64() {
			wantErr = money.ErrOverflow
		}
		got, err := money.Sum(amounts...)
		if !errors.Is(err, wantErr) {
			t.Fatalf("Sum(%v) error = %v, want %v", raw, err, wantErr)
		}
		if wantErr == nil && int64(got) != want.Int64() {
			t.Fatalf("Sum(%v) = %d, want %s", raw, got, want)
		}
	})
}

func FuzzSub(f *testing.F) {
	f.Add(int64(500), int64(499))
	f.Add(int64(499), int64(500))
	f.Add(int64(math.MaxInt64), int64(0))
	f.Add(int64(0), int64(-1))
	f.Fuzz(func(t *testing.T, a, b int64) {
		got, err := money.Sub(money.Paise(a), money.Paise(b))
		var wantErr error
		switch {
		case a < 0 || b < 0:
			wantErr = money.ErrNegativeAmount
		case b > a:
			wantErr = money.ErrNegativeResult
		}
		if !errors.Is(err, wantErr) {
			t.Fatalf("Sub(%d, %d) error = %v, want %v", a, b, err, wantErr)
		}
		want := new(big.Int).Sub(big.NewInt(a), big.NewInt(b))
		if wantErr == nil && int64(got) != want.Int64() {
			t.Fatalf("Sub(%d, %d) = %d, want %s", a, b, got, want)
		}
	})
}

func FuzzMulDiv(f *testing.F) {
	f.Add(int64(10_000_000), int64(2_000_000), int64(3_200_000))
	f.Add(int64(1), int64(1), int64(2))
	f.Add(int64(math.MaxInt64), int64(math.MaxInt64), int64(math.MaxInt64))
	f.Add(int64(math.MaxInt64), int64(math.MaxInt64), int64(1))
	f.Add(int64(5), int64(1), int64(0))
	f.Add(int64(-1), int64(1), int64(1))
	f.Fuzz(func(t *testing.T, amount, num, den int64) {
		got, err := money.MulDiv(money.Paise(amount), num, den)
		var wantErr error
		var want *big.Int
		switch {
		case amount < 0:
			wantErr = money.ErrNegativeAmount
		case num < 0 || den < 0:
			wantErr = money.ErrNegativeRatio
		case den == 0:
			wantErr = money.ErrZeroDivisor
		default:
			// Half-up for x ≥ 0: floor((2x + den) / 2den), x = amount × num.
			want = new(big.Int).Mul(big.NewInt(amount), big.NewInt(num))
			twoDen := new(big.Int).Lsh(big.NewInt(den), 1)
			want.Lsh(want, 1).Add(want, big.NewInt(den)).Quo(want, twoDen)
			if !want.IsInt64() {
				wantErr = money.ErrOverflow
			}
		}
		if !errors.Is(err, wantErr) {
			t.Fatalf("MulDiv(%d, %d, %d) error = %v, want %v", amount, num, den, err, wantErr)
		}
		if wantErr == nil && int64(got) != want.Int64() {
			t.Fatalf("MulDiv(%d, %d, %d) = %d, want %s", amount, num, den, got, want)
		}
	})
}
