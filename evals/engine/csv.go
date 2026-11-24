package engine

import (
	"bytes"
	_ "embed" // golden-cases.csv
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strconv"

	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/rules"
)

// CSVPath is where the checked figures live, relative to this package.
const CSVPath = "golden-cases.csv"

// checkedCSV is golden-cases.csv, compiled in beside the case files, so the
// tests and cmd/evalrunner judge the same figures wherever they run.
//
//go:embed golden-cases.csv
var checkedCSV []byte

// Checked returns the checked figures in golden-cases.csv, grouped by case
// and sorted. A case with no rows is unchecked.
func Checked() (map[string][]Row, error) { return ParseCSV(checkedCSV) }

var csvHeader = []string{"case_id", "kind", "line_id", "code", "paise"}

var canonicalInt = regexp.MustCompile(`^(0|[1-9][0-9]*)$`)

// ParseCSV reads the checked figures, grouped by case and sorted. It accepts
// a UTF-8 byte-order mark and CRLF line endings, which Excel on Windows
// writes, and fails on any row that breaks the rules: a kind it doesn't
// know, a figure that isn't a non-negative integer, a line ID or code where
// the kind has none or missing where it needs one, or a repeated
// (case_id, kind, line_id, code).
func ParseCSV(data []byte) (map[string][]Row, error) {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	r := csv.NewReader(bytes.NewReader(data))
	r.FieldsPerRecord = len(csvHeader)
	header, err := r.Read()
	if err != nil {
		return nil, fmt.Errorf("%s: header: %w", CSVPath, err)
	}
	if !slices.Equal(header, csvHeader) {
		return nil, fmt.Errorf("%s: header is %v, want %v", CSVPath, header, csvHeader)
	}
	byCase := map[string][]Row{}
	seen := map[string]int{}
	for {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", CSVPath, err)
		}
		line, _ := r.FieldPos(0)
		row, err := parseRow(rec)
		if err != nil {
			return nil, fmt.Errorf("%s line %d: %w", CSVPath, line, err)
		}
		key := rec[0] + "|" + row.Key()
		if first, dup := seen[key]; dup {
			return nil, fmt.Errorf("%s line %d: repeats line %d", CSVPath, line, first)
		}
		seen[key] = line
		byCase[rec[0]] = append(byCase[rec[0]], row)
	}
	for id := range byCase {
		slices.SortFunc(byCase[id], compareRows)
	}
	return byCase, nil
}

func parseRow(rec []string) (Row, error) {
	caseID, kind, lineID, code, paise := rec[0], rec[1], rec[2], rec[3], rec[4]
	if caseID == "" {
		return Row{}, errors.New("no case_id")
	}
	row := Row{Kind: kind, LineID: lineID, Code: code}
	if paise != "" {
		if !canonicalInt.MatchString(paise) {
			return Row{}, fmt.Errorf("paise %q isn't a non-negative integer", paise)
		}
		v, err := strconv.ParseInt(paise, 10, 64)
		if err != nil {
			return Row{}, fmt.Errorf("paise %q: %w", paise, err)
		}
		row.Paise, row.HasPaise = v, true
	}

	// What each kind needs: a line ID, a code, a figure.
	var needLine, needCode, needPaise bool
	switch kind {
	case KindLinePayable:
		needLine, needPaise = true, true
	case KindAdjustment:
		needLine, needCode, needPaise = true, true, true
		if row.HasPaise && row.Paise == 0 {
			return Row{}, errors.New("a zero adjustment has no row")
		}
	case KindClaimPayable:
		needPaise = true
	case KindFlag:
		flag := rules.FlagCode(code)
		if !slices.Contains(rules.FlagCodes(), flag) {
			return Row{}, fmt.Errorf("%q isn't a flag code", code)
		}
		needLine, needCode, needPaise = flag.AboutLine(), true, flag.HasFigure()
	default:
		return Row{}, fmt.Errorf("kind %q isn't line_payable, adjustment, claim_payable or flag", kind)
	}
	switch {
	case needLine != (lineID != ""):
		return Row{}, fmt.Errorf("a %s row %s line_id", kind, needs(needLine))
	case needCode != (code != ""):
		return Row{}, fmt.Errorf("a %s row %s code", kind, needs(needCode))
	case needPaise != row.HasPaise:
		return Row{}, fmt.Errorf("a %s row %s paise", kind, needs(needPaise))
	}
	return row, nil
}

func needs(b bool) string {
	if b {
		return "needs a"
	}
	return "has no"
}
