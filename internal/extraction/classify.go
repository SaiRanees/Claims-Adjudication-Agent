package extraction

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/documents"
	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/llm"
)

// PageType is a page's type, as page_classification.v1 names it. The types
// follow architecture.md's packet checklist.
type PageType string

// The page types, in the schema's order.
const (
	PageClaimForm           PageType = "claim_form"
	PagePhotoID             PageType = "photo_id"
	PageAdmissionAdvice     PageType = "admission_advice"
	PageHospitalBill        PageType = "hospital_bill"
	PagePharmacyBill        PageType = "pharmacy_bill"
	PagePaymentReceipt      PageType = "payment_receipt"
	PageDischargeSummary    PageType = "discharge_summary"
	PageInvestigationReport PageType = "investigation_report"
	PagePrescription        PageType = "prescription"
	PageOTNotes             PageType = "ot_notes"
	PageImplantInvoice      PageType = "implant_invoice"
	PageBankDetails         PageType = "bank_details"
	PageKYC                 PageType = "kyc"
	PageOther               PageType = "other"
)

// PageTypes lists every page type, in the schema's order.
func PageTypes() []PageType {
	return []PageType{
		PageClaimForm, PagePhotoID, PageAdmissionAdvice, PageHospitalBill, PagePharmacyBill,
		PagePaymentReceipt, PageDischargeSummary, PageInvestigationReport, PagePrescription,
		PageOTNotes, PageImplantInvoice, PageBankDetails, PageKYC, PageOther,
	}
}

// MaxInstructionSnippet caps an instruction-like snippet, from the model or
// from the code's scan, in characters.
const MaxInstructionSnippet = 200

// PageInput is one prepared page to classify.
type PageInput struct {
	// ID is how the claim names the page.
	ID documents.PageID
	// FileSHA256 is the uploaded file's SHA-256, which names the page in the
	// request with its number (ADR 0014).
	FileSHA256 string
	Page       documents.Page
}

// ClassifiedPage is a page's classification: the model's type and printed
// markers, and what code found beside it.
type ClassifiedPage struct {
	ID             documents.PageID
	Type           PageType
	PatientName    *string
	DocumentNumber *string
	DocumentDate   *string
	PageMarker     *string
	// ModelInstructionLike is what the model copied as instruction-like
	// text; CodeInstructionLike is what ScanInstructionLike found in the
	// page's text. Both are recorded; M3 routes them to review.
	ModelInstructionLike []string
	CodeInstructionLike  []InstructionHit
	HasTextLayer         bool
	// RepeatOf is the earlier page this one repeats, set by the caller from
	// documents.Repeats over the whole claim.
	RepeatOf *documents.PageID
	// RequestHash is the model request's hash, for its cassette and audit.
	RequestHash string
}

// ClassificationRequest builds the request that classifies one page: the
// page alone as a PDF, named by its file's SHA-256, its number and the
// split's derivation version, never by its bytes.
func ClassificationRequest(cfg llm.Config, in PageInput, sample int) (llm.Request, error) {
	part := llm.Part{
		MIMEType: llm.MIMEPDF,
		Data:     in.Page.PDF,
		Origin: &llm.Origin{
			FileSHA256: in.FileSHA256,
			Pages:      []int{in.Page.Number},
			Derivation: documents.PageDerivation,
		},
	}
	return NewRequest(cfg, JobPageClassification, []llm.Part{part}, sample)
}

// Classify asks the model for one page's type and markers, decodes the
// answer strictly and scans the page's text for instruction-like phrases.
// A stop other than the end, or an answer that doesn't fit the schema, is
// an error that carries the request's hash.
func Classify(ctx context.Context, client *llm.Client, cfg llm.Config, in PageInput) (ClassifiedPage, error) {
	req, err := ClassificationRequest(cfg, in, 0)
	if err != nil {
		return ClassifiedPage{}, err
	}
	res, err := client.Call(ctx, req)
	if err != nil {
		return ClassifiedPage{}, fmt.Errorf("classify %s: %w", in.ID, err)
	}
	if err := res.Failed(); err != nil {
		return ClassifiedPage{}, fmt.Errorf("classify %s, request %s: %w", in.ID, res.Hash, err)
	}
	var out PageClassification
	if err := llm.Decode(req.Schema, res.Output, &out); err != nil {
		return ClassifiedPage{}, fmt.Errorf("classify %s, request %s: %w", in.ID, res.Hash, err)
	}
	snippets := make([]string, 0, len(out.InstructionLikeText))
	for _, s := range out.InstructionLikeText {
		snippets = append(snippets, capSnippet(s))
	}
	return ClassifiedPage{
		ID:                   in.ID,
		Type:                 PageType(out.PageType),
		PatientName:          out.PatientName,
		DocumentNumber:       out.DocumentNumber,
		DocumentDate:         out.DocumentDate,
		PageMarker:           out.PageMarker,
		ModelInstructionLike: snippets,
		CodeInstructionLike:  ScanInstructionLike(in.Page.Text),
		HasTextLayer:         in.Page.HasTextLayer,
		RequestHash:          res.Hash,
	}, nil
}

// capSnippet keeps at most MaxInstructionSnippet characters.
func capSnippet(s string) string {
	if utf8.RuneCountInString(s) <= MaxInstructionSnippet {
		return s
	}
	return string([]rune(s)[:MaxInstructionSnippet])
}

// instructionPhrases are phrases that address a model rather than a reader.
// They are lower case with single spaces; the scan folds the page's text
// the same way. The list is short on purpose: M3's review sees every hit,
// so a phrase that ordinary medical or billing text uses would only add
// noise.
var instructionPhrases = []string{
	"ignore previous instructions",
	"ignore all previous",
	"ignore the above",
	"ignore your instructions",
	"disregard previous",
	"disregard all previous",
	"disregard the above",
	"disregard your instructions",
	"forget your instructions",
	"system prompt",
	"you are now",
	"you are an ai",
	"as an ai model",
	"as a language model",
	"developer mode",
	"approve this claim",
	"approve the claim",
	"pay this claim in full",
	"pay the full amount",
	"do not flag",
	"don't flag",
	"classify this page as",
	"classify this as",
	"classify this document as",
	"mark this claim as",
}

// InstructionHit is one instruction-like phrase found in a page's text, and
// the line it is on, capped at MaxInstructionSnippet characters.
type InstructionHit struct {
	Phrase  string `json:"phrase"`
	Snippet string `json:"snippet"`
}

// ScanInstructionLike looks for instructionPhrases in a page's text, line
// by line, ignoring case and runs of white space. Each phrase is reported
// once, at its first line, in the order the lines come.
func ScanInstructionLike(text string) []InstructionHit {
	var hits []InstructionHit
	seen := map[string]bool{}
	for line := range strings.Lines(text) {
		folded := strings.Join(strings.Fields(strings.ToLower(line)), " ")
		if folded == "" {
			continue
		}
		folded = strings.ReplaceAll(folded, "’", "'") // a typographic apostrophe
		for _, p := range instructionPhrases {
			if !seen[p] && strings.Contains(folded, p) {
				seen[p] = true
				hits = append(hits, InstructionHit{Phrase: p, Snippet: capSnippet(strings.TrimSpace(line))})
			}
		}
	}
	return hits
}
