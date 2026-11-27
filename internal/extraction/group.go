package extraction

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Part is a logical document: a type and a page range within one file.
type Part struct {
	File      string   `json:"file"`
	Type      PageType `json:"type"`
	FirstPage int      `json:"first_page"`
	LastPage  int      `json:"last_page"`
	// DocumentNumber is the first document number its pages print, or nil.
	DocumentNumber *string `json:"document_number"`
	// Relabelled lists pages the model typed differently that grouping put
	// in this part, because each printed the part's own document number or
	// continued its "page x of y". They are recorded for review.
	Relabelled []int `json:"relabelled,omitempty"`
}

// marker is a parsed "page x of y".
type marker struct{ x, of int }

var markerPattern = regexp.MustCompile(`(?i)\b(?:page|pg\.?)\s*(\d{1,4})\s*(?:of|/)\s*(\d{1,4})\b`)

// parseMarker reads "Page 2 of 3", "page 2/3" or "Pg. 2 of 3"; anything
// else is no marker.
func parseMarker(s *string) (marker, bool) {
	if s == nil {
		return marker{}, false
	}
	m := markerPattern.FindStringSubmatch(*s)
	if m == nil {
		return marker{}, false
	}
	x, err1 := strconv.Atoi(m[1])
	of, err2 := strconv.Atoi(m[2])
	if err1 != nil || err2 != nil || x < 1 || x > of {
		return marker{}, false
	}
	return marker{x, of}, true
}

// normNumber folds a printed document number for comparison: case and
// white space don't count.
func normNumber(s *string) string {
	if s == nil {
		return ""
	}
	return strings.ToUpper(strings.Join(strings.Fields(*s), ""))
}

// group is a part being built, with its pages.
type group struct {
	file   string
	typ    PageType
	pages  []ClassifiedPage
	number string // the first non-empty folded number
	relab  []int
}

func (g *group) add(p ClassifiedPage) {
	g.pages = append(g.pages, p)
	if g.number == "" {
		g.number = normNumber(p.DocumentNumber)
	}
}

// accepts reports whether p, of the group's own type, continues it under
// the plain rule: same file, numbers match or one is absent, and p isn't
// printed as page 1.
func (g *group) accepts(p ClassifiedPage) bool {
	if p.ID.File != g.file || p.Type != g.typ {
		return false
	}
	if n := normNumber(p.DocumentNumber); n != "" && g.number != "" && n != g.number {
		return false
	}
	if m, ok := parseMarker(p.PageMarker); ok && m.x == 1 {
		return false
	}
	return true
}

// continuedBy reports whether a single page p of another type shows that
// it belongs to g: it prints g's own number, or its marker follows g's
// last page's marker (or precedes g's first, when p comes before g).
func continuedBy(g *group, p ClassifiedPage, pBefore bool) bool {
	if p.ID.File != g.file {
		return false
	}
	if n := normNumber(p.DocumentNumber); n != "" && g.number != "" {
		return n == g.number
	}
	pm, ok := parseMarker(p.PageMarker)
	if !ok {
		return false
	}
	if pBefore {
		gm, ok := parseMarker(g.pages[0].PageMarker)
		return ok && gm.of == pm.of && gm.x == pm.x+1
	}
	if pm.x == 1 {
		return false
	}
	gm, ok := parseMarker(g.pages[len(g.pages)-1].PageMarker)
	return ok && gm.of == pm.of && pm.x == gm.x+1
}

// Group splits a claim's classified pages into parts. Grouping is code,
// never the model. The pages come in claim order: each file's pages
// together and ascending.
//
// Consecutive pages of one file form one part when they share a type and
// their document numbers match or are absent; a page printed as page 1
// starts a new part. Then a part of a single page whose type differs from
// a neighbour's joins that neighbour when the page prints the neighbour's
// own document number or continues its "page x of y", and the page doesn't
// outweigh the neighbour: one misread page doesn't split a bill. It joins
// the part before it if it can, else the one after, and the parts on
// either side join too if the plain rule now lets them. The page is listed
// in Relabelled.
func Group(pages []ClassifiedPage) ([]Part, error) {
	for i := 1; i < len(pages); i++ {
		a, b := pages[i-1].ID, pages[i].ID
		if a.File == b.File && b.Page != a.Page+1 {
			return nil, fmt.Errorf("extraction: %s follows %s; a file's pages must come together and in order", b, a)
		}
	}
	var groups []*group
	for _, p := range pages {
		if n := len(groups); n > 0 && groups[n-1].accepts(p) {
			groups[n-1].add(p)
			continue
		}
		g := &group{file: p.ID.File, typ: p.Type}
		g.add(p)
		groups = append(groups, g)
	}
	for changed := true; changed; {
		changed = false
		for i, g := range groups {
			if len(g.pages) != 1 {
				continue
			}
			p := g.pages[0]
			var into int
			switch {
			case i > 0 && groups[i-1].typ != p.Type && continuedBy(groups[i-1], p, false) &&
				!outweighs(g, groups[i-1]):
				into = i - 1
			case i+1 < len(groups) && groups[i+1].typ != p.Type && continuedBy(groups[i+1], p, true) &&
				outweighs(groups[i+1], g):
				into = i + 1
			default:
				continue
			}
			t := groups[into]
			t.relab = append(t.relab, p.ID.Page)
			if into < i {
				t.pages = append(t.pages, p)
			} else {
				t.pages = append([]ClassifiedPage{p}, t.pages...)
			}
			groups = append(groups[:i], groups[i+1:]...)
			changed = true
			break
		}
		if changed {
			groups = rejoin(groups)
		}
	}
	parts := make([]Part, 0, len(groups))
	for _, g := range groups {
		part := Part{
			File: g.file, Type: g.typ,
			FirstPage: g.pages[0].ID.Page, LastPage: g.pages[len(g.pages)-1].ID.Page,
			Relabelled: g.relab,
		}
		for _, p := range g.pages {
			if p.DocumentNumber != nil && normNumber(p.DocumentNumber) != "" {
				part.DocumentNumber = p.DocumentNumber
				break
			}
		}
		parts = append(parts, part)
	}
	return parts, nil
}

// outweighs reports whether a has more evidence for its type than b: more
// pages, or as many and a document number where b has none. When neither
// outweighs the other, a single page joins the part before it, so a bill
// whose last page is misread keeps its first page's type.
func outweighs(a, b *group) bool {
	if len(a.pages) != len(b.pages) {
		return len(a.pages) > len(b.pages)
	}
	return a.number != "" && b.number == ""
}

// rejoin merges neighbouring groups that the plain rule lets join, now
// that a relabelled page sits between them.
func rejoin(groups []*group) []*group {
	out := groups[:1]
	for _, g := range groups[1:] {
		last := out[len(out)-1]
		ok := g.file == last.file && g.typ == last.typ
		for _, p := range g.pages {
			if !ok {
				break
			}
			isRelabelled := false
			for _, r := range g.relab {
				if r == p.ID.Page {
					isRelabelled = true
				}
			}
			if !isRelabelled && !last.accepts(p) {
				ok = false
			}
		}
		if !ok {
			out = append(out, g)
			continue
		}
		for _, p := range g.pages {
			last.add(p)
		}
		last.relab = append(last.relab, g.relab...)
	}
	return out
}
