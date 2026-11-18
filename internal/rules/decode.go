package rules

import (
	"math"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/rules/money"
)

// decoder holds what later sections check against earlier ones.
type decoder struct {
	pack      *Pack
	documents map[string]Document
	clauses   map[string]Clause
	ruleIDs   map[string]bool
	// categories maps each category listed so far to where it was listed.
	categories map[Category]string
}

func decodePack(root *yaml.Node) (*Pack, error) {
	top, err := mapping("", root)
	if err != nil {
		return nil, err
	}
	if err := top.allow("id", "version", "wording", "documents", "clauses", "steps"); err != nil {
		return nil, err
	}
	d := &decoder{
		pack:       &Pack{},
		documents:  map[string]Document{},
		clauses:    map[string]Clause{},
		ruleIDs:    map[string]bool{},
		categories: map[Category]string{},
	}
	if d.pack.ID, _, err = top.id("id", kebabID); err != nil {
		return nil, err
	}
	if d.pack.Version, err = top.intIn("version", 1, math.MaxInt64); err != nil {
		return nil, err
	}
	// The sections are read in this order whatever their order in the file,
	// so clauses are known before the steps cite them.
	sections := []struct {
		key    string
		decode func(string, *yaml.Node) error
	}{
		{"wording", d.wording},
		{"documents", d.documentList},
		{"clauses", d.clauseList},
		{"steps", d.stepList},
	}
	for _, s := range sections {
		n, p, err := top.need(s.key)
		if err != nil {
			return nil, err
		}
		if err := s.decode(p, n); err != nil {
			return nil, err
		}
	}
	return d.pack, nil
}

func (d *decoder) wording(path string, n *yaml.Node) error {
	f, err := mapping(path, n)
	if err != nil {
		return err
	}
	if err := f.allow("uin", "valid_from", "valid_until"); err != nil {
		return err
	}
	w := &d.pack.Wording
	if w.UIN, _, err = f.id("uin", uinPattern); err != nil {
		return err
	}
	fromNode, fromPath, err := f.need("valid_from")
	if err != nil {
		return err
	}
	if w.ValidFrom, err = date(fromPath, fromNode); err != nil {
		return err
	}
	if untilNode, untilPath, ok := f.get("valid_until"); ok {
		if w.ValidUntil, err = date(untilPath, untilNode); err != nil {
			return err
		}
		if w.ValidUntil.Before(w.ValidFrom) {
			return fail(untilPath, untilNode, ErrInvalidValue, "%s is before valid_from %s", w.ValidUntil, w.ValidFrom)
		}
	}
	return nil
}

func (d *decoder) documentList(path string, n *yaml.Node) error {
	items, err := sequence(path, n)
	if err != nil {
		return err
	}
	if len(items) == 0 {
		return fail(path, n, ErrInvalidValue, "list at least the wording")
	}
	for i, item := range items {
		ip := index(path, i)
		f, err := mapping(ip, item)
		if err != nil {
			return err
		}
		if err := f.allow("id", "title", "pages"); err != nil {
			return err
		}
		var doc Document
		var idNode *yaml.Node
		if doc.ID, idNode, err = f.id("id", kebabID); err != nil {
			return err
		}
		if _, dup := d.documents[doc.ID]; dup {
			return fail(join(ip, "id"), idNode, ErrDuplicateID, "document %q", doc.ID)
		}
		if doc.Title, err = d.title(f); err != nil {
			return err
		}
		if doc.Pages, err = f.intIn("pages", 1, 10_000); err != nil {
			return err
		}
		d.documents[doc.ID] = doc
		d.pack.Documents = append(d.pack.Documents, doc)
	}
	return nil
}

func (d *decoder) title(f *fields) (string, error) {
	s, err := f.str("title")
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(s) == "" {
		n, p, _ := f.get("title")
		return "", fail(p, n, ErrInvalidValue, "the title is empty")
	}
	return s, nil
}

func (d *decoder) clauseList(path string, n *yaml.Node) error {
	items, err := sequence(path, n)
	if err != nil {
		return err
	}
	for i, item := range items {
		ip := index(path, i)
		f, err := mapping(ip, item)
		if err != nil {
			return err
		}
		if err := f.allow("id", "document", "page", "last_page", "title"); err != nil {
			return err
		}
		var c Clause
		var idNode *yaml.Node
		if c.ID, idNode, err = f.id("id", clauseID); err != nil {
			return err
		}
		if _, dup := d.clauses[c.ID]; dup {
			return fail(join(ip, "id"), idNode, ErrDuplicateID, "clause %q", c.ID)
		}
		docNode, docPath, err := f.need("document")
		if err != nil {
			return err
		}
		if c.Document, err = str(docPath, docNode); err != nil {
			return err
		}
		doc, ok := d.documents[c.Document]
		if !ok {
			return fail(docPath, docNode, ErrInvalidValue, "no document %q", c.Document)
		}
		if c.Page, err = f.intIn("page", 1, doc.Pages); err != nil {
			return err
		}
		if c.LastPage, _, err = f.optIntIn("last_page", c.Page+1, doc.Pages); err != nil {
			return err
		}
		if c.Title, err = d.title(f); err != nil {
			return err
		}
		d.clauses[c.ID] = c
		d.pack.Clauses = append(d.pack.Clauses, c)
	}
	return nil
}

// Every key a step may have; ratio_basis and the category lists belong only to
// StepApplyProportionateDeduction.
var stepKeys = []string{"step", "rules", "ratio_basis", "scaled_categories", "exempt_categories"}

func (d *decoder) stepList(path string, n *yaml.Node) error {
	items, err := sequence(path, n)
	if err != nil {
		return err
	}
	seen := map[StepName]string{}
	for i, item := range items {
		ip := index(path, i)
		f, err := mapping(ip, item)
		if err != nil {
			return err
		}
		if err := f.allow(stepKeys...); err != nil {
			return err
		}
		nameNode, namePath, err := f.need("step")
		if err != nil {
			return err
		}
		raw, err := str(namePath, nameNode)
		if err != nil {
			return err
		}
		name := StepName(raw)
		if !slices.Contains(StepNames(), name) {
			return fail(namePath, nameNode, ErrStep, "%q isn't a step", raw)
		}
		if first, dup := seen[name]; dup {
			return fail(namePath, nameNode, ErrStep, "%q is repeated; first at %s", raw, first)
		}
		seen[name] = namePath
		step, err := d.step(ip, f, name)
		if err != nil {
			return err
		}
		d.pack.Steps = append(d.pack.Steps, step)
	}
	for _, name := range StepNames() {
		if _, ok := seen[name]; !ok {
			return fail(path, n, ErrStep, "%q is missing", name)
		}
	}
	for _, c := range Categories() {
		if _, ok := d.categories[c]; !ok {
			return fail(path, n, ErrCategory, "%q is under no treatment", c)
		}
	}
	return nil
}

func (d *decoder) step(path string, f *fields, name StepName) (Step, error) {
	s := Step{Name: name}
	if name == StepApplyProportionateDeduction {
		basisNode, basisPath, err := f.need("ratio_basis")
		if err != nil {
			return s, err
		}
		raw, err := str(basisPath, basisNode)
		if err != nil {
			return s, err
		}
		if s.RatioBasis, err = oneOf(basisPath, basisNode, raw, ratioBases()); err != nil {
			return s, err
		}
		if s.ScaledCategories, err = d.categoryList(f, "scaled_categories"); err != nil {
			return s, err
		}
		if s.ExemptCategories, err = d.categoryList(f, "exempt_categories"); err != nil {
			return s, err
		}
	} else if err := f.allow("step", "rules"); err != nil {
		return s, err
	}

	rulesNode, rulesPath, err := f.need("rules")
	if err != nil {
		return s, err
	}
	items, err := sequence(rulesPath, rulesNode)
	if err != nil {
		return s, err
	}
	targets := map[string]bool{}
	for i, item := range items {
		r, err := d.rule(index(rulesPath, i), item, name, targets)
		if err != nil {
			return s, err
		}
		s.Rules = append(s.Rules, r)
	}
	for _, t := range Targets(name) {
		if !targets[t] {
			return s, fail(rulesPath, rulesNode, ErrTarget, "no rule applies to %q", t)
		}
	}
	return s, nil
}

// categoryList reads a required list of categories, each listed nowhere else.
func (d *decoder) categoryList(f *fields, key string) ([]Category, error) {
	n, p, err := f.need(key)
	if err != nil {
		return nil, err
	}
	items, err := sequence(p, n)
	if err != nil {
		return nil, err
	}
	out := make([]Category, 0, len(items))
	for i, item := range items {
		ip := index(p, i)
		raw, err := str(ip, item)
		if err != nil {
			return nil, err
		}
		c := Category(raw)
		if !slices.Contains(Categories(), c) {
			return nil, fail(ip, item, ErrCategory, "%q isn't a category", raw)
		}
		if first, dup := d.categories[c]; dup {
			return nil, fail(ip, item, ErrCategory, "%q is listed twice; first at %s", raw, first)
		}
		d.categories[c] = ip
		out = append(out, c)
	}
	return out, nil
}

// Every key a rule may have, and the ones each step adds to the common keys.
var (
	commonRuleKeys = []string{"id", "clause", "unmodelled", "see_also", "applies_to"}
	allRuleKeys    = append(slices.Clone(commonRuleKeys),
		"days", "months", "waived_after_months", "effect", "categories",
		"share_of_sum_insured_bp", "ceiling_paise", "per", "rate_bp")
)

func ruleKeys(s StepName) []string {
	var extra []string
	switch s {
	case StepCheckEligibility:
		extra = []string{"days", "months", "waived_after_months"}
	case StepDropListedItems:
		extra = []string{"effect"}
	case StepApplyDailyCaps:
		extra = []string{"categories", "share_of_sum_insured_bp", "ceiling_paise"}
	case StepApplySubLimits:
		extra = []string{"share_of_sum_insured_bp", "ceiling_paise", "per"}
	case StepTakeCoPayment:
		extra = []string{"rate_bp"}
	case StepSetAsideUnmodelledLines, StepApplyProportionateDeduction,
		StepCapAtCoverLeft, StepTakeBillDiscount:
	}
	return append(slices.Clone(commonRuleKeys), extra...)
}

const (
	maxDays   = 36_500 // a century
	maxMonths = 1_200
)

func (d *decoder) rule(path string, n *yaml.Node, step StepName, targets map[string]bool) (Rule, error) {
	var r Rule
	f, err := mapping(path, n)
	if err != nil {
		return r, err
	}
	// A key no rule has is a typo; a key another step's rules have is
	// misplaced. Both are unknown here.
	if err := f.allow(allRuleKeys...); err != nil {
		return r, err
	}
	if err := f.allow(ruleKeys(step)...); err != nil {
		return r, err
	}

	var idNode *yaml.Node
	if r.ID, idNode, err = f.id("id", kebabID); err != nil {
		return r, err
	}
	if d.ruleIDs[r.ID] {
		return r, fail(join(path, "id"), idNode, ErrDuplicateID, "rule %q", r.ID)
	}
	d.ruleIDs[r.ID] = true

	if un, p, ok := f.get("unmodelled"); ok {
		if r.Unmodelled, err = boolean(p, un); err != nil {
			return r, err
		}
	}
	clauseNode, clausePath, hasClause := f.get("clause")
	switch {
	case r.Unmodelled && hasClause:
		return r, fail(clausePath, clauseNode, ErrInvalidValue, "an unmodelled rule cites no clause")
	case !r.Unmodelled && !hasClause:
		return r, fail(clausePath, n, ErrNoClause, "rule %q", r.ID)
	case hasClause:
		if r.Clause, err = d.clauseRef(clausePath, clauseNode); err != nil {
			return r, err
		}
	}
	if r.SeeAlso, err = d.seeAlso(f, r.Clause); err != nil {
		return r, err
	}

	targetNode, targetPath, err := f.need("applies_to")
	if err != nil {
		return r, err
	}
	if r.AppliesTo, err = str(targetPath, targetNode); err != nil {
		return r, err
	}
	if !slices.Contains(Targets(step), r.AppliesTo) {
		return r, fail(targetPath, targetNode, ErrTarget, "%q isn't one of %s", r.AppliesTo, strings.Join(Targets(step), ", "))
	}
	if targets[r.AppliesTo] {
		return r, fail(targetPath, targetNode, ErrTarget, "%q already has a rule", r.AppliesTo)
	}
	targets[r.AppliesTo] = true

	return r, d.ruleParams(path, f, step, &r)
}

// ruleParams reads the fields a rule's step adds.
func (d *decoder) ruleParams(path string, f *fields, step StepName, r *Rule) error {
	var err error
	switch step {
	case StepCheckEligibility:
		var hasDays, hasMonths bool
		if r.Days, hasDays, err = f.optIntIn("days", 1, maxDays); err != nil {
			return err
		}
		if r.Months, hasMonths, err = f.optIntIn("months", 1, maxMonths); err != nil {
			return err
		}
		switch {
		case hasDays && hasMonths:
			n, p, _ := f.get("months")
			return fail(p, n, ErrInvalidValue, "give days or months, not both")
		case !hasDays && !hasMonths:
			return fail(join(path, "days"), f.node, ErrMissingField, "give days or months")
		}
		r.WaivedAfterMonths, _, err = f.optIntIn("waived_after_months", 1, maxMonths)
		return err
	case StepDropListedItems:
		n, p, err := f.need("effect")
		if err != nil {
			return err
		}
		raw, err := str(p, n)
		if err != nil {
			return err
		}
		r.Effect, err = oneOf(p, n, raw, listEffects())
		return err
	case StepApplyDailyCaps:
		if r.Categories, err = d.categoryList(f, "categories"); err != nil {
			return err
		}
		if len(r.Categories) == 0 {
			n, p, _ := f.get("categories")
			return fail(p, n, ErrInvalidValue, "a bucket needs at least one category")
		}
		share, err := f.intIn("share_of_sum_insured_bp", 1, int64(money.FullRate))
		if err != nil {
			return err
		}
		ceiling, err := f.intIn("ceiling_paise", 1, math.MaxInt64)
		if err != nil {
			return err
		}
		r.Limit = Limit{Share: money.BasisPoints(share), HasShare: true, Ceiling: money.Paise(ceiling), HasCeiling: true}
		return nil
	case StepApplySubLimits:
		share, hasShare, err := f.optIntIn("share_of_sum_insured_bp", 1, int64(money.FullRate))
		if err != nil {
			return err
		}
		ceiling, hasCeiling, err := f.optIntIn("ceiling_paise", 1, math.MaxInt64)
		if err != nil {
			return err
		}
		if !hasShare && !hasCeiling {
			return fail(join(path, "ceiling_paise"), f.node, ErrMissingField, "give share_of_sum_insured_bp, ceiling_paise or both")
		}
		r.Limit = Limit{Share: money.BasisPoints(share), HasShare: hasShare, Ceiling: money.Paise(ceiling), HasCeiling: hasCeiling}
		n, p, err := f.need("per")
		if err != nil {
			return err
		}
		raw, err := str(p, n)
		if err != nil {
			return err
		}
		r.Per, err = oneOf(p, n, raw, periods())
		return err
	case StepTakeCoPayment:
		rate, err := f.intIn("rate_bp", 0, int64(money.FullRate))
		r.Rate = money.BasisPoints(rate)
		return err
	case StepSetAsideUnmodelledLines, StepApplyProportionateDeduction,
		StepCapAtCoverLeft, StepTakeBillDiscount:
	}
	return nil
}

func (d *decoder) clauseRef(path string, n *yaml.Node) (string, error) {
	id, err := str(path, n)
	if err != nil {
		return "", err
	}
	if _, ok := d.clauses[id]; !ok {
		return "", fail(path, n, ErrUndefinedClause, "%q", id)
	}
	return id, nil
}

func (d *decoder) seeAlso(f *fields, clause string) ([]string, error) {
	n, p, ok := f.get("see_also")
	if !ok {
		return nil, nil
	}
	items, err := sequence(p, n)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(items))
	for i, item := range items {
		ip := index(p, i)
		id, err := d.clauseRef(ip, item)
		if err != nil {
			return nil, err
		}
		if id == clause || slices.Contains(out, id) {
			return nil, fail(ip, item, ErrInvalidValue, "%q is cited twice", id)
		}
		out = append(out, id)
	}
	return out, nil
}
