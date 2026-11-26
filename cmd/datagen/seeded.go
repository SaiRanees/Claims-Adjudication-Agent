package main

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/SaiRanees/Claims-Adjudication-Agent/evals/extraction"
	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/datagen"
	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/rules"
	"github.com/SaiRanees/Claims-Adjudication-Agent/rulepacks"
)

// perType is how many seeded scenarios each claim type has.
const perType = 8

// wordingUIN is the policy wording every seeded scenario is under.
const wordingUIN = "GODHLIP24179V022324"

// claimType is one of the six kinds of claim the seeded scenarios come
// from.
type claimType struct {
	// Name is the type in scenario IDs and the split file.
	Name       string
	About      string
	build      func(b *builder, c condition)
	conditions []condition
}

var claimTypes = []claimType{
	{"medical", "a medical admission", (*builder).medical, medicalConditions},
	{"surgery-implant", "surgery with an implant", (*builder).surgery, surgeryConditions},
	{"cataract-day-care", "cataract surgery as day care", (*builder).cataract, cataractConditions},
	{"icu-step-down", "an ICU stay with a step-down to the ward", (*builder).icu, icuConditions},
	{"accident-ambulance", "an accident brought in by ambulance", (*builder).accident, accidentConditions},
	{"long-pharmacy", "a long, pharmacy-heavy stay", (*builder).long, longConditions},
}

// seededID is the nth (from 0) seeded scenario's ID within its type.
func seededID(claimType string, n int) string { return fmt.Sprintf("seeded-%s-%02d", claimType, n+1) }

// seededSpecs writes the seeded scenario specs once, eight for each claim
// type. A spec that exists is never overwritten: specs are committed and
// reviewed like golden cases, so a later change to this code can't quietly
// move them. Each spec must parse, render and adjudicate cleanly before
// it's written.
func seededSpecs(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("scenarios", flag.ContinueOnError)
	fs.SetOutput(stderr)
	out := fs.String("out", defaultSpecs, "directory to write the specs into")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		fmt.Fprint(stderr, usage)
		return errUsage
	}
	reg, err := rulepacks.Registry()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(*out, 0o750); err != nil {
		return err
	}
	written, total := 0, 0
	for ti, ct := range claimTypes {
		for n := range perType {
			total++
			id := seededID(ct.Name, n)
			path := filepath.Join(*out, id+".yaml")
			if _, err := os.Stat(path); err == nil {
				continue
			} else if !errors.Is(err, os.ErrNotExist) {
				return err
			}
			s, err := seededSpec(ti, n)
			if err != nil {
				return fmt.Errorf("%s: %w", id, err)
			}
			header := fmt.Sprintf("# Seeded scenario %s: %s.\n", id, ct.About) +
				"# Written once by `go run ./cmd/datagen scenarios`, then reviewed and\n" +
				"# committed like a golden case. Everything in it is invented, and the\n" +
				"# engine adjudicates its facts without a flag that would mean the\n" +
				"# generator erred.\n\n"
			src, err := encodeSpec(header, s)
			if err != nil {
				return err
			}
			parsed, err := datagen.ParseSpec(id, src)
			if err != nil {
				return err
			}
			if _, err := datagen.Render(parsed); err != nil {
				return fmt.Errorf("%s: %w", id, err)
			}
			if err := adjudicatesCleanly(reg, parsed); err != nil {
				return fmt.Errorf("%s: %w", id, err)
			}
			if err := os.WriteFile(path, src, 0o600); err != nil {
				return err
			}
			written++
		}
	}
	fmt.Fprintf(stdout, "wrote %d seeded specs into %s; %d already existed\n", written, *out, total-written)
	return nil
}

// adjudicatesCleanly runs extraction.CheckSeeded on a spec's facts.
func adjudicatesCleanly(reg *rules.Registry, s datagen.Spec) error {
	period, cover, facts, err := s.Engine()
	if err != nil {
		return err
	}
	return extraction.CheckSeeded(reg, period, cover, facts)
}

// seededSpec builds the nth (from 0) scenario of claim type ti.
func seededSpec(ti, n int) (datagen.Spec, error) {
	ct := claimTypes[ti]
	b := newBuilder(seededID(ct.Name, n), ti*perType+n+1, n)
	c := ct.conditions[n%len(ct.conditions)]
	b.setUp(ct, c)
	ct.build(b, c)
	b.finish()
	return b.s, b.err
}

// builder builds one seeded spec. Its random choices come from a PCG
// seeded with the scenario ID's SHA-256, so they're the same on every run;
// but specs are written once and committed, so nothing depends on that.
type builder struct {
	rng *rand.Rand
	// seq numbers the scenario among all 48, from 1, for its document
	// numbers; n numbers it within its type, from 0.
	seq, n int
	s      datagen.Spec
	adm    rules.Date
	dis    rules.Date
	next   int // the last line's number
	// operated is the operation's date, if there is one, and implants what
	// the implant invoice lists.
	operated *rules.Date
	implants []datagen.Implant
	vendor   datagen.Vendor
	err      error
}

func newBuilder(id string, seq, n int) *builder {
	sum := sha256.Sum256([]byte(id))
	src := rand.NewPCG(binary.BigEndian.Uint64(sum[:8]), binary.BigEndian.Uint64(sum[8:16]))
	return &builder{rng: rand.New(src), seq: seq, n: n} //nolint:gosec // reproducible choices for invented data, not security
}

// intn returns a whole number from lo to hi, both included.
func (b *builder) intn(lo, hi int) int { return lo + b.rng.IntN(hi-lo+1) }

func pick[T any](b *builder, xs []T) T { return xs[b.rng.IntN(len(xs))] }

// rs converts rupees to paise.
func rs(rupees int64) int64 { return rupees * 100 }

func (b *builder) fail(err error) {
	if err != nil && b.err == nil {
		b.err = err
	}
}

func (b *builder) days(d rules.Date, n int) rules.Date {
	v, err := d.AddDays(n)
	b.fail(err)
	return v
}

func (b *builder) months(d rules.Date, n int) rules.Date {
	v, err := d.AddMonths(n)
	b.fail(err)
	return v
}

// within returns a day from d to d+span-1.
func (b *builder) within(d rules.Date, span int) rules.Date {
	return b.days(d, b.rng.IntN(max(span, 1)))
}

// setUp chooses the policy, the cover, the admission, the insured person
// and the hospital.
func (b *builder) setUp(ct claimType, c condition) {
	start, err := rules.NewDate(2025, time.Month(1+(b.seq*5)%12), 1)
	b.fail(err)
	end := b.days(b.months(start, 12), -1)
	b.s.Policy = datagen.PolicySpec{WordingUIN: wordingUIN, PeriodStart: start.String(), PeriodEnd: end.String()}

	si := pick(b, []int64{rs(300000), rs(500000), rs(500000), rs(1000000)})
	since := b.months(start, -12*b.intn(4, 7))
	// Two accidents come in the first month of a first policy, which the
	// 30-day wait doesn't stop.
	firstYear := ct.Name == "accident-ambulance" && b.n%4 == 2
	if firstYear {
		since = start
		b.adm = b.days(start, b.intn(8, 25))
	} else {
		b.adm = b.days(start, b.intn(30, 300))
	}
	b.s.Cover = datagen.CoverSpec{Tranches: []datagen.TrancheSpec{{SumInsured: si, ContinuousSince: since.String()}}}
	if !firstYear && b.rng.IntN(3) == 0 {
		b.s.Cover.CumulativeBonus = si / 10
	}

	i := b.seq - 1
	first, gender := firstNamesM, "male"
	if i%2 == 0 {
		first, gender = firstNamesF, "female"
	}
	ages := [2]int{22, 70}
	switch ct.Name {
	case "cataract-day-care":
		ages = [2]int{58, 80}
	case "surgery-implant":
		ages = [2]int{45, 75}
	}
	age := b.intn(ages[0], ages[1])
	b.s.Insured = datagen.Insured{
		Name:         first[(i/2)%len(first)] + " " + surnames[(i/2+i/24*5)%len(surnames)],
		Gender:       gender,
		Age:          age,
		PolicyNumber: fmt.Sprintf("SYN-ASG-%d-%06d", start.Year(), 200000+b.seq),
		MemberID:     fmt.Sprintf("SYN-M-%06d", 600000+b.seq),
		DateOfBirth:  b.birth(age).String(),
	}
	if ct.Name == "cataract-day-care" {
		b.s.Hospital = seededEyeHospitals[b.n%len(seededEyeHospitals)]
	} else {
		b.s.Hospital = seededHospitals[b.seq%len(seededHospitals)]
	}
	b.s.Hospital.Registration = fmt.Sprintf("SYN-HR-%04d", 2000+b.seq)
	b.s.Clinical = datagen.Clinical{Diagnosis: c.Diagnosis, Procedure: c.Procedure, History: c.History, Course: c.Course, Advice: c.Advice}
	b.s.Facts.DifferentialBilling = string(rules.AnswerYes)
	if b.rng.IntN(5) == 0 {
		b.s.Facts.DifferentialBilling = string(rules.AnswerNo)
	}
	b.s.Facts.Condition = datagen.ConditionSpec{ListedGroup: c.Listed, PreExisting: c.PreExisting}
	b.vendor = pick(b, vendors)
}

// birth chooses a date of birth that makes the person age years old at the
// admission.
func (b *builder) birth(age int) rules.Date {
	m := time.Month(1 + b.rng.IntN(int(b.adm.Month())))
	d := 1 + b.rng.IntN(28)
	if m == b.adm.Month() && d > b.adm.Day() {
		d = b.adm.Day()
	}
	dob, err := rules.NewDate(b.adm.Year()-age, m, d)
	b.fail(err)
	return dob
}

// stay sets a stay of nights nights in one room segment.
func (b *builder) stay(nights int) {
	b.dis = b.days(b.adm, nights)
	b.s.Facts.Segments = []datagen.SegmentSpec{{From: b.adm.String(), To: b.dis.String(), Kind: string(rules.SegmentRoom)}}
}

// onset sets the condition's onset days before the admission.
func (b *builder) onset(days int) { b.s.Facts.Condition.Onset = b.days(b.adm, -days).String() }

// lineOpt changes a line as it's added.
type lineOpt func(*datagen.LineSpec, *datagen.LineFormat)

// tied ties a line to a treatment with a sub-limit.
func tied(kind rules.TreatmentKind, eye rules.Eye) lineOpt {
	return func(l *datagen.LineSpec, _ *datagen.LineFormat) {
		l.Treatment = datagen.TreatmentSpec{Kind: string(kind), Eye: string(eye)}
	}
}

// listedAs matches a line to an Annexure A item, which it prints by the
// item's name in the wording.
func listedAs(key string) lineOpt {
	return func(l *datagen.LineSpec, lf *datagen.LineFormat) {
		for _, a := range datagen.AnnexureItems() {
			if a.Key() == key {
				l.ItemList, lf.Annexure, lf.Description = a.List, key, a.Name
			}
		}
	}
}

// line adds a facts line printed on a bill and returns its ID.
func (b *builder) line(bill string, cat rules.Category, on rules.Date, qty, unit int64, desc string, opts ...lineOpt) string {
	b.next++
	id := fmt.Sprintf("L%02d", b.next)
	l := datagen.LineSpec{ID: id, Category: string(cat), ServiceDate: on.String(), Quantity: qty, UnitPrice: unit, Amount: qty * unit}
	lf := datagen.LineFormat{Line: id, Bill: bill, Description: desc}
	for _, o := range opts {
		o(&l, &lf)
	}
	b.s.Facts.Lines = append(b.s.Facts.Lines, l)
	b.s.Lines = append(b.s.Lines, lf)
	return id
}

// room bills days of room rent and nursing from a day, and sometimes the
// patient's diet.
func (b *builder) room(from rules.Date, days int) {
	d := int64(days)
	b.line("B1", rules.CategoryRoomRent, from, d, rs(pick(b, []int64{2500, 3000, 3500, 4000, 4500, 5500})), pick(b, roomTypes))
	b.line("B1", rules.CategoryNursing, from, d, rs(pick(b, []int64{500, 600, 800, 900})), "Nursing Charges")
	if b.rng.IntN(2) == 0 {
		b.line("B1", rules.CategoryPatientDiet, from, d, rs(pick(b, []int64{250, 300, 350})), "Patient Diet Charges")
	}
}

// visits bills a consultant's daily visits.
func (b *builder) visits(from rules.Date, days int) {
	b.line("B1", rules.CategoryProfessionalFees, from, int64(days), rs(pick(b, []int64{800, 1000, 1200, 1500})), "Consultant Visit Charges")
}

// tests bills each test once on a day of the span; a blood count may be
// repeated.
func (b *builder) tests(names []string, from rules.Date, span int) {
	for _, t := range names {
		qty := int64(1)
		if t == "Complete Blood Count" && span > 3 {
			qty = int64(b.intn(2, 3))
		}
		b.line("B1", rules.CategoryDiagnostics, b.within(from, span), qty, rs(testPrices[t]), t)
	}
}

// extras sometimes bills a bedside procedure, physiotherapy and an
// Annexure A item.
func (b *builder) extras(from rules.Date, span int) {
	if b.rng.IntN(2) == 0 {
		b.line("B1", rules.CategoryOtherHospitalCharges, b.within(from, span), 1, rs(pick(b, []int64{800, 1200, 1500})), "Bedside Procedure Charges")
	}
	if b.rng.IntN(3) == 0 {
		b.line("B1", rules.CategoryOtherHospitalCharges, from, int64(max(1, min(span, b.intn(2, 5)))), rs(500), "Physiotherapy Charges")
	}
	if b.rng.IntN(2) == 0 {
		ch := pick(b, listedChoices)
		b.line("B1", rules.CategoryOtherHospitalCharges, from, 1, rs(ch.Rupees), "", listedAs(ch.Key))
	}
}

// pharmacy bills k different drugs from pool, each once on a day of the
// span, in date order.
func (b *builder) pharmacy(bill string, from rules.Date, span, k int, pool []drug, opts ...lineOpt) {
	type issue struct {
		on rules.Date
		d  drug
	}
	var issues []issue
	for _, i := range b.rng.Perm(len(pool))[:min(k, len(pool))] {
		issues = append(issues, issue{b.within(from, span), pool[i]})
	}
	slices.SortStableFunc(issues, func(x, y issue) int { return x.on.Compare(y.on) })
	for _, is := range issues {
		b.line(bill, rules.CategoryPharmacyConsumables, is.on, int64(b.intn(1, int(is.d.Max))), is.d.Paise, is.d.Name, opts...)
	}
}

// course bills m drugs each issued on two or three different days of the
// span, in date order, as a long stay's ward pharmacy does.
func (b *builder) course(bill string, from rules.Date, span, m int) {
	type issue struct {
		day int
		d   drug
	}
	var issues []issue
	for _, i := range b.rng.Perm(len(drugs))[:m] {
		for _, day := range b.rng.Perm(span)[:b.intn(2, 3)] {
			issues = append(issues, issue{day, drugs[i]})
		}
	}
	slices.SortStableFunc(issues, func(x, y issue) int { return x.day - y.day })
	for _, is := range issues {
		b.line(bill, rules.CategoryPharmacyConsumables, b.days(from, is.day), int64(b.intn(1, int(is.d.Max))), is.d.Paise, is.d.Name)
	}
}

// operate bills an operation on a day and records it for the OT notes and
// any implant invoice.
func (b *builder) operate(op *operation, on rules.Date, surgeon string) {
	b.operated = &on
	b.line("B1", rules.CategoryProfessionalFees, on, 1, rs(op.SurgeonFee+1000*int64(b.intn(0, 5))), "Surgeon Fees")
	if op.Anaesthesia != "Local" {
		b.line("B1", rules.CategoryProfessionalFees, on, 1, rs(op.SurgeonFee*3/10/100*100), "Anaesthetist Fees")
	}
	b.line("B1", rules.CategoryOperationTheatre, on, 1, rs(op.TheatreFee), op.Theatre)
	if op.Implant != "" {
		price := int64(b.intn(int(op.ImplantPrice[0]/100), int(op.ImplantPrice[1]/100))) * 100
		id := b.line("B1", rules.CategoryImplantsDevices, on, 1, rs(price), op.Implant)
		b.implants = append(b.implants, datagen.Implant{Line: id, Serial: b.serial()})
	}
	b.s.Clinical.Surgery = &datagen.Surgery{
		Date: on.String(), Surgeon: surgeon, Anaesthetist: pick(b, anaesthetists),
		Anaesthesia: op.Anaesthesia, Findings: op.Findings,
	}
}

const alnum = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

func (b *builder) code(n int) string {
	out := make([]byte, n)
	for i := range out {
		out[i] = alnum[b.rng.IntN(len(alnum))]
	}
	return string(out)
}

func (b *builder) serial() string { return "SYN-SN-" + b.code(6) }

func (b *builder) medical(c condition) {
	nights := b.intn(c.Nights[0], c.Nights[1])
	b.stay(nights)
	b.s.Clinical.TreatingDoctor = physicians[b.seq%len(physicians)]
	b.room(b.adm, nights)
	b.visits(b.adm, nights)
	b.tests(c.Tests, b.adm, nights)
	b.extras(b.adm, nights)
	b.pharmacy("P1", b.adm, nights, b.intn(5, 9), drugs)
	b.onset(b.intn(1, 7))
	b.s.ClaimForm.AdmissionType = pick(b, []string{"emergency", "emergency", "planned"})
}

func (b *builder) surgery(c condition) {
	nights := b.intn(c.Nights[0], c.Nights[1])
	b.stay(nights)
	surgeon := surgeons[b.seq%len(surgeons)]
	b.s.Clinical.TreatingDoctor = surgeon
	b.room(b.adm, nights)
	b.operate(c.Surgery, b.days(b.adm, b.rng.IntN(2)), surgeon)
	b.visits(b.days(b.adm, 1), nights-1)
	b.tests(c.Tests, b.adm, 1)
	b.extras(b.adm, nights)
	b.pharmacy("P1", b.adm, nights, b.intn(4, 7), drugs)
	b.onset(b.intn(60, 400))
	b.s.ClaimForm.AdmissionType = "planned"
}

func (b *builder) cataract(c condition) {
	eye, side := rules.EyeRight, "Right"
	if b.n%2 == 1 {
		eye, side = rules.EyeLeft, "Left"
	}
	b.stay(0)
	surgeon := surgeons[b.seq%len(surgeons)]
	b.s.Clinical = datagen.Clinical{
		Diagnosis: c.Diagnosis + ", " + strings.ToLower(side) + " eye", Procedure: c.Procedure + ", " + strings.ToLower(side) + " eye",
		TreatingDoctor: surgeon, History: cataractCare.History, Course: cataractCare.Course, Advice: cataractCare.Advice,
	}
	b.s.Facts.Condition.ListedGroup = "listed-24-months"
	tie := tied(rules.TreatmentCataract, eye)
	suffix := " - " + side + " Eye"
	on := b.adm
	b.line("B1", rules.CategoryRoomRent, on, 1, rs(pick(b, []int64{1500, 2000, 2500})), "Day Care Bed Charges"+suffix, tie)
	b.line("B1", rules.CategoryDiagnostics, on, 1, rs(1500), "Biometry and A-Scan"+suffix, tie)
	b.line("B1", rules.CategoryProfessionalFees, on, 1, rs(pick(b, []int64{15000, 18000, 20000, 25000})), "Cataract Surgery - Surgeon Fees"+suffix, tie)
	b.line("B1", rules.CategoryOperationTheatre, on, 1, rs(pick(b, []int64{5000, 6000, 8000})), "OT Charges - Cataract"+suffix, tie)
	iol := b.line("B1", rules.CategoryImplantsDevices, on, 1, rs(pick(b, []int64{8000, 12000, 16000, 22000})), "Foldable IOL"+suffix, tie)
	b.implants = []datagen.Implant{{Line: iol, Serial: b.serial()}}
	if b.rng.IntN(2) == 0 {
		b.line("B1", rules.CategoryOtherHospitalCharges, on, 1, rs(250), "", listedAs(pick(b, []string{"list-iii/3", "list-iii/15"})))
	}
	if b.n%3 == 0 {
		b.line("B1", rules.CategoryPharmacyConsumables, on, 1, rs(pick(b, []int64{1500, 2000, 2500})), "Eye Drops and Consumables"+suffix, tie)
	} else {
		b.pharmacy("P1", on, 1, b.intn(2, 3), eyeDrugs, tie)
	}
	b.operated = &on
	b.s.Clinical.Surgery = &datagen.Surgery{
		Date: on.String(), Surgeon: surgeon, Anaesthetist: pick(b, anaesthetists), Anaesthesia: "Topical",
		Findings: "A dense cataract of the " + strings.ToLower(side) + " eye. The lens was removed by phacoemulsification through a 2.8 mm incision and a foldable lens placed in the bag.",
	}
	b.vendor = lensVendor
	b.onset(b.intn(150, 500))
	b.s.ClaimForm.AdmissionType = "planned"
}

func (b *builder) icu(c condition) {
	icuDays, roomDays := b.intn(2, 4), b.intn(2, 4)
	icuEnd := b.days(b.adm, icuDays-1)
	roomFrom := b.days(icuEnd, 1)
	b.dis = b.days(roomFrom, roomDays-1)
	b.s.Facts.Segments = []datagen.SegmentSpec{
		{From: b.adm.String(), To: icuEnd.String(), Kind: string(rules.SegmentICU)},
		{From: roomFrom.String(), To: b.dis.String(), Kind: string(rules.SegmentRoom)},
	}
	nights := icuDays + roomDays - 1
	b.s.Clinical.TreatingDoctor = physicians[b.seq%len(physicians)]
	b.line("B1", rules.CategoryICUCharges, b.adm, int64(icuDays), rs(pick(b, []int64{6000, 7500, 9000, 12000})), "ICU Bed Charges")
	b.line("B1", rules.CategoryICUCharges, b.adm, int64(icuDays), rs(pick(b, []int64{1500, 2000, 2500})), "Intensivist Charges")
	b.line("B1", rules.CategoryOtherHospitalCharges, b.adm, int64(icuDays), rs(pick(b, []int64{1500, 2000})), "Equipment Charges")
	b.room(roomFrom, roomDays-1)
	b.visits(b.adm, nights)
	b.tests(c.Tests, b.adm, icuDays)
	b.extras(roomFrom, roomDays)
	b.pharmacy("P1", b.adm, nights, b.intn(8, 12), drugs)
	b.onset(b.intn(0, 2))
	b.s.ClaimForm.AdmissionType = "emergency"
}

func (b *builder) accident(c condition) {
	nights := b.intn(c.Nights[0], c.Nights[1])
	b.stay(nights)
	surgeon := surgeons[b.seq%len(surgeons)]
	b.s.Clinical.TreatingDoctor = surgeon
	b.s.Facts.Condition.Accident = true
	b.line("B1", rules.CategoryRoadAmbulance, b.adm, 1, rs(pick(b, []int64{1500, 1800, 2200, 2500, 3000})), "Road Ambulance Charges")
	b.line("B1", rules.CategoryOtherHospitalCharges, b.adm, 1, rs(pick(b, []int64{1000, 1500})), "Emergency Room Charges")
	b.room(b.adm, nights)
	if c.Surgery != nil {
		b.operate(c.Surgery, b.days(b.adm, b.rng.IntN(2)), surgeon)
	}
	b.visits(b.adm, nights)
	b.tests(c.Tests, b.adm, 1)
	b.extras(b.adm, nights)
	b.pharmacy("P1", b.adm, nights, b.intn(4, 8), drugs)
	b.onset(0)
	b.s.ClaimForm.AdmissionType = "emergency"
}

func (b *builder) long(c condition) {
	nights := b.intn(10, 16)
	b.stay(nights)
	b.s.Clinical.TreatingDoctor = physicians[b.seq%len(physicians)]
	b.room(b.adm, nights)
	b.visits(b.adm, nights)
	b.tests(c.Tests, b.adm, nights)
	b.extras(b.adm, nights)
	b.course("P1", b.adm, nights, b.intn(9, 12))
	if b.n%2 == 0 {
		// The medicines to take home, on a bill of their own.
		b.pharmacy("P2", b.dis, 1, b.intn(3, 4), drugs)
	}
	b.onset(b.intn(5, 20))
	b.s.ClaimForm.AdmissionType = pick(b, []string{"emergency", "planned"})
}

// finish adds the documents, the files and the claim form's values.
func (b *builder) finish() {
	count := map[string]int{}
	gross := map[string]int64{}
	for i, lf := range b.s.Lines {
		count[lf.Bill]++
		gross[lf.Bill] += b.s.Facts.Lines[i].Amount
	}
	if b.rng.IntN(4) == 0 {
		b.s.Facts.BillDiscount = gross["B1"] / 20 / 100 * 100
	}
	signed := b.days(b.dis, b.intn(2, 9))
	seq := 2000 + b.seq
	docs := []datagen.Document{
		{ID: "CF", Type: datagen.DocClaimForm, Number: fmt.Sprintf("SYN-CF-%06d", 200000+b.seq), Date: signed.String()},
		{ID: "ID", Type: datagen.DocPhotoID, Number: fmt.Sprintf("SYN-ID-%s-%04d", b.code(4), b.rng.IntN(10000)), Date: b.days(b.adm, -b.intn(400, 3000)).String()},
		{ID: "DS", Type: datagen.DocDischargeSummary, Number: fmt.Sprintf("SYN-DS-%06d", 200000+b.seq), Date: b.dis.String()},
	}
	if b.operated != nil {
		docs = append(docs, datagen.Document{ID: "OT", Type: datagen.DocOTNotes, Number: fmt.Sprintf("SYN-OT-%06d", 200000+b.seq), Date: b.operated.String()})
	}
	docs = append(docs, datagen.Document{
		ID: "B1", Type: datagen.DocHospitalBill, Layout: datagen.Layouts()[b.seq%3], Number: fmt.Sprintf("SYN-IP-%04d01", seq),
		Date: b.dis.String(), LinesPerPage: b.perPage(count["B1"], false), Discount: b.s.Facts.BillDiscount,
	})
	if len(b.implants) > 0 {
		v := b.vendor
		v.Registration = fmt.Sprintf("SYN-VR-%04d", seq)
		docs = append(docs, datagen.Document{
			ID: "IV", Type: datagen.DocImplantInvoice, Number: fmt.Sprintf("SYN-IV-%06d", 200000+b.seq),
			Date: b.days(*b.operated, -b.intn(0, 2)).String(), Vendor: &v, Implants: b.implants,
		})
	}
	bills := []string{"B1"}
	for i, p := range []string{"P1", "P2"} {
		if count[p] == 0 {
			continue
		}
		bills = append(bills, p)
		docs = append(docs, datagen.Document{
			ID: p, Type: datagen.DocPharmacyBill, Number: fmt.Sprintf("SYN-PH-%04d%02d", seq, i+1),
			Date: b.dis.String(), LinesPerPage: b.perPage(count[p], true),
		})
	}
	docs = append(docs, datagen.Document{
		ID: "R1", Type: datagen.DocPaymentReceipt, Number: fmt.Sprintf("SYN-RC-%04d01", seq),
		Date: b.dis.String(), Against: bills, Mode: pick(b, paymentModes),
	})
	b.s.Documents = docs
	b.s.Files = b.files(docs)

	var claimed int64
	for _, g := range gross {
		claimed += g
	}
	b.s.ClaimForm.SignedOn = signed.String()
	b.s.ClaimForm.ClaimedAmount = claimed - b.s.Facts.BillDiscount
	b.s.Facts.Admission, b.s.Facts.Discharge = b.adm.String(), b.dis.String()
}

// perPage chooses how many lines a bill's page holds: a hospital bill runs
// to one to three pages, a pharmacy bill to one, or more when it's long.
func (b *builder) perPage(n int, pharmacy bool) int {
	if pharmacy {
		if n <= 14 {
			return b.intn(max(n, 10), 20)
		}
		return b.intn(12, 16)
	}
	pages := 2
	switch {
	case n >= 14:
		pages = b.intn(2, 3)
	case n <= 6 && b.rng.IntN(2) == 0:
		pages = 1
	}
	return min(24, (n+pages-1)/pages)
}

// files arranges the documents in one file, in three, or each alone.
func (b *builder) files(docs []datagen.Document) []datagen.FileSpec {
	var ids []string
	for _, d := range docs {
		ids = append(ids, d.ID)
	}
	switch b.seq % 3 {
	case 0:
		return []datagen.FileSpec{{Name: "claim-packet.pdf", Documents: ids}}
	case 1:
		groups := []datagen.FileSpec{{Name: "claim-form.pdf"}, {Name: "hospital-records.pdf"}, {Name: "bills.pdf"}}
		for _, d := range docs {
			g := 2
			switch d.Type {
			case datagen.DocClaimForm, datagen.DocPhotoID:
				g = 0
			case datagen.DocDischargeSummary, datagen.DocOTNotes:
				g = 1
			case datagen.DocHospitalBill, datagen.DocPharmacyBill, datagen.DocPaymentReceipt, datagen.DocImplantInvoice:
			}
			groups[g].Documents = append(groups[g].Documents, d.ID)
		}
		return groups
	}
	names := map[string]string{
		"CF": "claim-form.pdf", "ID": "photo-id.pdf", "DS": "discharge-summary.pdf", "OT": "ot-notes.pdf",
		"B1": "hospital-bill.pdf", "IV": "implant-invoice.pdf", "P1": "pharmacy-bill.pdf",
		"P2": "pharmacy-bill-2.pdf", "R1": "payment-receipt.pdf",
	}
	var out []datagen.FileSpec
	for _, id := range ids {
		out = append(out, datagen.FileSpec{Name: names[id], Documents: []string{id}})
	}
	return out
}
