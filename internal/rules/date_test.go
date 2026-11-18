package rules_test

import (
	"errors"
	"testing"
	"time"

	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/rules"
)

func TestParseDate(t *testing.T) {
	tests := []struct {
		in      string
		wantErr bool
	}{
		{in: "2020-04-16"},
		{in: "2024-02-29"},                // leap year
		{in: "2000-02-29"},                // divisible by 400
		{in: "0001-01-01"},                // first year
		{in: "9999-12-31"},                // last year
		{in: "2023-02-29", wantErr: true}, // not a leap year
		{in: "1900-02-29", wantErr: true}, // divisible by 100, not 400
		{in: "0000-01-01", wantErr: true},
		{in: "2020-00-10", wantErr: true},
		{in: "2020-13-01", wantErr: true},
		{in: "2020-04-31", wantErr: true},
		{in: "2020-04-00", wantErr: true},
		{in: "2020-4-16", wantErr: true},
		{in: "2020/04/16", wantErr: true},
		{in: "+020-04-16", wantErr: true},
		{in: "2020-04-16 ", wantErr: true},
		{in: "2020-04-16T00:00:00Z", wantErr: true},
		{in: "２０２０-04-16", wantErr: true}, // full-width digits
		{in: "", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			d, err := rules.ParseDate(tt.in)
			if tt.wantErr {
				if !errors.Is(err, rules.ErrInvalidDate) {
					t.Fatalf("ParseDate(%q) error = %v, want ErrInvalidDate", tt.in, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseDate(%q) error = %v", tt.in, err)
			}
			if got := d.String(); got != tt.in {
				t.Errorf("ParseDate(%q).String() = %q", tt.in, got)
			}
		})
	}
}

func TestDateCompare(t *testing.T) {
	d := func(y int, m time.Month, day int) rules.Date {
		t.Helper()
		v, err := rules.NewDate(y, m, day)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	tests := []struct {
		name string
		a, b rules.Date
		want int
	}{
		{name: "same day", a: d(2024, 3, 1), b: d(2024, 3, 1), want: 0},
		{name: "day decides", a: d(2024, 3, 1), b: d(2024, 3, 2), want: -1},
		{name: "month beats day", a: d(2024, 2, 29), b: d(2024, 3, 1), want: -1},
		{name: "year beats month", a: d(2025, 1, 1), b: d(2024, 12, 31), want: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.a.Compare(tt.b); got != tt.want {
				t.Errorf("%s.Compare(%s) = %d, want %d", tt.a, tt.b, got, tt.want)
			}
			if got := tt.b.Compare(tt.a); got != -tt.want {
				t.Errorf("%s.Compare(%s) = %d, want %d", tt.b, tt.a, got, -tt.want)
			}
			if got := tt.a.Before(tt.b); got != (tt.want < 0) {
				t.Errorf("%s.Before(%s) = %t", tt.a, tt.b, got)
			}
			if got := tt.a.After(tt.b); got != (tt.want > 0) {
				t.Errorf("%s.After(%s) = %t", tt.a, tt.b, got)
			}
		})
	}
}

func TestDateZero(t *testing.T) {
	if !(rules.Date{}).IsZero() {
		t.Error("the zero Date isn't IsZero")
	}
	d, err := rules.NewDate(2020, time.April, 16)
	if err != nil {
		t.Fatal(err)
	}
	if d.IsZero() {
		t.Error("2020-04-16 is IsZero")
	}
}
