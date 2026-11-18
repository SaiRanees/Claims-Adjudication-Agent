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

// Year, Month and Day return the date's parts.
func (d Date) Year() int         { return d.year }
func (d Date) Month() time.Month { return d.month }
func (d Date) Day() int          { return d.day }

// DaysUntil returns the number of days from d to e: 1 for the next day,
// negative when e is before d. Both must be dates, not the zero Date. A stay
// from d to e inclusive has d.DaysUntil(e) + 1 calendar days.
func (d Date) DaysUntil(e Date) int { return e.dayNumber() - d.dayNumber() }

// The largest shifts that can stay inside years 1 to 9999; bounding them
// first keeps the arithmetic below from overflowing.
const (
	maxDayShift   = 10_000 * 366
	maxMonthShift = 10_000 * 12
)

// AddDays returns the date n days after d, or before it when n is negative.
// It fails when d is the zero Date or the result falls outside years 1 to
// 9999.
func (d Date) AddDays(n int) (Date, error) {
	if d.IsZero() {
		return Date{}, fmt.Errorf("%w: the zero Date", ErrInvalidDate)
	}
	if n > maxDayShift || n < -maxDayShift {
		return Date{}, fmt.Errorf("%w: %s plus %d days", ErrInvalidDate, d, n)
	}
	y, m, day := civil(d.dayNumber() + n)
	return NewDate(y, m, day)
}

// AddMonths returns the date n calendar months after d, or before it when n
// is negative. The rule for month ends: when the target month is shorter, the
// result is its last day, so 31 January plus one month is 28 February, or 29
// February in a leap year, and 29 February plus 12 months is 28 February. It
// fails when d is the zero Date or the result falls outside years 1 to 9999.
func (d Date) AddMonths(n int) (Date, error) {
	if d.IsZero() {
		return Date{}, fmt.Errorf("%w: the zero Date", ErrInvalidDate)
	}
	if n > maxMonthShift || n < -maxMonthShift {
		return Date{}, fmt.Errorf("%w: %s plus %d months", ErrInvalidDate, d, n)
	}
	total := d.year*12 + int(d.month) - 1 + n
	y := floorDiv(total, 12)
	m := time.Month(total - y*12 + 1)
	if y < 1 || y > 9999 {
		return Date{}, fmt.Errorf("%w: %s plus %d months is outside years 1 to 9999", ErrInvalidDate, d, n)
	}
	return NewDate(y, m, min(d.day, daysIn(y, m)))
}

func floorDiv(a, b int) int {
	q := a / b
	if (a%b != 0) && ((a < 0) != (b < 0)) {
		q--
	}
	return q
}

func daysIn(y int, m time.Month) int {
	// Day 0 of the next month is the last day of this one.
	return time.Date(y, m+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

// dayNumber counts days from 1970-01-01, which is day 0. It is Howard
// Hinnant's days_from_civil, in integers, so it holds for every year from 1
// to 9999 where a time.Duration would overflow.
func (d Date) dayNumber() int {
	y, m := d.year, int(d.month)
	if m <= 2 {
		y-- // Count the year from March, so a leap day falls at its end.
	}
	era := floorDiv(y, 400)
	yoe := y - era*400 // [0, 399]
	mp := (m + 9) % 12 // March is 0
	doy := (153*mp+2)/5 + d.day - 1
	doe := yoe*365 + yoe/4 - yoe/100 + doy // [0, 146096]
	return era*146_097 + doe - 719_468
}

// civil is the inverse of dayNumber: Hinnant's civil_from_days.
func civil(z int) (int, time.Month, int) {
	z += 719_468
	era := floorDiv(z, 146_097)
	doe := z - era*146_097                                    // [0, 146096]
	yoe := (doe - doe/1_460 + doe/36_524 - doe/146_096) / 365 // [0, 399]
	y := yoe + era*400
	doy := doe - (365*yoe + yoe/4 - yoe/100) // [0, 365]
	mp := (5*doy + 2) / 153                  // [0, 11], March is 0
	day := doy - (153*mp+2)/5 + 1
	m := (mp+2)%12 + 1
	if m <= 2 {
		y++
	}
	return y, time.Month(m), day
}
