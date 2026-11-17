package money_test

import (
	"errors"
	"math"
	"math/big"
	"slices"
	"testing"

	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/rules/money"
)

const maxP = money.Paise(math.MaxInt64)

func TestPercent(t *testing.T) {
	tests := []struct {
		name    string
		amount  money.Paise
		rate    money.BasisPoints
		want    money.Paise
		wantErr error
	}{
		{name: "zero amount", amount: 0, rate: 500, want: 0},
		{name: "zero rate", amount: 12_345, rate: 0, want: 0},
		{name: "full rate", amount: 12_345, rate: money.FullRate, want: 12_345},
		{name: "5% exact", amount: 100_000, rate: 500, want: 5_000},
		{name: "below half rounds down", amount: 12_344, rate: 1_000, want: 1_234},
		{name: "exactly half rounds up", amount: 12_345, rate: 1_000, want: 1_235},
		{name: "1 bp just below half", amount: 4_999, rate: 1, want: 0},
		{name: "1 bp exactly half", amount: 5_000, rate: 1, want: 1},
		{name: "above 100%", amount: 1_000, rate: 15_000, want: 1_500},
		{name: "max amount at 100%", amount: maxP, rate: money.FullRate, want: maxP},
		{name: "max amount at 50% rounds up", amount: maxP, rate: 5_000, want: 4_611_686_018_427_387_904},
		{name: "max rate on one paisa", amount: 1, rate: math.MaxInt64, want: 922_337_203_685_478},
		{name: "max amount just over 100%", amount: maxP, rate: money.FullRate + 1, wantErr: money.ErrOverflow},
		{name: "128-bit quotient", amount: maxP, rate: math.MaxInt64, wantErr: money.ErrOverflow},
		{name: "negative amount", amount: -1, rate: 500, wantErr: money.ErrNegativeAmount},
		{name: "negative rate", amount: 100, rate: -1, wantErr: money.ErrNegativeRate},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := money.Percent(tt.amount, tt.rate)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Percent(%d, %d) error = %v, want %v", tt.amount, tt.rate, err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("Percent(%d, %d) = %d, want %d", tt.amount, tt.rate, got, tt.want)
			}
		})
	}
}

func TestAllocate(t *testing.T) {
	tests := []struct {
		name    string
		total   money.Paise
		weights []int64
		want    []money.Paise
		wantErr error
	}{
		{name: "even three ways", total: 100, weights: []int64{1, 1, 1}, want: []money.Paise{34, 33, 33}},
		{name: "zero weight gets nothing", total: 100, weights: []int64{1, 1, 1, 0}, want: []money.Paise{34, 33, 33, 0}},
		{name: "exact split", total: 10, weights: []int64{3, 7}, want: []money.Paise{3, 7}},
		{name: "zero total", total: 0, weights: []int64{3, 7}, want: []money.Paise{0, 0}},
		{name: "tie goes to earlier index", total: 1, weights: []int64{1, 1}, want: []money.Paise{1, 0}},
		{name: "two paise three ways", total: 2, weights: []int64{1, 1, 1}, want: []money.Paise{1, 1, 0}},
		{name: "largest remainder wins over index", total: 1, weights: []int64{1, 2}, want: []money.Paise{0, 1}},
		{name: "single weight takes all", total: 7, weights: []int64{0, 5, 0}, want: []money.Paise{0, 7, 0}},
		{name: "one line", total: 999, weights: []int64{42}, want: []money.Paise{999}},
		{
			// 10^15 × 2×10^9 overflows int64; the 128-bit product doesn't.
			name: "total × weight beyond int64", total: 1_000_000_000_000_000,
			weights: []int64{1_000_000_000, 2_000_000_000},
			want:    []money.Paise{333_333_333_333_333, 666_666_666_666_667},
		},
		{name: "max total", total: maxP, weights: []int64{math.MaxInt64 - 1, 1}, want: []money.Paise{maxP - 1, 1}},
		{name: "negative total", total: -1, weights: []int64{1}, wantErr: money.ErrNegativeAmount},
		{name: "negative weight", total: 1, weights: []int64{1, -1}, wantErr: money.ErrNegativeWeight},
		{name: "no weights", total: 1, weights: nil, wantErr: money.ErrNoWeight},
		{name: "all weights zero", total: 1, weights: []int64{0, 0}, wantErr: money.ErrNoWeight},
		{name: "weight sum overflows", total: 1, weights: []int64{math.MaxInt64, 1}, wantErr: money.ErrOverflow},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := money.Allocate(tt.total, tt.weights)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Allocate(%d, %v) error = %v, want %v", tt.total, tt.weights, err, tt.wantErr)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("Allocate(%d, %v) = %v, want %v", tt.total, tt.weights, got, tt.want)
			}
		})
	}
}

// FuzzPercent checks Percent against an exact math/big reference.
func FuzzPercent(f *testing.F) {
	f.Add(int64(12_345), int64(1_000))
	f.Add(int64(math.MaxInt64), int64(5_000))
	f.Add(int64(1), int64(math.MaxInt64))
	f.Add(int64(-1), int64(500))
	f.Fuzz(func(t *testing.T, amount, rate int64) {
		got, err := money.Percent(money.Paise(amount), money.BasisPoints(rate))

		var wantErr error
		var want *big.Int
		switch {
		case amount < 0:
			wantErr = money.ErrNegativeAmount
		case rate < 0:
			wantErr = money.ErrNegativeRate
		default:
			// Half-up for x ≥ 0: floor((2x + 10000) / 20000), x = amount × rate.
			want = new(big.Int).Mul(big.NewInt(amount), big.NewInt(rate))
			want.Lsh(want, 1).Add(want, big.NewInt(10_000)).Quo(want, big.NewInt(20_000))
			if !want.IsInt64() {
				wantErr = money.ErrOverflow
			}
		}
		if !errors.Is(err, wantErr) {
			t.Fatalf("Percent(%d, %d) error = %v, want %v", amount, rate, err, wantErr)
		}
		if wantErr == nil && int64(got) != want.Int64() {
			t.Fatalf("Percent(%d, %d) = %d, want %s", amount, rate, got, want)
		}
	})
}

// FuzzAllocate checks that the parts always add up to the total, none is
// negative, and each is strictly less than one paisa from its exact share
// (which also means a zero weight never gets a paisa).
func FuzzAllocate(f *testing.F) {
	f.Add(int64(100), int64(1), int64(1), int64(1), int64(0), int64(0), uint8(3))
	f.Add(int64(1_000_000_000_000_000), int64(1_000_000_000), int64(2_000_000_000), int64(0), int64(0), int64(0), uint8(2))
	f.Add(int64(math.MaxInt64), int64(math.MaxInt64-3), int64(1), int64(1), int64(1), int64(0), uint8(5))
	f.Add(int64(7), int64(0), int64(5), int64(0), int64(3), int64(0), uint8(5))
	f.Fuzz(func(t *testing.T, total, w0, w1, w2, w3, w4 int64, n uint8) {
		weights := []int64{w0, w1, w2, w3, w4}[:int(n%5)+1]
		parts, err := money.Allocate(money.Paise(total), weights)

		sum := new(big.Int)
		var wantErr error
		if total < 0 {
			wantErr = money.ErrNegativeAmount
		}
		for _, w := range weights {
			if w < 0 && wantErr == nil {
				wantErr = money.ErrNegativeWeight
			}
			sum.Add(sum, big.NewInt(w))
		}
		switch {
		case wantErr != nil:
		case !sum.IsInt64():
			wantErr = money.ErrOverflow
		case sum.Sign() == 0:
			wantErr = money.ErrNoWeight
		}
		if !errors.Is(err, wantErr) {
			t.Fatalf("Allocate(%d, %v) error = %v, want %v", total, weights, err, wantErr)
		}
		if err != nil {
			return
		}
		if len(parts) != len(weights) {
			t.Fatalf("Allocate(%d, %v) returned %d parts", total, weights, len(parts))
		}

		added := new(big.Int)
		for i, p := range parts {
			if p < 0 {
				t.Fatalf("Allocate(%d, %v) part %d = %d is negative", total, weights, i, p)
			}
			added.Add(added, big.NewInt(int64(p)))
			// |p - total×w/sum| < 1  ⇔  |p×sum - total×w| < sum.
			diff := new(big.Int).Mul(big.NewInt(int64(p)), sum)
			diff.Sub(diff, new(big.Int).Mul(big.NewInt(total), big.NewInt(weights[i])))
			if diff.Abs(diff).Cmp(sum) >= 0 {
				t.Fatalf("Allocate(%d, %v) part %d = %d is a paisa or more from its share", total, weights, i, p)
			}
		}
		if added.Cmp(big.NewInt(total)) != 0 {
			t.Fatalf("Allocate(%d, %v) = %v adds up to %s, want %d", total, weights, parts, added, total)
		}
	})
}
