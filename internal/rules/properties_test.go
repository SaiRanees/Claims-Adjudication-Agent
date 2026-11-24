package rules_test

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"math"
	"math/big"
	"math/bits"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/rules"
	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/rules/money"
)

// The property tests run the engine on claims generated from fuzz input,
// using native fuzzing so no dependency is added. `go test` runs each
// fuzzer's seed corpus, as CI does; `go test -fuzz` searches further.

// gen draws a claim's choices from fuzz input. Past the end of the input
// every draw is zero, and a zero draw always picks the plainest option, so
// any input is a claim and a short one a plain claim.
type gen struct{ b []byte }

func (g *gen) next() byte {
	if len(g.b) == 0 {
		return 0
	}
	c := g.b[0]
	g.b = g.b[1:]
	return c
}

// n returns a number from 0 to n−1, reading as many bytes as n needs: the
// bytes read as one number, modulo n. Reducing at each byte keeps v below
// n, so v<<8 can't overflow for any n the generator uses (below 2^55).
func (g *gen) n(n int) int {
	if n <= 1 {
		return 0
	}
	v := 0
	for k := n - 1; k > 0; k >>= 8 {
		v = (v<<8 | int(g.next())) % n
	}
	return v
}

// oneIn reports true one time in k, and false on a zero draw.
func (g *gen) oneIn(k int) bool { return g.n(k) == k-1 }

func addDays(t *testing.T, d rules.Date, n int) rules.Date {
	t.Helper()
	e, err := d.AddDays(n)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

// genClaim draws a valid claim: one to three tranches, a stay of up to 12
// days in up to three segments of room or ICU, and up to eight lines of any
// category, some undated, outside the stay, listed, at a package rate or
// tied to a sub-limit, with every condition and cover amount varied.
func genClaim(t *testing.T, g *gen) (rules.Facts, rules.Cover) {
	t.Helper()
	var cover rules.Cover
	since := addDays(t, date(t, "2018-01-01"), g.n(3000))
	var total money.Paise
	for i := range 1 + g.n(3) {
		if i > 0 {
			since = addDays(t, since, g.n(900))
		}
		si := money.Paise(1+g.n(40)) * 2_500_000 // ₹25,000 steps to ₹10 lakh
		if g.oneIn(8) {
			si = money.Paise(1 + g.n(100_000_000))
		}
		cover.Tranches = append(cover.Tranches, rules.Tranche{SumInsured: si, ContinuousSince: since})
		total += si
	}

	// One claim in four is admitted near the latest tranche's start, where
	// the 30-day wait bites.
	admission := addDays(t, since, g.n(1500))
	if g.oneIn(4) {
		admission = addDays(t, since, g.n(45))
	}
	stay := 1 + g.n(12)
	discharge := addDays(t, admission, stay-1)
	starts := []int{0}
	free := make([]int, 0, stay)
	for d := 1; d < stay; d++ {
		free = append(free, d)
	}
	for range g.n(min(3, stay)) {
		k := g.n(len(free))
		starts = append(starts, free[k])
		free = slices.Delete(free, k, k+1)
	}
	slices.Sort(starts)
	f := rules.Facts{
		Admission:           admission,
		Discharge:           discharge,
		DifferentialBilling: []rules.Answer{rules.AnswerYes, rules.AnswerNo, rules.AnswerUnknown}[g.n(3)],
	}
	for k, from := range starts {
		to := stay - 1
		if k+1 < len(starts) {
			to = starts[k+1] - 1
		}
		kind := rules.SegmentRoom
		if g.oneIn(3) {
			kind = rules.SegmentICU
		}
		f.Segments = append(f.Segments, rules.Segment{From: addDays(t, admission, from), To: addDays(t, admission, to), Kind: kind})
	}

	cats := rules.Categories()
	buckets := []rules.Category{rules.CategoryRoomRent, rules.CategoryNursing, rules.CategoryPatientDiet, rules.CategoryICUCharges}
	lists := []string{"list-i", "list-ii", "list-iii", "list-iv", "list-v"}
	for i := range 1 + g.n(8) {
		l := rules.Line{ID: fmt.Sprintf("L%02d", i+1), Category: cats[g.n(len(cats))]}
		bucket := slices.Contains(buckets, l.Category)
		switch {
		case (!bucket || len(f.Segments) == 1) && g.oneIn(6):
			// Undated.
		case g.oneIn(10):
			if g.oneIn(2) {
				l.ServiceDate = addDays(t, admission, -1-g.n(5))
			} else {
				l.ServiceDate = addDays(t, discharge, 1+g.n(5))
			}
		default:
			l.ServiceDate = addDays(t, admission, g.n(stay))
		}
		l.Quantity = int64(1 + g.n(3))
		if bucket {
			l.Quantity = int64(1 + g.n(stay+1))
		}
		l.UnitPrice = money.Paise(g.n(1_000_001)) // to ₹10,000
		if g.oneIn(5) {
			l.UnitPrice = money.Paise(g.n(50_000_001)) // to ₹5 lakh
		}
		l.Amount = money.Paise(l.Quantity) * l.UnitPrice
		if g.oneIn(8) {
			l.ItemList = lists[g.n(len(lists))]
		}
		l.PackageRate = g.oneIn(12)
		switch {
		case g.oneIn(8):
			l.Treatment = rules.Treatment{Kind: rules.TreatmentCataract, Eye: []rules.Eye{rules.EyeLeft, rules.EyeRight}[g.n(2)]}
		case g.oneIn(10):
			l.Treatment.Kind = rules.TreatmentModernTreatment
		}
		f.Lines = append(f.Lines, l)
	}
	if g.oneIn(6) {
		f.BillDiscount = money.Paise(g.n(2_000_001))
	}

	f.Condition.ListedGroup = []string{"", "", "listed-24-months", "listed-36-months"}[g.n(4)]
	f.Condition.PreExisting = g.oneIn(6)
	f.Condition.Accident = g.oneIn(6)
	if g.oneIn(3) {
		onset := addDays(t, cover.Tranches[0].ContinuousSince, g.n(800)-90)
		if onset.After(admission) {
			onset = admission
		}
		f.Condition.Onset = onset
	}

	if g.oneIn(4) {
		cover.CumulativeBonus = money.Paise(g.n(int(total/2) + 1))
	}
	if g.oneIn(4) {
		cover.PaidThisYear = money.Paise(g.n(int(total+total/4) + 1)) // sometimes past the cover
	}
	if g.oneIn(6) {
		cover.CataractUsed = rules.EyeAmounts{Left: money.Paise(g.n(5_000_001)), Right: money.Paise(g.n(5_000_001))}
	}
	if g.oneIn(6) {
		cover.ModernTreatmentUsed = money.Paise(g.n(int(total/2) + 1))
	}
	return f, cover
}

// checkResult checks what holds of every result, in big integers so the
// check can't overflow itself: each line pays from zero to what it billed,
// its billed less its payable is its adjustments, every adjustment is
// positive and names a line, the lines add up to the totals, the ledger
// pays the payable, and cover_exceeded's figure is never below the payable.
func checkResult(t *testing.T, f rules.Facts, res rules.Result) {
	t.Helper()
	billedIn := make(map[string]money.Paise, len(f.Lines))
	for _, l := range f.Lines {
		billedIn[l.ID] = l.Amount
	}
	adjusted := map[string]*big.Int{}
	for _, a := range res.Adjustments {
		if a.Amount <= 0 {
			t.Errorf("adjustment %s on %s is %d; only positive adjustments are listed", a.RuleID, a.LineID, a.Amount)
		}
		if _, ok := billedIn[a.LineID]; !ok {
			t.Errorf("adjustment %s names line %q, which the bill doesn't have", a.RuleID, a.LineID)
		}
		if adjusted[a.LineID] == nil {
			adjusted[a.LineID] = new(big.Int)
		}
		adjusted[a.LineID].Add(adjusted[a.LineID], big.NewInt(int64(a.Amount)))
	}
	if len(res.Lines) != len(f.Lines) {
		t.Errorf("%d result lines for %d billed", len(res.Lines), len(f.Lines))
	}
	billed, payable := new(big.Int), new(big.Int)
	for _, l := range res.Lines {
		if l.Billed != billedIn[l.ID] {
			t.Errorf("%s: billed %d, but the bill says %d", l.ID, l.Billed, billedIn[l.ID])
		}
		if l.Payable < 0 || l.Payable > l.Billed {
			t.Errorf("%s pays %d of the %d it billed", l.ID, l.Payable, l.Billed)
		}
		adj := adjusted[l.ID]
		if adj == nil {
			adj = new(big.Int)
		}
		if d := new(big.Int).Sub(big.NewInt(int64(l.Billed)), big.NewInt(int64(l.Payable))); d.Cmp(adj) != 0 {
			t.Errorf("%s: billed %d less payable %d is %s, but its adjustments are %s", l.ID, l.Billed, l.Payable, d, adj)
		}
		billed.Add(billed, big.NewInt(int64(l.Billed)))
		payable.Add(payable, big.NewInt(int64(l.Payable)))
	}
	if billed.Cmp(big.NewInt(int64(res.Billed))) != 0 || payable.Cmp(big.NewInt(int64(res.Payable))) != 0 {
		t.Errorf("the lines add to billed %s and payable %s; the result says %d and %d", billed, payable, res.Billed, res.Payable)
	}
	if res.Ledger.Paid != res.Payable {
		t.Errorf("the ledger pays %d of a payable %d", res.Ledger.Paid, res.Payable)
	}
	for _, fl := range res.Flags {
		if fl.Code == rules.FlagCoverExceeded && fl.Figure < res.Payable {
			t.Errorf("cover_exceeded's figure %d is below the payable %d", fl.Figure, res.Payable)
		}
	}
}

func cloneFacts(f rules.Facts) rules.Facts {
	f.Segments = slices.Clone(f.Segments)
	f.Lines = slices.Clone(f.Lines)
	return f
}

// seeds returns n inputs of 256 bytes each, the same on every run.
func seeds(n int) [][]byte {
	out := make([][]byte, n)
	for i := range out {
		for j := range 8 {
			h := sha256.Sum256([]byte{byte(i), byte(i >> 8), byte(j)})
			out[i] = append(out[i], h[:]...)
		}
	}
	return out
}

// roundingBound is the most that rounding can lower the payable when the
// sum insured rises, lo and hi being the results at the lower and the
// higher. ADR 0011 has the derivation:
//
//   - Without rounding, a higher sum insured never lowers the payable.
//   - Largest remainder keeps each line of a split within a paisa of its
//     exact share, and the shares add up to the split's total. So a split
//     only moves less than a paisa per line between its lines.
//   - That reaches the payable only through a later sub-limit that holds
//     some of the split's lines and not others. Across that sub-limit the
//     paise moved add up to less than the smaller of its lines inside and
//     outside it (roundingSplits). For the Go Digit pack, only the daily
//     caps' splits can do that since ADR 0012.
//   - After the sub-limits the steps act on the total, and none of them
//     moves the payable by more than the total moves.
//
// At each sum insured the payable is then less than that count away from
// the unrounded one, which doesn't fall. The drop is less than the two
// counts together, so at most their sum less a paisa, and nothing when both
// are zero.
func roundingBound(t *testing.T, pack *rules.Pack, f rules.Facts, lo, hi rules.Result) money.Paise {
	t.Helper()
	n := roundingSplits(t, pack, f, lo) + roundingSplits(t, pack, f, hi)
	if n == 0 {
		return 0
	}
	return money.Paise(n - 1)
}

// subLimitGroup is one sub-limit's lines: rule is its rule's ID, and in
// reports whether a line is tied to it.
type subLimitGroup struct {
	rule string
	in   func(rules.Line) bool
}

// roundingSplits counts, for one result, the lines that rounding can move a
// paisa between and that a later sub-limit treats differently. It looks at
// each split the result made (a split made a cut, so at least one of its
// lines took a share) whose lines a later sub-limit tells apart:
//
//   - A daily cap's cut over a segment's bucket: the segment's own lines,
//     or the lines held to both caps, once for each cap (ADR 0011). Every
//     sub-limit comes later.
//   - A sub-limit's cut over its lines. Only the sub-limits after it in the
//     pack count.
//
// For each split and each later sub-limit, it adds the smaller of the
// split's lines inside the sub-limit and outside it. A line counts only if
// it entered the split with something to pay, because a line of zero
// weight takes an exact share of zero.
//
// For the Go Digit pack, since ADR 0012, no sub-limit's split can lower the
// payable. The road-ambulance limit comes first, and its split works on
// amounts the sum insured can't move: an ambulance line is in no bucket and
// never scaled, and the limit is a fixed ceiling. The cataract and
// modern-treatment limits share no line, so neither tells the other's
// lines apart. The count still includes the ambulance limit's split ahead
// of the treatment limits, when its lines are tied differently: that keeps
// the bound sound for any order the parser accepts, at the cost of a bound
// above zero for a claim that can't actually drop.
func roundingSplits(t *testing.T, pack *rules.Pack, f rules.Facts, res rules.Result) int64 {
	t.Helper()
	caps, okCaps := pack.Step(rules.StepApplyDailyCaps)
	subs, okSubs := pack.Step(rules.StepApplySubLimits)
	if !okCaps || !okSubs {
		t.Fatal("the pack has no daily caps or no sub-limits")
	}
	bucketOf := map[rules.Category]rules.SegmentKind{}
	capRule := map[rules.SegmentKind]string{}
	for _, r := range caps.Rules {
		kind := rules.SegmentKind(r.AppliesTo)
		capRule[kind] = r.ID
		for _, c := range r.Categories {
			bucketOf[c] = kind
		}
	}
	// The sub-limits in the order step 4 applies them.
	var groups []subLimitGroup
	for _, r := range subs.Rules {
		switch r.AppliesTo {
		case "cataract":
			for _, eye := range []rules.Eye{rules.EyeLeft, rules.EyeRight} {
				groups = append(groups, subLimitGroup{r.ID, func(l rules.Line) bool {
					return l.Treatment.Kind == rules.TreatmentCataract && l.Treatment.Eye == eye
				}})
			}
		case "modern-treatment":
			groups = append(groups, subLimitGroup{r.ID, func(l rules.Line) bool { return l.Treatment.Kind == rules.TreatmentModernTreatment }})
		case "road-ambulance":
			groups = append(groups, subLimitGroup{r.ID, func(l rules.Line) bool { return l.Category == rules.CategoryRoadAmbulance }})
		default:
			t.Fatalf("sub-limit rule %s applies to %q, which roundingBound doesn't know", r.ID, r.AppliesTo)
		}
	}

	// The rules that act before the daily caps, and before the sub-limits.
	beforeCaps, beforeSubs := map[string]bool{}, map[string]bool{}
	capsSeen := false
	for _, s := range pack.Steps {
		if s.Name == rules.StepApplySubLimits {
			break
		}
		capsSeen = capsSeen || s.Name == rules.StepApplyDailyCaps
		for _, r := range s.Rules {
			beforeSubs[r.ID] = true
			if !capsSeen {
				beforeCaps[r.ID] = true
			}
		}
	}
	if !capsSeen {
		t.Fatal("roundingBound expects the daily caps before the sub-limits")
	}
	// What each line had left entering the daily caps and the sub-limits,
	// and which rules cut which lines.
	toCaps, toSubs := map[string]money.Paise{}, map[string]money.Paise{}
	for _, l := range f.Lines {
		toCaps[l.ID], toSubs[l.ID] = l.Amount, l.Amount
	}
	cutBy := map[[2]string]bool{}
	for _, a := range res.Adjustments {
		cutBy[[2]string{a.LineID, a.RuleID}] = true
		if beforeCaps[a.RuleID] {
			toCaps[a.LineID] -= a.Amount
		}
		if beforeSubs[a.RuleID] {
			toSubs[a.LineID] -= a.Amount
		}
	}
	took := func(split []rules.Line, rule func(rules.Line) string) bool {
		return slices.ContainsFunc(split, func(l rules.Line) bool { return cutBy[[2]string{l.ID, rule(l)}] })
	}
	across := func(split []rules.Line, later []subLimitGroup) int64 {
		var n int64
		for _, g := range later {
			var in int64
			for _, l := range split {
				if g.in(l) {
					in++
				}
			}
			n += min(in, int64(len(split))-in)
		}
		return n
	}

	var n int64
	for si, sg := range f.Segments {
		// The segment's own bucket lines, and the lines of the other kind
		// held to both caps.
		var own, held []rules.Line
		for _, l := range f.Lines {
			kind, ok := bucketOf[l.Category]
			if !ok || toCaps[l.ID] <= 0 || segmentIndex(f, l.ServiceDate) != si {
				continue
			}
			if kind == sg.Kind {
				own = append(own, l)
			} else {
				held = append(held, l)
			}
		}
		segmentCap := func(rules.Line) string { return capRule[sg.Kind] }
		ownCap := func(l rules.Line) string { return capRule[bucketOf[l.Category]] }
		if took(held, ownCap) {
			n += across(held, groups)
		}
		if took(held, segmentCap) {
			n += across(held, groups)
		}
		if took(own, segmentCap) {
			n += across(own, groups)
		}
	}
	for k, g := range groups {
		var split []rules.Line
		for _, l := range f.Lines {
			if g.in(l) && toSubs[l.ID] > 0 {
				split = append(split, l)
			}
		}
		if took(split, func(rules.Line) string { return g.rule }) {
			n += across(split, groups[k+1:])
		}
	}
	return n
}

// segmentIndex returns the index of the segment d falls in: an undated line
// is in a stay's only segment, and otherwise in none (-1).
func segmentIndex(f rules.Facts, d rules.Date) int {
	if d.IsZero() {
		if len(f.Segments) == 1 {
			return 0
		}
		return -1
	}
	for i, s := range f.Segments {
		if !d.Before(s.From) && !d.After(s.To) {
			return i
		}
	}
	return -1
}

// FuzzAdjudicateProperties checks, on every generated claim, the invariants
// of checkResult and four more: the same input run twice gives identical
// results and isn't changed by the engine, shuffling the lines changes
// nothing, and raising a tranche's sum insured never lowers the payable by
// more than rounding can (roundingBound), which for most claims is nothing.
func FuzzAdjudicateProperties(f *testing.F) {
	f.Add([]byte{})
	for _, s := range seeds(300) {
		f.Add(s)
	}
	pack, err := rules.ParsePack([]byte(goDigitSource(f)))
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		g := &gen{b: data}
		facts, cover := genClaim(t, g)
		before := cloneFacts(facts)
		res, err := rules.Adjudicate(pack, facts, cover)
		if err != nil {
			t.Fatalf("a valid claim failed: %v\nfacts %+v\ncover %+v", err, facts, cover)
		}
		checkResult(t, facts, res)

		again, err := rules.Adjudicate(pack, facts, cover)
		if err != nil || !reflect.DeepEqual(again, res) {
			t.Errorf("the same input gave a different result (%v):\n got %+v\nwant %+v", err, again, res)
		}
		if !reflect.DeepEqual(facts, before) {
			t.Errorf("Adjudicate changed its facts:\n got %+v\nwant %+v", facts, before)
		}

		shuffled := cloneFacts(facts)
		for i := len(shuffled.Lines) - 1; i > 0; i-- {
			j := g.n(i + 1)
			shuffled.Lines[i], shuffled.Lines[j] = shuffled.Lines[j], shuffled.Lines[i]
		}
		got, err := rules.Adjudicate(pack, shuffled, cover)
		if err != nil || !reflect.DeepEqual(got, res) {
			t.Errorf("shuffling the lines changed the result (%v):\n got %+v\nwant %+v", err, got, res)
		}

		raised := cover
		raised.Tranches = slices.Clone(cover.Tranches)
		k := g.n(len(raised.Tranches))
		raised.Tranches[k].SumInsured += money.Paise(1 + g.n(50_000_000))
		up, err := rules.Adjudicate(pack, facts, raised)
		if err != nil {
			t.Fatalf("raising tranche %d's sum insured: %v", k, err)
		}
		if drop := res.Payable - up.Payable; drop > 0 {
			if bound := roundingBound(t, pack, facts, res, up); drop > bound {
				t.Errorf("raising tranche %d's sum insured from %d to %d lowered the payable from %d to %d, more than the rounding bound of %d",
					k, cover.Tranches[k].SumInsured, raised.Tranches[k].SumInsured, res.Payable, up.Payable, bound)
			}
		}
	})
}

// TestRoundingBound pins the two ways rounding is known to lower the
// payable by a paisa when the sum insured rises, each within its bound, and
// the examples of ADRs 0011 and 0012, whose bounds are zero. ADR 0011's
// third source, a sub-limit's split ahead of the ambulance limit, is gone
// since ADR 0012 put the ambulance limit first.
func TestRoundingBound(t *testing.T) {
	pack := goDigitPack(t)
	cataract := func(l rules.Line) rules.Line {
		l.Treatment = rules.Treatment{Kind: rules.TreatmentCataract, Eye: rules.EyeLeft}
		return l
	}
	oneTranche := func(t *testing.T, sumInsured money.Paise, since string) rules.Cover {
		return rules.Cover{Tranches: []rules.Tranche{{SumInsured: sumInsured, ContinuousSince: date(t, since)}}}
	}
	tests := []struct {
		name                   string
		facts                  func(t *testing.T) rules.Facts
		since                  string
		sumInsured, raised     money.Paise
		payable, raisedPayable money.Paise
		bound                  money.Paise
	}{
		{
			// The Alabama paradox. 50 paise more sum insured raises the
			// room cap by a paisa, so the room bucket's cut falls by one,
			// yet largest remainder takes a paisa more from the untied room
			// line (22,282 to 22,283) and one less from nursing and diet.
			// Both are tied to a cataract whose ₹40,000 implant already
			// reaches the limit, so the limit takes their paisa off again.
			// The bucket's split counts min(2 tied, 1 untied) = 1 at each
			// sum insured, so the bound is 1 + 1 − 1.
			name: "the Alabama paradox in a bucket of three lines, two tied",
			facts: func(t *testing.T) rules.Facts {
				return facts(rules.AnswerYes, []rules.Segment{seg(t, "2025-07-01", "2025-07-02", rules.SegmentRoom)},
					ln(t, "L01", rules.CategoryRoomRent, "2025-07-01", 1, 69_106),
					cataract(ln(t, "L02", rules.CategoryNursing, "2025-07-01", 1, 295_423)),
					cataract(ln(t, "L03", rules.CategoryPatientDiet, "2025-07-01", 1, 225_826)),
					cataract(ln(t, "L04", rules.CategoryImplantsDevices, "2025-07-01", 1, 4_000_000)),
				)
			},
			since:      "2023-04-01",
			sumInsured: 20_000_000, raised: 20_000_050,
			payable: 3_844_483, raisedPayable: 3_844_482,
			bound: 1,
		},
		{
			// The double rounding: fuzz input 9d50a174da0c39c0. Two room
			// lines on an ICU day, one tied to a cataract, are rounded at
			// the room cap and again at the ICU cap. Their total is 383,187
			// at both sums insured, but the tied line's share rises from
			// 64,600 to 64,601 and the cataract limit takes the paisa. The
			// two lines count min(1, 1) = 1 at each cap, so 2 at each sum
			// insured, and the bound is 2 + 2 − 1.
			name: "the double rounding of lines held to both caps",
			facts: func(t *testing.T) rules.Facts {
				return facts(rules.AnswerYes, []rules.Segment{seg(t, "2019-11-04", "2019-11-05", rules.SegmentICU)},
					cataract(ln(t, "L01", rules.CategoryProfessionalFees, "2019-11-04", 1, 8_464_416)),
					cataract(ln(t, "L02", rules.CategoryRoomRent, "2019-11-04", 1, 158_061)),
					ln(t, "L03", rules.CategoryProfessionalFees, "", 1, 158_061),
					ln(t, "L04", rules.CategoryRoomRent, "2019-11-04", 1, 779_499),
					ln(t, "L05", rules.CategoryICUCharges, "2019-11-04", 1, 616_813),
				)
			},
			since:      "2018-12-03",
			sumInsured: 22_500_000, raised: 30_964_433,
			payable: 4_785_234, raisedPayable: 4_785_233,
			bound: 3,
		},
		{
			// ADR 0012's example, once ADR 0011's third source of a drop.
			// With the cataract limit first, its cut was split across the
			// road-ambulance line and the others, and the ambulance limit,
			// which came later, took the line's share off again: 3,629,282
			// paise, then 3,629,281. That was a rule fault, not rounding.
			// The ambulance limit now comes first and brings the line to
			// ₹2,000, so the tied lines come to ₹49,733.03 (₹49,733.04 at the
			// higher sum insured, the room cap a paisa higher), the cataract
			// limit admits ₹40,000 under both, and ₹38,000 is paid. The
			// ambulance split has one line, and the cataract split's lines
			// are all tied to the cataract, so the bound is 0.
			name: "ADR 0012's example: the ambulance limit before the cataract limit",
			facts: func(t *testing.T) rules.Facts {
				return facts(rules.AnswerYes, []rules.Segment{seg(t, "2025-07-01", "2025-07-02", rules.SegmentRoom)},
					cataract(ln(t, "L01", rules.CategoryRoomRent, "2025-07-01", 1, 534_829)),
					cataract(ln(t, "L02", rules.CategoryImplantsDevices, "2025-07-01", 1, 4_373_303)),
					cataract(ln(t, "L03", rules.CategoryRoadAmbulance, "2025-07-01", 1, 500_633)),
				)
			},
			since:      "2023-04-01",
			sumInsured: 20_000_000, raised: 20_000_050,
			payable: 3_800_000, raisedPayable: 3_800_000,
			bound: 0,
		},
		{
			// ADR 0011's example: no split mixes lines a sub-limit tells
			// apart, so the property holds exactly, and it does.
			name: "ADR 0011's example has a bound of zero",
			facts: func(t *testing.T) rules.Facts {
				return facts(rules.AnswerYes, []rules.Segment{seg(t, "2025-07-01", "2025-07-02", rules.SegmentICU)},
					ln(t, "L01", rules.CategoryICUCharges, "2025-07-01", 1, 1_200_000),
					cataract(ln(t, "L02", rules.CategoryNursing, "2025-07-01", 1, 600_000)),
					cataract(ln(t, "L03", rules.CategoryImplantsDevices, "2025-07-01", 1, 4_000_000)),
				)
			},
			since:      "2023-04-01",
			sumInsured: 20_000_000, raised: 25_000_000,
			payable: 4_750_000, raisedPayable: 4_750_000,
			bound: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := tt.facts(t)
			lo, err := rules.Adjudicate(pack, f, oneTranche(t, tt.sumInsured, tt.since))
			if err != nil {
				t.Fatal(err)
			}
			hi, err := rules.Adjudicate(pack, f, oneTranche(t, tt.raised, tt.since))
			if err != nil {
				t.Fatal(err)
			}
			if lo.Payable != tt.payable || hi.Payable != tt.raisedPayable {
				t.Errorf("payable %d, then %d; want %d, then %d", lo.Payable, hi.Payable, tt.payable, tt.raisedPayable)
			}
			bound := roundingBound(t, pack, f, lo, hi)
			if bound != tt.bound {
				t.Errorf("rounding bound %d, want %d", bound, tt.bound)
			}
			if drop := lo.Payable - hi.Payable; drop > bound {
				t.Errorf("the payable fell by %d, more than the bound of %d", drop, bound)
			}
		})
	}
}

// limitsPack is the Go Digit pack with every ceiling and share and the
// co-payment rate replaced, each mapped into the range the pack allows.
// The replacements are made in one pass over the original text, so a new
// value can't be mistaken for one still to replace. Each must occur once,
// and the parsed pack must carry each value on the rule it is meant for,
// so moving a rule in the pack (ADR 0012) can't send a value to another.
func limitsPack(t *testing.T, src string, ceilings [4]int64, shares [4]uint16, coPay uint16) *rules.Pack {
	t.Helper()
	ceiling := func(v int64) money.Paise { return money.Paise(max(1, v&math.MaxInt64)) }
	share := func(v uint16) money.BasisPoints { return money.BasisPoints(1 + int(v)%int(money.FullRate)) }
	rate := money.BasisPoints(int(coPay) % (int(money.FullRate) + 1))
	both := func(sh uint16, c int64) rules.Limit {
		return rules.Limit{Share: share(sh), HasShare: true, Ceiling: ceiling(c), HasCeiling: true}
	}
	text := func(l rules.Limit) string {
		var s string
		if l.HasShare {
			s += "share_of_sum_insured_bp: " + strconv.Itoa(int(l.Share)) + "\n"
		}
		if l.HasShare && l.HasCeiling {
			s += "        "
		}
		if l.HasCeiling {
			s += "ceiling_paise: " + strconv.FormatInt(int64(l.Ceiling), 10) + "\n"
		}
		return s
	}
	limits := []struct {
		rule string
		old  string
		new  rules.Limit
	}{
		{"daily-cap-room", "share_of_sum_insured_bp: 200\n        ceiling_paise: 500000\n", both(shares[0], ceilings[0])},
		{"daily-cap-icu", "share_of_sum_insured_bp: 500\n        ceiling_paise: 1000000\n", both(shares[1], ceilings[1])},
		{"sub-limit-cataract", "share_of_sum_insured_bp: 2500\n        ceiling_paise: 4000000\n", both(shares[2], ceilings[2])},
		{"sub-limit-modern-treatment", "share_of_sum_insured_bp: 5000\n", rules.Limit{Share: share(shares[3]), HasShare: true}},
		{"sub-limit-road-ambulance", "ceiling_paise: 200000\n", rules.Limit{Ceiling: ceiling(ceilings[3]), HasCeiling: true}},
	}
	var pairs []string
	for _, l := range limits {
		pairs = append(pairs, l.old, text(l.new))
	}
	pairs = append(pairs, "rate_bp: 500\n", "rate_bp: "+strconv.Itoa(int(rate))+"\n")
	for i := 0; i < len(pairs); i += 2 {
		if n := strings.Count(src, pairs[i]); n != 1 {
			t.Fatalf("%q occurs %d times in the pack, want 1", pairs[i], n)
		}
	}
	p, err := rules.ParsePack([]byte(strings.NewReplacer(pairs...).Replace(src)))
	if err != nil {
		t.Fatalf("a pack with valid limits failed to parse: %v", err)
	}
	for _, l := range limits {
		if r, ok := p.Rule(l.rule); !ok || r.Limit != l.new {
			t.Fatalf("%s's limit is %+v, want %+v: a replacement missed its rule", l.rule, r.Limit, l.new)
		}
	}
	if r, ok := p.Rule("co-payment"); !ok || r.Rate != rate {
		t.Fatalf("co-payment's rate is %d, want %d: a replacement missed its rule", r.Rate, rate)
	}
	return p
}

// limitsLine is a line of qty × price, held to a quantity of 1 when the
// product doesn't fit, so the bill is valid and the extremes reach the
// steps.
func limitsLine(id string, cat rules.Category, day rules.Date, qty, price int64) rules.Line {
	price &= math.MaxInt64
	qty = max(1, qty&math.MaxInt64)
	if hi, lo := bits.Mul64(uint64(qty), uint64(price)); hi != 0 || lo > math.MaxInt64 {
		qty = 1
	}
	return rules.Line{ID: id, Category: cat, ServiceDate: day, Quantity: qty, UnitPrice: money.Paise(price), Amount: money.Paise(qty * price)}
}

// limitsSeed is one starting point for FuzzAdjudicateLimits.
type limitsSeed struct {
	roomDays, room, icu, fee, implant, ambulance    int64
	sumInsured, increase, bonus, paid, discount     int64
	used                                            int64
	roomCap, icuCap, cataractCap, ambulanceCap      int64
	roomShare, icuShare, cataractShare, modernShare uint16
	coPay, year, back                               uint16
}

// FuzzAdjudicateLimits runs the engine on packs and claims near the int64
// limits: the pack's ceilings, shares and co-payment rate, every amount in
// the facts and the cover, a room billed for up to math.MaxInt64 days, and
// dates near the calendar's ends. Whatever the values, the engine returns
// either an error of a known kind or a result that keeps every invariant,
// and raising the sum insured by a paisa doesn't lower its payable. It
// never panics and never overflows silently.
func FuzzAdjudicateLimits(f *testing.F) {
	const top = math.MaxInt64
	for _, s := range []limitsSeed{
		// The pack as it is, on validClaim's figures.
		{2, 400_000, 900_000, 3_000_000, 1_200_000, 150_000, 50_000_000, 0, 0, 0, 0, 0,
			500_000, 1_000_000, 4_000_000, 200_000, 199, 499, 2499, 4999, 500, 2025, 800},
		// Every amount and ceiling at the top.
		{1, top, top, top, top, top, top, top, top, top, top, top, top, top, top, top, 9999, 9999, 9999, 9999, 10000, 2025, 800},
		// The sum insured at the top, the bill small.
		{2, 400_000, 900_000, 3_000_000, 1_200_000, 150_000, top, 0, 0, 0, 0, 0,
			top, top, top, top, 9999, 9999, 9999, 9999, 500, 2025, 800},
		// A room billed for every day there is, at a paisa and at nothing.
		{top, 1, 0, 0, 0, 0, 50_000_000, 0, 0, 0, 0, 0,
			500_000, 1_000_000, 4_000_000, 200_000, 199, 499, 2499, 4999, 500, 2025, 800},
		{top, 0, 0, 0, 0, 0, 50_000_000, 0, 0, 0, 0, 0, top, top, top, top, 199, 499, 2499, 4999, 500, 2025, 800},
		// A bill just under the top in total, and the cover used past it.
		{1, top / 8, top / 8, top / 8, top / 8, top / 8, top / 2, top / 2, 0, top, top / 16, top,
			top, top, top, top, 9999, 9999, 9999, 9999, 10000, 2025, 800},
		// The calendar's ends: waits that run past year 9999, and a
		// tranche from year 1.
		{2, 400_000, 900_000, 3_000_000, 1_200_000, 150_000, 50_000_000, 50_000_000, 0, 0, 0, 0,
			500_000, 1_000_000, 4_000_000, 200_000, 199, 499, 2499, 4999, 500, 9997, 10},
		{2, 400_000, 900_000, 3_000_000, 1_200_000, 150_000, 50_000_000, 0, 0, 0, 0, 0,
			500_000, 1_000_000, 4_000_000, 200_000, 199, 499, 2499, 4999, 500, 0, 0},
	} {
		f.Add(s.roomDays, s.room, s.icu, s.fee, s.implant, s.ambulance, s.sumInsured, s.increase, s.bonus, s.paid, s.discount,
			s.used, s.roomCap, s.icuCap, s.cataractCap, s.ambulanceCap, s.roomShare, s.icuShare, s.cataractShare, s.modernShare,
			s.coPay, s.year, s.back)
	}
	src := goDigitSource(f)
	f.Fuzz(func(t *testing.T, roomDays, room, icu, fee, implant, ambulance, sumInsured, increase, bonus, paid, discount,
		used, roomCap, icuCap, cataractCap, ambulanceCap int64, roomShare, icuShare, cataractShare, modernShare,
		coPay, year, back uint16,
	) {
		pack := limitsPack(t, src, [4]int64{roomCap, icuCap, cataractCap, ambulanceCap},
			[4]uint16{roomShare, icuShare, cataractShare, modernShare}, coPay)

		// A 4-day stay, two days in a room and two in ICU, starting on a
		// day of year 1 to 9998, so the stay itself fits the calendar.
		admission, err := rules.NewDate(1+int(year)%9998, time.January, 1)
		if err != nil {
			t.Fatal(err)
		}
		admission = addDays(t, admission, int(back)%365)
		day := func(n int) rules.Date { return addDays(t, admission, n) }
		facts := rules.Facts{
			Admission: admission,
			Discharge: day(3),
			Segments: []rules.Segment{
				{From: admission, To: day(1), Kind: rules.SegmentRoom},
				{From: day(2), To: day(3), Kind: rules.SegmentICU},
			},
			DifferentialBilling: rules.AnswerYes,
			BillDiscount:        money.Paise(discount & math.MaxInt64),
			Lines: []rules.Line{
				limitsLine("L01", rules.CategoryRoomRent, admission, roomDays, room),
				limitsLine("L02", rules.CategoryICUCharges, day(2), 2, icu),
				limitsLine("L03", rules.CategoryProfessionalFees, admission, 1, fee),
				limitsLine("L04", rules.CategoryImplantsDevices, rules.Date{}, 1, implant),
				limitsLine("L05", rules.CategoryOtherHospitalCharges, day(2), 1, fee),
				limitsLine("L06", rules.CategoryRoadAmbulance, admission, 1, ambulance),
				limitsLine("L07", rules.CategoryPharmacyConsumables, day(3), 1, icu),
			},
		}
		facts.Lines[3].Treatment = rules.Treatment{Kind: rules.TreatmentCataract, Eye: rules.EyeLeft}
		facts.Lines[4].Treatment.Kind = rules.TreatmentModernTreatment

		// The base tranche began up to about 90 years before the
		// admission, and never before year 1; an increase began halfway.
		start := func(daysBack int) rules.Date {
			d, err := admission.AddDays(-daysBack)
			if err != nil {
				d, err = rules.NewDate(1, time.January, 1)
				if err != nil {
					t.Fatal(err)
				}
			}
			return d
		}
		cover := rules.Cover{
			Tranches:            []rules.Tranche{{SumInsured: money.Paise(max(1, sumInsured&math.MaxInt64)), ContinuousSince: start(int(back) / 2)}},
			CumulativeBonus:     money.Paise(bonus & math.MaxInt64),
			PaidThisYear:        money.Paise(paid & math.MaxInt64),
			CataractUsed:        rules.EyeAmounts{Left: money.Paise(used & math.MaxInt64)},
			ModernTreatmentUsed: money.Paise(used & math.MaxInt64),
		}
		if inc := increase & math.MaxInt64; inc > 0 {
			cover.Tranches = append(cover.Tranches, rules.Tranche{SumInsured: money.Paise(inc), ContinuousSince: start(int(back) / 4)})
		}

		res, err := rules.Adjudicate(pack, facts, cover)
		if err != nil {
			if !errors.Is(err, rules.ErrInvalidInput) && !errors.Is(err, money.ErrOverflow) && !errors.Is(err, rules.ErrInvalidDate) {
				t.Fatalf("an error of an unknown kind: %v", err)
			}
			return
		}
		checkResult(t, facts, res)

		if cover.Tranches[0].SumInsured == math.MaxInt64 {
			return
		}
		raised := cover
		raised.Tranches = slices.Clone(cover.Tranches)
		raised.Tranches[0].SumInsured++
		up, err := rules.Adjudicate(pack, facts, raised)
		if err == nil && up.Payable < res.Payable {
			t.Errorf("a paisa more sum insured lowered the payable from %d to %d", res.Payable, up.Payable)
		}
	})
}
