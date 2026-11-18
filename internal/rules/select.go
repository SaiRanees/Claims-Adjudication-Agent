package rules

import (
	"errors"
	"fmt"
)

// The errors Registry returns.
var (
	ErrDuplicatePack          = errors.New("rules: two packs share a UIN and version, or an ID and version")
	ErrNoPack                 = errors.New("rules: no pack for the wording's UIN")
	ErrOutsideValidity        = errors.New("rules: the policy period starts outside the pack's validity")
	ErrInvalidPeriod          = errors.New("rules: invalid policy period")
	ErrAdmissionOutsidePeriod = errors.New("rules: the admission date is outside the policy period")
)

// PolicyPeriod is the policy period that contains a claim's admission date,
// with the UIN of the wording it was issued on.
type PolicyPeriod struct {
	WordingUIN string
	// Start and End are inclusive.
	Start Date
	End   Date
}

// Registry holds the published packs and picks one for a claim.
type Registry struct {
	packs []*Pack
}

// NewRegistry builds a registry. Two packs with the same UIN and version, or
// the same ID and version, are an error, because selection would be
// ambiguous or a result's identity wouldn't name one pack.
func NewRegistry(packs ...*Pack) (*Registry, error) {
	type key struct {
		name    string
		version int64
	}
	byUIN := map[key]bool{}
	byID := map[key]bool{}
	for i, p := range packs {
		if p == nil {
			return nil, fmt.Errorf("rules: pack %d is nil", i)
		}
		u := key{p.Wording.UIN, p.Version}
		id := key{p.ID, p.Version}
		if byUIN[u] {
			return nil, fmt.Errorf("%w: UIN %s, version %d", ErrDuplicatePack, u.name, u.version)
		}
		if byID[id] {
			return nil, fmt.Errorf("%w: ID %s, version %d", ErrDuplicatePack, id.name, id.version)
		}
		byUIN[u], byID[id] = true, true
	}
	return &Registry{packs: packs}, nil
}

// Packs returns the registry's packs in the order they were given.
func (r *Registry) Packs() []*Pack { return append([]*Pack(nil), r.packs...) }

// Select returns the pack for a claim admitted on admission, in period.
//
// The pack follows the policy, not the calendar: the period names its
// wording's UIN, and the newest version for that UIN applies. Its validity is
// checked against the period's start date, so a policy that hasn't renewed
// onto a revised wording keeps the wording it was issued on.
func (r *Registry) Select(period PolicyPeriod, admission Date) (*Pack, error) {
	if period.Start.IsZero() || period.End.IsZero() || period.End.Before(period.Start) {
		return nil, fmt.Errorf("%w: %s to %s", ErrInvalidPeriod, period.Start, period.End)
	}
	if admission.Before(period.Start) || admission.After(period.End) {
		return nil, fmt.Errorf("%w: %s is outside %s to %s", ErrAdmissionOutsidePeriod, admission, period.Start, period.End)
	}
	var newest *Pack
	for _, p := range r.packs {
		if p.Wording.UIN == period.WordingUIN && (newest == nil || p.Version > newest.Version) {
			newest = p
		}
	}
	if newest == nil {
		return nil, fmt.Errorf("%w: %q", ErrNoPack, period.WordingUIN)
	}
	w := newest.Wording
	if period.Start.Before(w.ValidFrom) || (!w.ValidUntil.IsZero() && period.Start.After(w.ValidUntil)) {
		return nil, fmt.Errorf("%w: %s starts %s; %s is valid from %s to %s",
			ErrOutsideValidity, period.WordingUIN, period.Start, newest.Identity(), w.ValidFrom, until(w.ValidUntil))
	}
	return newest, nil
}

func until(d Date) string {
	if d.IsZero() {
		return "no end date"
	}
	return d.String()
}
