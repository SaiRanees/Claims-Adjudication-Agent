package extraction

import "slices"

// KYCThresholdPaise is the claimed amount above which the packet needs KYC:
// ₹1 lakh (architecture.md, "Packet checklist").
const KYCThresholdPaise int64 = 1_00_000_00

// ClaimContext is what the checklist needs to know about the claim beyond
// its pages. Week 4 fills it from the discharge summary and the bills;
// the eval fills it from the truth.
type ClaimContext struct {
	Surgery       bool
	ImplantBilled bool
	ClaimedPaise  int64
}

// ChecklistItem is one required document and whether a part of its type
// is in the packet.
type ChecklistItem struct {
	Type    PageType `json:"type"`
	Present bool     `json:"present"`
}

// Checklist lists the documents a packet must hold, in a fixed order:
// a claim form, a discharge summary, a hospital bill and a payment
// receipt; OT notes for surgery; an implant invoice when an implant is
// billed; and KYC when the claimed amount is over ₹1 lakh. Asking for
// what's missing is week 4.
func Checklist(present []PageType, c ClaimContext) []ChecklistItem {
	required := []PageType{PageClaimForm, PageDischargeSummary, PageHospitalBill, PagePaymentReceipt}
	if c.Surgery {
		required = append(required, PageOTNotes)
	}
	if c.ImplantBilled {
		required = append(required, PageImplantInvoice)
	}
	if c.ClaimedPaise > KYCThresholdPaise {
		required = append(required, PageKYC)
	}
	items := make([]ChecklistItem, len(required))
	for i, t := range required {
		items[i] = ChecklistItem{Type: t, Present: slices.Contains(present, t)}
	}
	return items
}

// PartTypes lists the types of a packet's parts, each once, in the order
// they first appear.
func PartTypes(parts []Part) []PageType {
	var out []PageType
	for _, p := range parts {
		if !slices.Contains(out, p.Type) {
			out = append(out, p.Type)
		}
	}
	return out
}

// Missing lists the checklist's documents that aren't present.
func Missing(items []ChecklistItem) []PageType {
	var out []PageType
	for _, it := range items {
		if !it.Present {
			out = append(out, it.Type)
		}
	}
	return out
}
