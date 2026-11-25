package datagen

import (
	"fmt"
	"strings"

	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/rules"
)

// Grouping is how an amount's rupees are grouped.
type Grouping int

// The groupings.
const (
	// GroupIndian groups the last three digits, then pairs: 1,23,456.00.
	GroupIndian Grouping = iota
	// GroupWestern groups in threes: 123,456.00.
	GroupWestern
)

// FormatAmount prints paise as rupees with two decimals, grouped. Amounts
// are never negative (validation and the engine both require it).
func FormatAmount(p int64, g Grouping) string {
	rupees, paise := p/100, p%100
	digits := itoa(rupees)
	var groups []string
	switch g {
	case GroupWestern:
		for len(digits) > 3 {
			groups = append([]string{digits[len(digits)-3:]}, groups...)
			digits = digits[:len(digits)-3]
		}
	case GroupIndian:
		if len(digits) > 3 {
			groups = []string{digits[len(digits)-3:]}
			digits = digits[:len(digits)-3]
			for len(digits) > 2 {
				groups = append([]string{digits[len(digits)-2:]}, groups...)
				digits = digits[:len(digits)-2]
			}
		}
	}
	groups = append([]string{digits}, groups...)
	return fmt.Sprintf("%s.%02d", strings.Join(groups, ","), paise)
}

// DateStyle is how a date is printed.
type DateStyle int

// The date styles.
const (
	// DateSlashes prints 22/09/2025.
	DateSlashes DateStyle = iota
	// DateMonthName prints 22-Sep-2025.
	DateMonthName
)

// FormatDate prints a date in a style.
func FormatDate(d rules.Date, s DateStyle) string {
	switch s {
	case DateMonthName:
		return fmt.Sprintf("%02d-%s-%04d", d.Day(), d.Month().String()[:3], d.Year())
	case DateSlashes:
	}
	return fmt.Sprintf("%02d/%02d/%04d", d.Day(), int(d.Month()), d.Year())
}

// Style is how one document prints its values.
type Style struct {
	Grouping Grouping
	Dates    DateStyle
	// Mark is the currency mark before a total: "₹", "Rs. " or none.
	Mark string
	// Columns are the line table's columns, left to right.
	Columns []Column
}

// Money prints an amount with the style's grouping and no mark.
func (s Style) Money(p int64) string { return FormatAmount(p, s.Grouping) }

// Total prints an amount with the style's mark.
func (s Style) Total(p int64) string { return s.Mark + s.Money(p) }

// Column is one column of a bill's line table.
type Column struct {
	Field ColumnField
	Title string
	// X is the column's left edge and Width its width, in points.
	X, Width float64
	// Right aligns the column's values to its right edge.
	Right bool
}

// ColumnField is what a column shows.
type ColumnField string

// The column fields.
const (
	FieldSerial      ColumnField = "serial"
	FieldDate        ColumnField = "date"
	FieldDescription ColumnField = "description"
	FieldQuantity    ColumnField = "quantity"
	FieldUnitPrice   ColumnField = "unit_price"
	FieldAmount      ColumnField = "amount"
)

// The page is A4 in points, with 40-point margins.
const (
	pageWidth   = 595.28
	pageHeight  = 841.89
	marginLeft  = 40.0
	marginRight = pageWidth - 40.0
)

// styles: the three hospital-bill layouts and the pharmacy bill differ in
// column order, date style, digit grouping and currency mark.
var styles = map[Layout]Style{
	LayoutLedger: {Grouping: GroupIndian, Dates: DateSlashes, Mark: "₹", Columns: []Column{
		{FieldDate, "Date", 40, 62, false},
		{FieldDescription, "Particulars", 106, 247, false},
		{FieldQuantity, "Qty", 357, 34, true},
		{FieldUnitPrice, "Rate (₹)", 395, 75, true},
		{FieldAmount, "Amount (₹)", 474, 81, true},
	}},
	LayoutColumnar: {Grouping: GroupWestern, Dates: DateMonthName, Mark: "Rs. ", Columns: []Column{
		{FieldSerial, "S.No", 40, 28, false},
		{FieldDescription, "Service Description", 72, 220, false},
		{FieldDate, "Service Date", 296, 66, false},
		{FieldUnitPrice, "Unit Rate (Rs.)", 366, 78, true},
		{FieldQuantity, "Units", 448, 30, true},
		{FieldAmount, "Total (Rs.)", 482, 73, true},
	}},
	LayoutCompact: {Grouping: GroupIndian, Dates: DateMonthName, Mark: "", Columns: []Column{
		{FieldDescription, "Item", 40, 238, false},
		{FieldQuantity, "Nos", 282, 30, true},
		{FieldUnitPrice, "Price", 316, 72, true},
		{FieldDate, "On", 400, 66, false},
		{FieldAmount, "Value", 470, 85, true},
	}},
}

// pharmacyStyle is the pharmacy bill's one layout.
var pharmacyStyle = Style{Grouping: GroupIndian, Dates: DateSlashes, Mark: "₹", Columns: []Column{
	{FieldSerial, "#", 40, 22, false},
	{FieldDescription, "Item", 66, 226, false},
	{FieldDate, "Date", 296, 62, false},
	{FieldQuantity, "Qty", 362, 34, true},
	{FieldUnitPrice, "MRP (₹)", 400, 70, true},
	{FieldAmount, "Amount (₹)", 474, 81, true},
}}

// receiptStyle prints a receipt's amount.
var receiptStyle = Style{Grouping: GroupIndian, Dates: DateSlashes, Mark: "₹"}

// StyleOf returns a document's style.
func StyleOf(d Document) Style {
	switch d.Type {
	case DocHospitalBill:
		return styles[d.Layout]
	case DocPharmacyBill:
		return pharmacyStyle
	case DocPaymentReceipt:
	}
	return receiptStyle
}
