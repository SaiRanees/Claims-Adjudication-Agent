package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/SaiRanees/Claims-Adjudication-Agent/evals/engine"
	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/datagen"
	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/rules"
)

// goldenSpecs writes one spec per golden case, once. A spec that exists is
// never overwritten: specs are committed and reviewed like golden cases, so
// a later change to this code can't quietly move them.
func goldenSpecs(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("golden-specs", flag.ContinueOnError)
	fs.SetOutput(stderr)
	out := fs.String("out", defaultSpecs, "directory to write the specs into")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		fmt.Fprint(stderr, usage)
		return errUsage
	}
	cases, err := engine.Cases()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(*out, 0o750); err != nil {
		return err
	}
	written := 0
	for i, c := range cases {
		path := filepath.Join(*out, goldenSpecID(c.ID)+".yaml")
		if _, err := os.Stat(path); err == nil {
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		src, err := goldenSpec(i, c)
		if err != nil {
			return fmt.Errorf("case %s: %w", c.ID, err)
		}
		// The spec must parse and render before it's written.
		s, err := datagen.ParseSpec(goldenSpecID(c.ID), src)
		if err != nil {
			return err
		}
		if _, err := datagen.Render(s); err != nil {
			return fmt.Errorf("%s: %w", s.ID, err)
		}
		if err := os.WriteFile(path, src, 0o600); err != nil {
			return err
		}
		written++
	}
	fmt.Fprintf(stdout, "wrote %d golden specs into %s; %d already existed\n", written, *out, len(cases)-written)
	return nil
}

// goldenSpecID is a golden case's scenario ID.
func goldenSpecID(caseID string) string { return "golden-" + caseID }

// listedLines prints each golden case's listed lines, keyed by case and
// line: the Annexure A item the case's comment names, and for cases 21 and
// 31, which match without the item's name, the description and how it
// matched.
var listedLines = map[string]datagen.LineFormat{
	"01-worked-example/L05":         {Annexure: "list-ii/27"},
	"18-annexure-list-i/L02":        {Annexure: "list-i/11"},
	"19-annexure-list-iii/L03":      {Annexure: "list-iii/11"},
	"20-annexure-list-iv/L02":       {Annexure: "list-iv/1"},
	"21-annexure-list-v/L02":        {Annexure: "list-v/23", Description: "Biomedical Waste Disposal Charges", MatchedBy: datagen.MatchedByExplanation},
	"24-listed-package-line/L02":    {Annexure: "list-ii/27"},
	"31-every-line-non-payable/L01": {Annexure: "list-iv/2", Description: "Observation Room Charges", MatchedBy: datagen.MatchedByStay},
	"31-every-line-non-payable/L02": {Annexure: "list-iv/2", Description: "Cardiac Evaluation Panel", MatchedBy: datagen.MatchedByStay},
	"31-every-line-non-payable/L03": {Annexure: "list-iv/2", Description: "Physician Consultation", MatchedBy: datagen.MatchedByStay},
	"32-package-cataract/L02":       {Annexure: "list-ii/27"},
}

// clinical is each golden case's condition, as its comment tells it. A case
// not listed is a medical admission from the default list.
var clinical = map[string]datagen.Clinical{
	"05": {Diagnosis: "Severe sepsis with acute kidney injury"},
	"06": {Diagnosis: "Acute inferior wall myocardial infarction"},
	"08": {Diagnosis: "Senile cataract", Procedure: "Phacoemulsification with foldable IOL"},
	"14": {Diagnosis: "Acute exacerbation of COPD with respiratory failure"},
	"17": {Diagnosis: "Acute appendicitis", Procedure: "Laparoscopic appendicectomy"},
	"28": {Diagnosis: "Senile cataract, both eyes", Procedure: "Phacoemulsification with foldable IOL"},
	"29": {Diagnosis: "Posterior capsule opacification", Procedure: "Nd:YAG capsulotomy"},
	"30": {Diagnosis: "Localised carcinoma of the prostate", Procedure: "Robotic radical prostatectomy"},
	"31": {Diagnosis: "Chest pain under evaluation"},
	"32": {Diagnosis: "Senile cataract", Procedure: "Phacoemulsification with foldable IOL"},
	"34": {Diagnosis: "Inguinal hernia", Procedure: "Mesh hernioplasty"},
	"35": {Diagnosis: "Inguinal hernia", Procedure: "Mesh hernioplasty"},
	"36": {Diagnosis: "Age-related osteoarthritis of the knee"},
	"37": {Diagnosis: "Age-related osteoarthritis of the knee"},
	"38": {Diagnosis: "Diabetic foot infection"},
	"39": {Diagnosis: "Diabetic foot infection"},
	"40": {Diagnosis: "Typhoid fever"},
	"41": {Diagnosis: "Typhoid fever"},
	"42": {Diagnosis: "Community-acquired pneumonia"},
	"43": {Diagnosis: "Fracture of the right distal radius after a fall", Procedure: "Closed reduction and casting"},
	"44": {Diagnosis: "Dengue fever"},
	"45": {Diagnosis: "Inguinal hernia", Procedure: "Mesh hernioplasty"},
	"46": {Diagnosis: "Inguinal hernia", Procedure: "Mesh hernioplasty"},
	"47": {Diagnosis: "Gall stones", Procedure: "Laparoscopic cholecystectomy"},
	"48": {Diagnosis: "Inguinal hernia", Procedure: "Mesh hernioplasty"},
	"49": {Diagnosis: "Acute bronchitis"},
	"50": {Diagnosis: "Community-acquired pneumonia"},
	"51": {Diagnosis: "Community-acquired pneumonia"},
	"52": {Diagnosis: "Senile cataract", Procedure: "Phacoemulsification with foldable IOL"},
}

var defaultDiagnoses = []string{
	"Acute gastroenteritis with dehydration",
	"Community-acquired pneumonia",
	"Viral fever with thrombocytopenia",
	"Acute pyelonephritis",
}

// Invented people and places. Any likeness to a real one is chance.
var (
	femaleNames = []string{"Meera Pillai", "Aarti Deshmane", "Kavya Raghunath", "Nisha Varkey", "Farida Sheikh", "Lalitha Venugopal"}
	maleNames   = []string{"Arjun Mehrotra", "Sanjay Kulkarni", "Imran Qadri", "Deepak Choudhury", "Harish Nambiar", "Vikram Sodhi"}
	doctors     = []string{"Dr. Ananya Rao", "Dr. Farhan Siddiqui", "Dr. Kavin Raman", "Dr. Leela Menon", "Dr. Rohit Bhalla", "Dr. Sunita Kher"}
	hospitals   = []datagen.Hospital{
		{Name: "Amberpur General Hospital", Address: "14 Lakeview Road, Ward 3", City: "Amberpur"},
		{Name: "Ravikund Multispeciality Hospital", Address: "Plot 7, Station Road", City: "Ravikund"},
		{Name: "Tarangpur City Hospital", Address: "22 Canal Street", City: "Tarangpur"},
		{Name: "Sundervan Care Centre", Address: "3 Temple Lane, Sector 4", City: "Sundervan"},
	}
	eyeHospital = datagen.Hospital{Name: "Drishtipur Eye Hospital", Address: "9 Market Road", City: "Drishtipur"}
)

// descriptions are each category's printed descriptions, taken in turn.
var descriptions = map[rules.Category][]string{
	rules.CategoryRoomRent:             {"Room Rent - Twin Sharing", "Room Charges - Private Room", "Ward Charges - General Ward", "Room Rent - Deluxe Single"},
	rules.CategoryNursing:              {"Nursing Charges", "Nursing Care Charges"},
	rules.CategoryPatientDiet:          {"Patient Diet Charges"},
	rules.CategoryICUCharges:           {"ICU Bed Charges", "Intensivist Charges", "ICU Nursing Charges"},
	rules.CategoryProfessionalFees:     {"Consultant Visit Charges", "Surgeon Fees", "Physician Consultation", "Anaesthetist Fees", "Specialist Visit Charges"},
	rules.CategoryOperationTheatre:     {"Operation Theatre Charges", "OT Charges"},
	rules.CategoryOtherHospitalCharges: {"Hospital Service Charges", "Bedside Procedure Charges", "Equipment Charges"},
	rules.CategoryPharmacyConsumables:  {"Pharmacy and Consumables", "Medicines and Consumables", "Drugs and Disposables"},
	rules.CategoryImplantsDevices:      {"Implant Charges", "Orthopaedic Implant"},
	rules.CategoryDiagnostics:          {"CT Scan Abdomen", "Complete Blood Count", "X-Ray Chest PA View", "Ultrasound Abdomen", "Laboratory Investigations"},
	rules.CategoryRoadAmbulance:        {"Road Ambulance Charges", "Ambulance Transfer Charges"},
}

// pharmacyItems are what a pharmacy bill prints, taken in turn.
var pharmacyItems = []string{"Inj. Ceftriaxone 1 g", "Tab. Paracetamol 650 mg", "IV Fluid Ringer Lactate 500 ml", "Inj. Pantoprazole 40 mg", "Surgical Consumables Kit"}

// treated is what a line tied to a treatment prints, by category.
var treated = map[rules.TreatmentKind]map[rules.Category]string{
	rules.TreatmentCataract: {
		rules.CategoryRoomRent:             "Day Care Bed Charges",
		rules.CategoryProfessionalFees:     "Cataract Surgery - Surgeon Fees",
		rules.CategoryImplantsDevices:      "Foldable IOL",
		rules.CategoryOperationTheatre:     "OT Charges - Cataract",
		rules.CategoryPharmacyConsumables:  "Eye Drops and Consumables",
		rules.CategoryOtherHospitalCharges: "Cataract Surgery Package",
		rules.CategoryRoadAmbulance:        "Road Ambulance Charges",
	},
	rules.TreatmentModernTreatment: {
		rules.CategoryProfessionalFees:    "Robotic Surgery - Surgeon Fees",
		rules.CategoryOperationTheatre:    "Robotic Surgery - OT Charges",
		rules.CategoryPharmacyConsumables: "Robotic Surgery - Consumables",
	},
}

// goldenSpec chooses a golden case's presentation from its index, so the
// packets vary in layout, pages, files and bills, and writes the spec.
func goldenSpec(i int, c engine.Case) ([]byte, error) {
	num := strings.SplitN(c.ID, "-", 2)[0]
	n := i + 1
	f := c.Facts
	s := datagen.Spec{GoldenCase: c.ID}
	s.Policy, s.Cover, s.Facts = specBlocks(c)

	cl, ok := clinical[num]
	if !ok {
		cl = datagen.Clinical{Diagnosis: defaultDiagnoses[i%len(defaultDiagnoses)]}
	}
	cl.TreatingDoctor = doctors[i%len(doctors)]
	s.Clinical = cl

	female := i%2 == 0
	name := maleNames[(i/2)%len(maleNames)]
	gender := "male"
	if female {
		name, gender = femaleNames[(i/2)%len(femaleNames)], "female"
	}
	s.Insured = datagen.Insured{
		Name: name, Gender: gender, Age: 28 + (i*7)%48,
		PolicyNumber: fmt.Sprintf("SYN-ASG-%d-%06d", c.Period.Start.Year(), 100+n),
		MemberID:     fmt.Sprintf("SYN-M-%06d", 500+n),
	}
	cataract := slices.ContainsFunc(f.Lines, func(l rules.Line) bool { return l.Treatment.Kind == rules.TreatmentCataract })
	s.Hospital = hospitals[i%len(hospitals)]
	if cataract {
		s.Hospital = eyeHospital
	}
	s.Hospital.Registration = fmt.Sprintf("SYN-HR-%04d", n)

	discharge := f.Discharge.String()
	signed, err := f.Discharge.AddDays(3)
	if err != nil {
		return nil, err
	}
	admissionType := "planned"
	if f.Condition.Accident || (!cataract && cl.Procedure == "" && i%2 == 1) {
		admissionType = "emergency"
	}
	var billed int64
	for _, l := range f.Lines {
		billed += int64(l.Amount)
	}
	s.ClaimForm = datagen.ClaimForm{AdmissionType: admissionType, SignedOn: signed.String(), ClaimedAmount: billed - int64(f.BillDiscount)}

	// The bills: pharmacy lines go on a pharmacy bill in every other case,
	// when the hospital bill keeps lines enough for its discount.
	var pharmacyGross, otherGross int64
	for _, l := range f.Lines {
		if l.Category == rules.CategoryPharmacyConsumables && l.Treatment.Kind == rules.TreatmentNone && l.ItemList == "" {
			pharmacyGross += int64(l.Amount)
		} else {
			otherGross += int64(l.Amount)
		}
	}
	split := i%2 == 0 && pharmacyGross > 0 && otherGross >= int64(f.BillDiscount)
	perPage := []int{3, 2, 12, 4}[i%4]
	bill := datagen.Document{
		ID: "B1", Type: datagen.DocHospitalBill, Layout: datagen.Layouts()[i%3],
		Number: fmt.Sprintf("SYN-IP-%04d01", n), Date: discharge, LinesPerPage: perPage, Discount: int64(f.BillDiscount),
	}
	s.Documents = []datagen.Document{bill}
	against := []string{"B1"}
	if split {
		s.Documents = append(s.Documents, datagen.Document{
			ID: "P1", Type: datagen.DocPharmacyBill, Number: fmt.Sprintf("SYN-PH-%04d01", n), Date: discharge, LinesPerPage: 10,
		})
		against = append(against, "P1")
	}
	s.Documents = append(s.Documents, datagen.Document{
		ID: "R1", Type: datagen.DocPaymentReceipt, Number: fmt.Sprintf("SYN-RC-%04d01", n), Date: discharge,
		Against: against, Mode: []string{"UPI", "Card", "Cash", "NEFT"}[i%4],
	})

	// The files: everything in one, bills apart from the receipt, or each
	// document alone.
	switch i % 3 {
	case 0:
		ids := []string{}
		for _, d := range s.Documents {
			ids = append(ids, d.ID)
		}
		s.Files = []datagen.FileSpec{{Name: "claim-documents.pdf", Documents: ids}}
	case 1:
		s.Files = []datagen.FileSpec{{Name: "hospital-bill.pdf", Documents: []string{"B1", "R1"}}}
		if split {
			s.Files = append(s.Files, datagen.FileSpec{Name: "pharmacy-bill.pdf", Documents: []string{"P1"}})
		}
	default:
		s.Files = []datagen.FileSpec{{Name: "hospital-bill.pdf", Documents: []string{"B1"}}}
		if split {
			s.Files = append(s.Files, datagen.FileSpec{Name: "pharmacy-bill.pdf", Documents: []string{"P1"}})
		}
		s.Files = append(s.Files, datagen.FileSpec{Name: "payment-receipt.pdf", Documents: []string{"R1"}})
	}

	lines, err := lineFormats(i, c, split)
	if err != nil {
		return nil, err
	}
	s.Lines = lines
	return encodeSpec(c, s)
}

// lineFormats prints each facts line.
func lineFormats(i int, c engine.Case, split bool) ([]datagen.LineFormat, error) {
	used := map[rules.Category]int{}
	pharmacy := 0
	room := descriptions[rules.CategoryRoomRent][i%len(descriptions[rules.CategoryRoomRent])]
	var out []datagen.LineFormat
	for _, l := range c.Facts.Lines {
		lf := datagen.LineFormat{Line: l.ID, Bill: "B1"}
		listed, isListed := listedLines[c.ID+"/"+l.ID]
		switch {
		case isListed:
			lf.Annexure, lf.MatchedBy, lf.Description = listed.Annexure, listed.MatchedBy, listed.Description
			if lf.MatchedBy == datagen.MatchedByName {
				item, ok := annexureName(listed.Annexure)
				if !ok {
					return nil, fmt.Errorf("line %s: no annexure item %s", l.ID, listed.Annexure)
				}
				lf.Description = item
			}
		case l.ItemList != "":
			return nil, fmt.Errorf("line %s is on %s but has no entry in listedLines", l.ID, l.ItemList)
		case l.Treatment.Kind != rules.TreatmentNone:
			d, ok := treated[l.Treatment.Kind][l.Category]
			if !ok {
				return nil, fmt.Errorf("line %s: no description for %s %s", l.ID, l.Treatment.Kind, l.Category)
			}
			if l.Treatment.Eye != rules.EyeNone {
				d += " - " + capitalize(string(l.Treatment.Eye)) + " Eye"
			}
			lf.Description = d
		case l.Category == rules.CategoryPharmacyConsumables && split:
			lf.Bill = "P1"
			lf.Description = pharmacyItems[(i+pharmacy)%len(pharmacyItems)]
			pharmacy++
		case l.Category == rules.CategoryRoomRent:
			lf.Description = room
		default:
			pool := descriptions[l.Category]
			if len(pool) == 0 {
				return nil, fmt.Errorf("line %s: no descriptions for %s", l.ID, l.Category)
			}
			lf.Description = pool[(i+used[l.Category])%len(pool)]
			used[l.Category]++
		}
		if l.PackageRate && !isListed {
			lf.Description = strings.Replace(lf.Description, "Charges", "Package", 1)
			if !strings.Contains(lf.Description, "Package") {
				lf.Description += " Package"
			}
		}
		out = append(out, lf)
	}
	return out, nil
}

func annexureName(key string) (string, bool) {
	for _, a := range datagen.AnnexureItems() {
		if a.Key() == key {
			return a.Name, true
		}
	}
	return "", false
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// specBlocks copies a golden case's policy, cover and facts into the spec's
// blocks. A test checks they convert back to exactly the case's.
func specBlocks(c engine.Case) (datagen.PolicySpec, datagen.CoverSpec, datagen.FactsSpec) {
	p := datagen.PolicySpec{WordingUIN: c.Period.WordingUIN, PeriodStart: date(c.Period.Start), PeriodEnd: date(c.Period.End)}
	cv := datagen.CoverSpec{
		CumulativeBonus:     int64(c.Cover.CumulativeBonus),
		PaidThisYear:        int64(c.Cover.PaidThisYear),
		CataractUsed:        datagen.EyeSpec{Left: int64(c.Cover.CataractUsed.Left), Right: int64(c.Cover.CataractUsed.Right)},
		ModernTreatmentUsed: int64(c.Cover.ModernTreatmentUsed),
	}
	for _, t := range c.Cover.Tranches {
		cv.Tranches = append(cv.Tranches, datagen.TrancheSpec{SumInsured: int64(t.SumInsured), ContinuousSince: date(t.ContinuousSince)})
	}
	f := c.Facts
	fs := datagen.FactsSpec{
		Admission: date(f.Admission), Discharge: date(f.Discharge),
		DifferentialBilling: string(f.DifferentialBilling), BillDiscount: int64(f.BillDiscount),
		Condition: datagen.ConditionSpec{
			ListedGroup: f.Condition.ListedGroup, PreExisting: f.Condition.PreExisting,
			Accident: f.Condition.Accident, Onset: date(f.Condition.Onset),
		},
	}
	for _, seg := range f.Segments {
		fs.Segments = append(fs.Segments, datagen.SegmentSpec{From: date(seg.From), To: date(seg.To), Kind: string(seg.Kind)})
	}
	for _, l := range f.Lines {
		fs.Lines = append(fs.Lines, datagen.LineSpec{
			ID: l.ID, Category: string(l.Category), ServiceDate: date(l.ServiceDate),
			Quantity: l.Quantity, UnitPrice: int64(l.UnitPrice), Amount: int64(l.Amount),
			ItemList: l.ItemList, PackageRate: l.PackageRate,
			Treatment: datagen.TreatmentSpec{Kind: string(l.Treatment.Kind), Eye: string(l.Treatment.Eye)},
		})
	}
	return p, cv, fs
}

func date(d rules.Date) string {
	if d.IsZero() {
		return ""
	}
	return d.String()
}

// encodeSpec writes the spec as YAML with a header, each list entry on one
// line, as the golden cases are written.
func encodeSpec(c engine.Case, s datagen.Spec) ([]byte, error) {
	var node yaml.Node
	if err := node.Encode(s); err != nil {
		return nil, err
	}
	flowEntries(&node, map[string]bool{"segments": true, "lines": true, "documents": true, "files": true})
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "# The packet for golden case %s (evals/engine/golden/%s.yaml).\n", c.ID, c.ID)
	buf.WriteString("# Written once by `go run ./cmd/datagen golden-specs`, then reviewed and\n")
	buf.WriteString("# committed. policy, cover and facts are the case's, unchanged, which a test\n")
	buf.WriteString("# checks; everything else is how the packet presents them, and is invented.\n\n")
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&node); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// flowEntries prints the entries of the named sequences in flow style, one
// per line.
func flowEntries(n *yaml.Node, names map[string]bool) {
	if n.Kind == yaml.MappingNode {
		for k := 0; k+1 < len(n.Content); k += 2 {
			key, val := n.Content[k], n.Content[k+1]
			if names[key.Value] && val.Kind == yaml.SequenceNode {
				for _, e := range val.Content {
					e.Style = yaml.FlowStyle
				}
				continue
			}
			flowEntries(val, names)
		}
		return
	}
	for _, c := range n.Content {
		flowEntries(c, names)
	}
}
