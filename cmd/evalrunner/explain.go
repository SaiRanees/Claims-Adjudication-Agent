package main

import (
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/SaiRanees/Claims-Adjudication-Agent/evals/engine"
	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/rules"
	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/rules/money"
)

// explain writes a claim's result laid out like the worked example in
// docs/plan/architecture.md: each line's billed amount, the line-level rules
// applied to it and what they admit, then the subtotal, the claim-level
// steps and the payable. Each line's payable, the flags and the ledger
// follow. The tables are Markdown, so the output pastes into a PR.
//
// Every figure comes from the result; explain only adds and lays them out.
// It returns an error if the rows don't add up to the payable.
func explain(w io.Writer, c engine.Case, pack *rules.Pack, res rules.Result) error {
	stepOf := map[string]rules.StepName{}
	for _, s := range pack.Steps {
		for _, r := range s.Rules {
			stepOf[r.ID] = s.Name
		}
	}
	facts := map[string]rules.Line{}
	for _, l := range c.Facts.Lines {
		facts[l.ID] = l
	}
	lineAdjs := map[string][]rules.Adjustment{}
	claimShare := map[string]money.Paise{}
	claimTotal := map[string]money.Paise{}
	for _, a := range res.Adjustments { // in step order, then by line
		if claimLevel(stepOf[a.RuleID]) {
			claimShare[a.LineID] += a.Amount
			claimTotal[a.RuleID] += a.Amount
		} else {
			lineAdjs[a.LineID] = append(lineAdjs[a.LineID], a)
		}
	}
	exempt := map[rules.Category]bool{}
	if s, ok := pack.Step(rules.StepApplyProportionateDeduction); ok {
		for _, cat := range s.ExemptCategories {
			exempt[cat] = true
		}
	}

	fmt.Fprintf(w, "Case %s\n", c.ID)
	fmt.Fprintf(w, "Pack %s version %d, SHA-256 %x; engine %s\n", res.Pack.ID, res.Pack.Version, res.Pack.SHA256, res.Engine)
	fmt.Fprintf(w, "%s Stay %s to %s; differential billing: %s.\n\n",
		coverText(c.Cover), c.Facts.Admission, c.Facts.Discharge, c.Facts.DifferentialBilling)

	// The engine checks that the bill's total fits in int64, and every
	// figure below is at most a line's billed amount, so these sums fit.
	var billed, admissible, claimTotals money.Paise
	admitted := map[string]money.Paise{}
	var rows [][]string
	for _, l := range res.Lines {
		adm := l.Billed
		for _, a := range lineAdjs[l.ID] {
			adm -= a.Amount
		}
		admitted[l.ID] = adm
		billed += l.Billed
		admissible += adm
		desc, amount := lineText(facts[l.ID]), rupees(l.Billed)
		adjs := lineAdjs[l.ID]
		if len(adjs) == 0 {
			what := "Nothing deducted"
			if exempt[facts[l.ID].Category] {
				what = "Exempt from scaling"
			}
			rows = append(rows, []string{desc, amount, what, rupees(adm)})
			continue
		}
		for k, a := range adjs {
			r, _ := pack.Rule(a.RuleID)
			row := []string{"", "", ruleText(stepOf[a.RuleID], r, a), ""}
			if k == 0 {
				row[0], row[1] = desc, amount
			}
			if k == len(adjs)-1 {
				row[3] = rupees(adm)
			}
			rows = append(rows, row)
		}
	}
	rows = append(rows, []string{"**Subtotal**", "**" + rupees(billed) + "**", "", "**" + rupees(admissible) + "**"})
	for _, s := range pack.Steps {
		if !claimLevel(s.Name) {
			continue
		}
		for _, r := range s.Rules {
			if t := claimTotal[r.ID]; t > 0 {
				claimTotals += t
				rows = append(rows, []string{claimLabel(s.Name, r), "", ruleRef(r), "−" + rupees(t)})
			}
		}
	}
	rows = append(rows, []string{"**Payable**", "", "", "**" + rupees(res.Payable) + "**"})
	if admissible-claimTotals != res.Payable {
		return fmt.Errorf("%s: admissible %d less claim-level %d isn't the payable %d", c.ID, admissible, claimTotals, res.Payable)
	}
	table(w, []string{"Line", "Billed (₹)", "Rule applied", "Admissible (₹)"}, []bool{false, true, false, true}, rows)

	// Each line's share of the claim-level steps, which Allocate spreads by
	// what the lines admit.
	fmt.Fprintln(w, "\nEach line's payable, after its share of the claim-level steps:")
	fmt.Fprintln(w)
	rows = nil
	for _, l := range res.Lines {
		if admitted[l.ID]-claimShare[l.ID] != l.Payable {
			return fmt.Errorf("%s %s: admissible %d less its share %d isn't its payable %d",
				c.ID, l.ID, admitted[l.ID], claimShare[l.ID], l.Payable)
		}
		share := rupees(0)
		if claimShare[l.ID] > 0 {
			share = "−" + rupees(claimShare[l.ID])
		}
		rows = append(rows, []string{l.ID, rupees(admitted[l.ID]), share, rupees(l.Payable)})
	}
	table(w, []string{"Line", "Admissible (₹)", "Claim-level (₹)", "Payable (₹)"}, []bool{false, true, true, true}, rows)

	fmt.Fprintln(w)
	if len(res.Flags) == 0 {
		fmt.Fprintln(w, "Flags: none.")
	} else {
		fmt.Fprintln(w, "Flags:")
		for _, f := range res.Flags {
			s := "- " + string(f.Code)
			if f.LineID != "" {
				s += " on " + f.LineID
			}
			if f.HasFigure {
				s += ": ₹" + rupees(f.Figure)
			}
			fmt.Fprintln(w, s)
		}
	}
	fmt.Fprintln(w, ledgerText(res.Ledger))
	return nil
}

// claimLevel reports whether a step's rules apply to the claim as a whole,
// their reduction spread across the lines.
func claimLevel(s rules.StepName) bool {
	switch s {
	case rules.StepCapAtCoverLeft, rules.StepTakeCoPayment, rules.StepTakeBillDiscount:
		return true
	case rules.StepCheckEligibility, rules.StepDropListedItems, rules.StepSetAsideUnmodelledLines,
		rules.StepApplyDailyCaps, rules.StepApplyProportionateDeduction, rules.StepApplySubLimits:
		return false
	}
	return false
}

func claimLabel(s rules.StepName, r rules.Rule) string {
	switch s {
	case rules.StepCapAtCoverLeft:
		return "Capped at the cover left"
	case rules.StepTakeCoPayment:
		return "Co-payment, " + percentText(r.Rate)
	case rules.StepTakeBillDiscount:
		return "Bill discount"
	case rules.StepCheckEligibility, rules.StepDropListedItems, rules.StepSetAsideUnmodelledLines,
		rules.StepApplyDailyCaps, rules.StepApplyProportionateDeduction, rules.StepApplySubLimits:
	}
	return r.ID
}

// ruleText says what a line-level rule did to a line, citing it, and the
// amount it deducted.
func ruleText(s rules.StepName, r rules.Rule, a rules.Adjustment) string {
	in := func(name string) money.Paise {
		for _, i := range a.Inputs {
			if i.Name == name {
				return money.Paise(i.Value)
			}
		}
		return 0
	}
	var what string
	switch s {
	case rules.StepCheckEligibility:
		what = "Inside the waiting period"
	case rules.StepDropListedItems:
		what = "Listed item, not payable"
	case rules.StepSetAsideUnmodelledLines:
		what = "Not modelled, set aside for a person"
	case rules.StepApplyDailyCaps:
		days := int64(in("days"))
		unit := "days"
		if days == 1 {
			unit = "day"
		}
		what = fmt.Sprintf("Capped at ₹%s a day for %d %s", rupees(in("daily_cap")), days, unit)
	case rules.StepApplyProportionateDeduction:
		adm, billed := in("admissible"), in("billed")
		what = fmt.Sprintf("Scaled by ₹%s ÷ ₹%s", rupees(adm), rupees(billed))
		if bp, err := money.MulDiv(adm, int64(money.FullRate), int64(billed)); err == nil {
			what += " = " + percentText(money.BasisPoints(bp))
		}
	case rules.StepApplySubLimits:
		if used := in("used"); used > 0 {
			what = fmt.Sprintf("Limited to the ₹%s left of ₹%s, from ₹%s", rupees(in("left")), rupees(in("limit")), rupees(in("tied_total")))
		} else {
			what = fmt.Sprintf("Limited to ₹%s, from ₹%s", rupees(in("limit")), rupees(in("tied_total")))
		}
	case rules.StepCapAtCoverLeft, rules.StepTakeCoPayment, rules.StepTakeBillDiscount:
		what = claimLabel(s, r)
	}
	return fmt.Sprintf("%s (%s): −%s", what, ruleRef(r), rupees(a.Amount))
}

// ruleRef names a rule and the clause it cites.
func ruleRef(r rules.Rule) string {
	if r.Clause == "" {
		return r.ID
	}
	return r.ID + ", " + r.Clause
}

func lineText(l rules.Line) string {
	s := l.ID + " " + string(l.Category)
	if l.Quantity > 1 {
		s += fmt.Sprintf(", %d × %s", l.Quantity, rupees(l.UnitPrice))
	}
	switch l.Treatment.Kind {
	case rules.TreatmentCataract:
		s += ", cataract (" + string(l.Treatment.Eye) + " eye)"
	case rules.TreatmentModernTreatment:
		s += ", modern treatment"
	case rules.TreatmentNone:
	}
	if l.ItemList != "" {
		s += ", " + l.ItemList
	}
	if l.PackageRate {
		s += ", package rate"
	}
	return s
}

func coverText(c rules.Cover) string {
	amounts := make([]money.Paise, 0, len(c.Tranches))
	for _, t := range c.Tranches {
		amounts = append(amounts, t.SumInsured)
	}
	s := "Sum insured "
	if total, err := money.Sum(amounts...); err == nil {
		s += "₹" + rupees(total)
	} else {
		s += "too large to add up"
	}
	if len(c.Tranches) > 1 {
		s += fmt.Sprintf(" in %d tranches", len(c.Tranches))
	}
	if c.CumulativeBonus > 0 {
		s += ", cumulative bonus ₹" + rupees(c.CumulativeBonus)
	}
	if c.PaidThisYear > 0 {
		s += ", ₹" + rupees(c.PaidThisYear) + " paid this policy year"
	}
	return s + "."
}

func ledgerText(l rules.Ledger) string {
	s := "Ledger: ₹" + rupees(l.Paid) + " paid"
	if l.CataractUsed.Left > 0 || l.CataractUsed.Right > 0 {
		s += fmt.Sprintf("; cataract use ₹%s left eye, ₹%s right eye", rupees(l.CataractUsed.Left), rupees(l.CataractUsed.Right))
	}
	if l.ModernTreatmentUsed > 0 {
		s += "; modern-treatment use ₹" + rupees(l.ModernTreatmentUsed)
	}
	return s + "."
}

// rupees formats paise as rupees with Indian digit grouping, as the plan
// writes them: 11637500 is 1,16,375.00. A negative amount gets a minus sign.
func rupees(p money.Paise) string {
	// Dividing first keeps the negation in range, even for the most
	// negative int64.
	sign, whole, frac := "", int64(p)/100, int64(p)%100
	if p < 0 {
		sign, whole, frac = "−", -whole, -frac
	}
	digits := strconv.FormatInt(whole, 10)
	if len(digits) > 3 {
		head, groups := digits[:len(digits)-3], []string{digits[len(digits)-3:]}
		for len(head) > 2 {
			groups = append(groups, head[len(head)-2:])
			head = head[:len(head)-2]
		}
		groups = append(groups, head)
		slices.Reverse(groups)
		digits = strings.Join(groups, ",")
	}
	return fmt.Sprintf("%s%s.%02d", sign, digits, frac)
}

// percentText formats basis points as a percentage without trailing zeros:
// 500 is 5%, 6250 is 62.5%.
func percentText(bp money.BasisPoints) string {
	s := fmt.Sprintf("%d.%02d", bp/100, bp%100)
	s = strings.TrimSuffix(strings.TrimRight(s, "0"), ".")
	return s + "%"
}

// table writes a Markdown table with its columns padded, so it reads in a
// terminal too. right marks the columns aligned right.
func table(w io.Writer, header []string, right []bool, rows [][]string) {
	width := make([]int, len(header))
	for _, r := range append([][]string{header}, rows...) {
		for i, cell := range r {
			width[i] = max(width[i], utf8.RuneCountInString(cell), 3)
		}
	}
	line := func(cells []string) {
		var b strings.Builder
		for i, cell := range cells {
			pad := strings.Repeat(" ", width[i]-utf8.RuneCountInString(cell))
			if right[i] {
				b.WriteString("| " + pad + cell + " ")
			} else {
				b.WriteString("| " + cell + pad + " ")
			}
		}
		fmt.Fprintln(w, b.String()+"|")
	}
	line(header)
	rule := make([]string, len(header))
	for i := range rule {
		rule[i] = strings.Repeat("-", width[i])
		if right[i] {
			rule[i] = strings.Repeat("-", width[i]-1) + ":"
		}
	}
	line(rule)
	for _, r := range rows {
		line(r)
	}
}
