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
	if _, err := (rules.Date{}).AddDays(1); !errors.Is(err, rules.ErrInvalidDate) {
		t.Errorf("zero Date AddDays error = %v, want ErrInvalidDate", err)
	}
	if _, err := (rules.Date{}).AddMonths(1); !errors.Is(err, rules.ErrInvalidDate) {
		t.Errorf("zero Date AddMonths error = %v, want ErrInvalidDate", err)
	}
}

func TestDaysUntil(t *testing.T) {
	tests := []struct {
		from, to string
		want     int
	}{
		{from: "2025-06-10", to: "2025-06-10", want: 0},
		{from: "2025-06-10", to: "2025-06-14", want: 4},
		{from: "2025-06-14", to: "2025-06-10", want: -4},
		{from: "2024-02-28", to: "2024-03-01", want: 2}, // leap year
		{from: "2023-02-28", to: "2023-03-01", want: 1},
		{from: "2100-02-28", to: "2100-03-01", want: 1}, // not a leap year
		{from: "2000-02-28", to: "2000-03-01", want: 2}, // leap year
		{from: "2023-04-01", to: "2024-04-01", want: 366},
		{from: "2024-04-01", to: "2025-04-01", want: 365},
		{from: "1970-01-01", to: "1970-01-02", want: 1},
		{from: "0001-01-01", to: "9999-12-31", want: 3_652_058},
	}
	for _, tt := range tests {
		t.Run(tt.from+" to "+tt.to, func(t *testing.T) {
			if got := date(t, tt.from).DaysUntil(date(t, tt.to)); got != tt.want {
				t.Errorf("DaysUntil = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestAddDays(t *testing.T) {
	tests := []struct {
		from    string
		n       int
		want    string
		wantErr bool
	}{
		{from: "2025-06-10", n: 0, want: "2025-06-10"},
		{from: "2025-06-10", n: 4, want: "2025-06-14"},
		{from: "2024-02-28", n: 1, want: "2024-02-29"},
		{from: "2023-02-28", n: 1, want: "2023-03-01"},
		{from: "2024-12-31", n: 1, want: "2025-01-01"},
		{from: "2025-01-01", n: -1, want: "2024-12-31"},
		{from: "2024-03-01", n: -1, want: "2024-02-29"},
		{from: "2023-04-01", n: 30, want: "2023-05-01"},
		{from: "9999-12-31", n: 1, wantErr: true},
		{from: "0001-01-01", n: -1, wantErr: true},
		{from: "2025-01-01", n: 1 << 40, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.from, func(t *testing.T) {
			got, err := date(t, tt.from).AddDays(tt.n)
			if tt.wantErr {
				if !errors.Is(err, rules.ErrInvalidDate) {
					t.Fatalf("AddDays(%d) = %s, %v; want ErrInvalidDate", tt.n, got, err)
				}
				return
			}
			if err != nil || got.String() != tt.want {
				t.Errorf("%s.AddDays(%d) = %s, %v; want %s", tt.from, tt.n, got, err, tt.want)
			}
		})
	}
}

func TestAddMonths(t *testing.T) {
	tests := []struct {
		from    string
		n       int
		want    string
		wantErr bool
	}{
		{from: "2025-06-10", n: 1, want: "2025-07-10"},
		{from: "2025-06-10", n: 0, want: "2025-06-10"},
		// Month ends: the result is the last day of a shorter month.
		{from: "2025-01-31", n: 1, want: "2025-02-28"},
		{from: "2024-01-31", n: 1, want: "2024-02-29"}, // leap year
		{from: "2024-03-31", n: 1, want: "2024-04-30"},
		{from: "2024-02-29", n: 12, want: "2025-02-28"},
		{from: "2024-02-29", n: 48, want: "2028-02-29"},
		{from: "2024-02-29", n: 36, want: "2027-02-28"}, // a 36-month wait from a leap day
		{from: "2024-02-29", n: 24, want: "2026-02-28"},
		{from: "2100-01-31", n: 1, want: "2100-02-28"}, // not a leap year
		{from: "2025-12-15", n: 1, want: "2026-01-15"},
		{from: "2025-01-15", n: -1, want: "2024-12-15"},
		{from: "2025-03-31", n: -1, want: "2025-02-28"},
		{from: "2023-04-01", n: 24, want: "2025-04-01"},
		{from: "9999-12-01", n: 1, wantErr: true},
		{from: "0001-01-15", n: -1, wantErr: true},
		{from: "2025-01-01", n: 1 << 40, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.from, func(t *testing.T) {
			got, err := date(t, tt.from).AddMonths(tt.n)
			if tt.wantErr {
				if !errors.Is(err, rules.ErrInvalidDate) {
					t.Fatalf("AddMonths(%d) = %s, %v; want ErrInvalidDate", tt.n, got, err)
				}
				return
			}
			if err != nil || got.String() != tt.want {
				t.Errorf("%s.AddMonths(%d) = %s, %v; want %s", tt.from, tt.n, got, err, tt.want)
			}
		})
	}
}

// FuzzDate checks the day arithmetic against the time package, which uses
// a different algorithm, for every year from 1 to 9999.
func FuzzDate(f *testing.F) {
	f.Add(uint16(2024), uint8(2), uint8(29), int32(365), int16(12))
	f.Add(uint16(1), uint8(1), uint8(1), int32(3_652_058), int16(-1))
	f.Add(uint16(9999), uint8(12), uint8(31), int32(-3_652_058), int16(1))
	f.Add(uint16(2025), uint8(1), uint8(31), int32(1), int16(1))
	f.Fuzz(func(t *testing.T, y uint16, m, d uint8, n int32, months int16) {
		start, err := rules.NewDate(int(y), time.Month(m), int(d))
		if err != nil {
			return // not a date
		}
		ts := time.Date(int(y), time.Month(m), int(d), 0, 0, 0, 0, time.UTC)

		// AddDays agrees with AddDate, when the result is in range.
		want := ts.AddDate(0, 0, int(n))
		got, err := start.AddDays(int(n))
		inRange := want.Year() >= 1 && want.Year() <= 9999
		if inRange != (err == nil) {
			t.Fatalf("%s.AddDays(%d) error = %v, want in range %t", start, n, err, inRange)
		}
		if err == nil {
			if got.String() != want.Format("2006-01-02") {
				t.Fatalf("%s.AddDays(%d) = %s, want %s", start, n, got, want.Format("2006-01-02"))
			}
			// DaysUntil inverts AddDays, measured in whole days of Unix time.
			if days := start.DaysUntil(got); days != int(n) || int64(days) != (want.Unix()-ts.Unix())/86_400 {
				t.Fatalf("%s.DaysUntil(%s) = %d, want %d", start, got, days, n)
			}
		}

		// AddMonths keeps the day when the target month has it, and otherwise
		// gives the target month's last day.
		target := time.Date(int(y), time.Month(m)+time.Month(months), 1, 0, 0, 0, 0, time.UTC)
		got, err = start.AddMonths(int(months))
		inRange = target.Year() >= 1 && target.Year() <= 9999
		if inRange != (err == nil) {
			t.Fatalf("%s.AddMonths(%d) error = %v, want in range %t", start, months, err, inRange)
		}
		if err != nil {
			return
		}
		last := time.Date(target.Year(), target.Month()+1, 0, 0, 0, 0, 0, time.UTC).Day()
		if got.Year() != target.Year() || got.Month() != target.Month() || got.Day() != min(int(d), last) {
			t.Fatalf("%s.AddMonths(%d) = %s, want %04d-%02d-%02d",
				start, months, got, target.Year(), int(target.Month()), min(int(d), last))
		}
	})
}
