package rules_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/rules"
)

const (
	goDigitUIN = "GODHLIP24179V022324"
	// A made-up UIN for a revised wording, valid from 2025-01-01. The pack
	// under it is synthetic test data (ADR 0006).
	revisedUIN = "TESTUIN00002V032526"
)

// variant builds a pack from the real one with another UIN, version, ID and
// validity.
func variant(t *testing.T, uin string, version int, id, validFrom, validUntil string) *rules.Pack {
	t.Helper()
	src := goDigitSource(t)
	src = edit(t, src, "  uin: GODHLIP24179V022324\n", "  uin: "+uin+"\n")
	src = edit(t, src, "version: 1\n", fmt.Sprintf("version: %d\n", version))
	src = edit(t, src, "id: arogya-sanjeevani-godigit-2024\n", "id: "+id+"\n")
	validity := "  valid_from: " + validFrom + "\n"
	if validUntil != "" {
		validity += "  valid_until: " + validUntil + "\n"
	}
	src = edit(t, src, "  valid_from: 2023-04-01\n", validity)
	p, err := rules.ParsePack([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func date(t *testing.T, s string) rules.Date {
	t.Helper()
	d, err := rules.ParseDate(s)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestSelect(t *testing.T) {
	original := variant(t, goDigitUIN, 1, "original", "2023-04-01", "")
	originalV2 := variant(t, goDigitUIN, 2, "original", "2023-04-01", "")
	revised := variant(t, revisedUIN, 1, "revised", "2025-01-01", "")
	closed := variant(t, "TESTUIN00003V012021", 1, "closed", "2020-01-01", "2022-12-31")

	tests := []struct {
		name      string
		packs     []*rules.Pack
		uin       string
		start     string
		end       string
		admission string
		want      *rules.Pack
		wantErr   error
	}{
		{
			// The period began on the old wording before the revision, and the
			// claim comes after it. The policy hasn't renewed onto the revised
			// wording, so the old pack applies.
			name:  "period started before a revised wording",
			packs: []*rules.Pack{original, revised}, uin: goDigitUIN,
			start: "2024-10-01", end: "2025-09-30", admission: "2025-03-01",
			want: original,
		},
		{
			name:  "renewed onto the revised wording",
			packs: []*rules.Pack{original, revised}, uin: revisedUIN,
			start: "2025-10-01", end: "2026-09-30", admission: "2026-03-01",
			want: revised,
		},
		{
			name:  "newest version for the UIN",
			packs: []*rules.Pack{originalV2, original, revised}, uin: goDigitUIN,
			start: "2024-10-01", end: "2025-09-30", admission: "2025-03-01",
			want: originalV2,
		},
		{
			name:  "period starts on valid_from",
			packs: []*rules.Pack{revised}, uin: revisedUIN,
			start: "2025-01-01", end: "2025-12-31", admission: "2025-01-01",
			want: revised,
		},
		{
			name:  "period starts on valid_until",
			packs: []*rules.Pack{closed}, uin: "TESTUIN00003V012021",
			start: "2022-12-31", end: "2023-12-30", admission: "2023-12-30",
			want: closed,
		},
		{
			name:  "no pack for the UIN",
			packs: []*rules.Pack{original, revised}, uin: "TESTUIN00009V012526",
			start: "2025-01-01", end: "2025-12-31", admission: "2025-06-01",
			wantErr: rules.ErrNoPack,
		},
		{
			name:  "period starts before valid_from",
			packs: []*rules.Pack{original, revised}, uin: revisedUIN,
			start: "2024-12-31", end: "2025-12-30", admission: "2025-06-01",
			wantErr: rules.ErrOutsideValidity,
		},
		{
			name:  "period starts after valid_until",
			packs: []*rules.Pack{closed}, uin: "TESTUIN00003V012021",
			start: "2023-01-01", end: "2023-12-31", admission: "2023-06-01",
			wantErr: rules.ErrOutsideValidity,
		},
		{
			name:  "admission before the period",
			packs: []*rules.Pack{original}, uin: goDigitUIN,
			start: "2024-10-01", end: "2025-09-30", admission: "2024-09-30",
			wantErr: rules.ErrAdmissionOutsidePeriod,
		},
		{
			name:  "admission after the period",
			packs: []*rules.Pack{original}, uin: goDigitUIN,
			start: "2024-10-01", end: "2025-09-30", admission: "2025-10-01",
			wantErr: rules.ErrAdmissionOutsidePeriod,
		},
		{
			name:  "period ends before it starts",
			packs: []*rules.Pack{original}, uin: goDigitUIN,
			start: "2025-10-01", end: "2025-09-30", admission: "2025-10-01",
			wantErr: rules.ErrInvalidPeriod,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, err := rules.NewRegistry(tt.packs...)
			if err != nil {
				t.Fatal(err)
			}
			period := rules.PolicyPeriod{WordingUIN: tt.uin, Start: date(t, tt.start), End: date(t, tt.end)}
			got, err := r.Select(period, date(t, tt.admission))
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Select error = %v, want %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("Select = %v, want %v", identity(got), identity(tt.want))
			}
		})
	}
}

func identity(p *rules.Pack) string {
	if p == nil {
		return "no pack"
	}
	return p.Identity().String()
}

func TestSelectZeroPeriod(t *testing.T) {
	r, err := rules.NewRegistry(variant(t, goDigitUIN, 1, "original", "2023-04-01", ""))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Select(rules.PolicyPeriod{WordingUIN: goDigitUIN}, date(t, "2025-01-01")); !errors.Is(err, rules.ErrInvalidPeriod) {
		t.Errorf("Select with a zero period: error = %v, want ErrInvalidPeriod", err)
	}
}

func TestNewRegistryDuplicates(t *testing.T) {
	tests := []struct {
		name string
		a, b *rules.Pack
	}{
		{
			name: "same UIN and version",
			a:    variant(t, goDigitUIN, 1, "first", "2023-04-01", ""),
			b:    variant(t, goDigitUIN, 1, "second", "2023-04-01", ""),
		},
		{
			name: "same ID and version",
			a:    variant(t, goDigitUIN, 1, "same", "2023-04-01", ""),
			b:    variant(t, revisedUIN, 1, "same", "2025-01-01", ""),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := rules.NewRegistry(tt.a, tt.b); !errors.Is(err, rules.ErrDuplicatePack) {
				t.Errorf("NewRegistry error = %v, want ErrDuplicatePack", err)
			}
		})
	}
}
