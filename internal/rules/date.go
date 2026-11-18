package rules

import (
	"cmp"
	"errors"
	"fmt"
	"time"
)

// ErrInvalidDate means a date that doesn't exist on the calendar, such as
// 30 February, or text that isn't a date in the form YYYY-MM-DD.
var ErrInvalidDate = errors.New("rules: invalid date")

// Date is a calendar date with no time of day and no time zone. The zero Date
// is not a date; build one with NewDate or ParseDate.
type Date struct {
	year  int
	month time.Month
	day   int
}

// NewDate returns the date year-month-day. It fails for a day the calendar
// doesn't have, and for a year outside 1 to 9999.
func NewDate(year int, month time.Month, day int) (Date, error) {
	if year < 1 || year > 9999 {
		return Date{}, fmt.Errorf("%w: year %d is outside 1 to 9999", ErrInvalidDate, year)
	}
	// time.Date normalises out-of-range values (13 January becomes January of
	// the next year), so a date exists only if it comes back unchanged.
	t := time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
	if t.Year() != year || t.Month() != month || t.Day() != day {
		return Date{}, fmt.Errorf("%w: %04d-%02d-%02d", ErrInvalidDate, year, int(month), day)
	}
	return Date{year: year, month: month, day: day}, nil
}

// ParseDate parses exactly YYYY-MM-DD: four, two and two digits, separated by
// hyphens.
func ParseDate(s string) (Date, error) {
	if len(s) != len("2006-01-02") || s[4] != '-' || s[7] != '-' {
		return Date{}, fmt.Errorf("%w: %q is not YYYY-MM-DD", ErrInvalidDate, s)
	}
	year, ok1 := digits(s[0:4])
	month, ok2 := digits(s[5:7])
	day, ok3 := digits(s[8:10])
	if !ok1 || !ok2 || !ok3 {
		return Date{}, fmt.Errorf("%w: %q is not YYYY-MM-DD", ErrInvalidDate, s)
	}
	return NewDate(year, time.Month(month), day)
}

// digits reads a short run of ASCII digits, rejecting anything else.
func digits(s string) (int, bool) {
	n := 0
	for i := range len(s) {
		c := s[i]
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int(c-'0')
	}
	return n, true
}

// IsZero reports whether d is the zero Date, which is not a date.
func (d Date) IsZero() bool { return d == Date{} }

// Compare returns -1 if d is before e, 0 if they're the same day, and +1 if
// d is after e.
func (d Date) Compare(e Date) int {
	if c := cmp.Compare(d.year, e.year); c != 0 {
		return c
	}
	if c := cmp.Compare(d.month, e.month); c != 0 {
		return c
	}
	return cmp.Compare(d.day, e.day)
}

// Before reports whether d is before e.
func (d Date) Before(e Date) bool { return d.Compare(e) < 0 }

// After reports whether d is after e.
func (d Date) After(e Date) bool { return d.Compare(e) > 0 }

// String formats d as YYYY-MM-DD.
func (d Date) String() string {
	return fmt.Sprintf("%04d-%02d-%02d", d.year, int(d.month), d.day)
}
