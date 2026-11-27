package extraction

import (
	"embed"
	"fmt"

	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/llm"
	"github.com/SaiRanees/Claims-Adjudication-Agent/prompts"
)

// The jobs, as named in models.yaml, prompts/ and cassettes.
const (
	JobPageClassification = "page_classification"
	JobLineItems          = "line_items"
)

//go:embed schemas/*.json
var schemaFiles embed.FS

// PageClassification is page_classification.v1: one page's type and its
// printed markers, each as printed or null (sessions 5 and 6).
type PageClassification struct {
	PageType            string   `json:"page_type"`
	PatientName         *string  `json:"patient_name"`
	DocumentNumber      *string  `json:"document_number"`
	DocumentDate        *string  `json:"document_date"`
	PageMarker          *string  `json:"page_marker"`
	InstructionLikeText []string `json:"instruction_like_text"`
}

// LineItems is line_items.v1: a bill's printed totals and its charge lines,
// every value a string as printed or null (ADR 0015). Session 7 parses them.
type LineItems struct {
	Bill                BillHeader `json:"bill"`
	Lines               []Line     `json:"lines"`
	InstructionLikeText []string   `json:"instruction_like_text"`
}

// BillHeader is a bill's identity and totals as printed.
type BillHeader struct {
	BillNumber   *string `json:"bill_number"`
	BillDate     *string `json:"bill_date"`
	PatientName  *string `json:"patient_name"`
	HospitalName *string `json:"hospital_name"`
	GrossTotal   *string `json:"gross_total"`
	Discount     *string `json:"discount"`
	NetTotal     *string `json:"net_total"`
}

// Line is one charge as printed. Page counts within the PDF sent, from 1.
type Line struct {
	Page        int     `json:"page"`
	ServiceDate *string `json:"service_date"`
	Description string  `json:"description"`
	Quantity    *string `json:"quantity"`
	UnitPrice   *string `json:"unit_price"`
	Amount      *string `json:"amount"`
}

// Schema returns a job's schema at a version.
func Schema(job, version string) (llm.Schema, error) {
	b, err := schemaFiles.ReadFile("schemas/" + job + "." + version + ".json")
	if err != nil {
		return llm.Schema{}, fmt.Errorf("extraction: no schema %s.%s: %w", job, version, err)
	}
	return llm.Schema{ID: job, Version: version, JSON: b}, nil
}

// NewRequest builds a job's request from config: its model, thinking level
// and token cap, its prompt for that provider and its schema.
func NewRequest(cfg llm.Config, job string, parts []llm.Part, sample int) (llm.Request, error) {
	j, ok := cfg.Jobs[job]
	if !ok {
		return llm.Request{}, fmt.Errorf("extraction: no job %s in config", job)
	}
	text, err := prompts.Load(job, j.Provider, j.PromptVersion)
	if err != nil {
		return llm.Request{}, err
	}
	schema, err := Schema(job, j.SchemaVersion)
	if err != nil {
		return llm.Request{}, err
	}
	return cfg.BuildRequest(job, llm.Prompt{ID: job, Version: j.PromptVersion, Text: text}, schema, parts, sample)
}
