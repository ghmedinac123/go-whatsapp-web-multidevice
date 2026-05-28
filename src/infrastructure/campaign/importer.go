package campaign

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"

	domainCampaign "github.com/aldinokemal/go-whatsapp-web-multidevice/domains/campaign"
	"github.com/xuri/excelize/v2"
)

// Supported import formats.
const (
	FormatCSV  = "csv"
	FormatXLSX = "xlsx"
	FormatJSON = "json"
)

// phoneAliases / nameAliases are the (lower-cased) column / key names treated as
// the phone and name fields. Everything else becomes a spintax variable.
var (
	phoneAliases = map[string]bool{
		"phone": true, "telefono": true, "teléfono": true, "celular": true,
		"numero": true, "número": true, "msisdn": true, "whatsapp": true,
	}
	nameAliases = map[string]bool{"name": true, "nombre": true}

	nonDigit = regexp.MustCompile(`\D`)
)

// ImportOptions overrides column auto-detection. An empty field means "auto":
// fall back to the phone/name aliases. PhoneColumn/NameColumn are matched against
// header (CSV/XLSX) or key (JSON) names case-insensitively.
type ImportOptions struct {
	PhoneColumn string
	NameColumn  string
}

// ImportAnalysis describes an uploaded file without inserting anything: which
// columns it has, which become usable {tags}, and a few sample rows. The UI uses
// it to tell the user "this file has these columns, you can use these tags".
type ImportAnalysis struct {
	Format      string              `json:"format"`
	Columns     []string            `json:"columns"`
	PhoneColumn string              `json:"phone_column"`
	NameColumn  string              `json:"name_column"`
	Tags        []string            `json:"tags"` // e.g. ["{nombre}","{empresa}"]
	SampleRows  []map[string]string `json:"sample_rows"`
	RowCount    int                 `json:"row_count"`
}

// normalizePhone strips formatting (spaces, +, dashes, parentheses) and validates
// the digit count, returning a digits-only phone and whether it is valid.
func normalizePhone(raw string) (string, bool) {
	digits := nonDigit.ReplaceAllString(raw, "")
	if len(digits) < 8 || len(digits) > 15 {
		return "", false
	}
	return digits, true
}

// ParseRecipients parses an import payload in the given format ("csv"/"xlsx"/"json"),
// deduping by phone and dropping rows with an invalid phone number. opts can pin
// which column holds the phone/name (empty = auto-detect by alias).
func ParseRecipients(data []byte, format string, opts ImportOptions) ([]*domainCampaign.Recipient, error) {
	switch strings.ToLower(format) {
	case FormatXLSX:
		header, rows, err := readXLSX(data)
		if err != nil {
			return nil, err
		}
		return tableToRecipients(header, rows, opts)
	case FormatJSON:
		return parseJSON(data, opts)
	default:
		header, rows, err := readCSV(data)
		if err != nil {
			return nil, err
		}
		return tableToRecipients(header, rows, opts)
	}
}

// AnalyzeImport inspects a payload and reports its columns, usable tags and sample
// rows. It never inserts anything.
func AnalyzeImport(data []byte, format string) (*ImportAnalysis, error) {
	switch strings.ToLower(format) {
	case FormatJSON:
		return analyzeJSON(data)
	case FormatXLSX:
		header, rows, err := readXLSX(data)
		if err != nil {
			return nil, err
		}
		return analyzeTable(FormatXLSX, header, rows), nil
	default:
		header, rows, err := readCSV(data)
		if err != nil {
			return nil, err
		}
		return analyzeTable(FormatCSV, header, rows), nil
	}
}

// ParseCSV reads `phone,name,var1,...` rows (first row is the header), auto-detecting columns.
func ParseCSV(data []byte) ([]*domainCampaign.Recipient, error) {
	header, rows, err := readCSV(data)
	if err != nil {
		return nil, err
	}
	return tableToRecipients(header, rows, ImportOptions{})
}

// ParseXLSX reads the first worksheet of an .xlsx file the same way as CSV.
func ParseXLSX(data []byte) ([]*domainCampaign.Recipient, error) {
	header, rows, err := readXLSX(data)
	if err != nil {
		return nil, err
	}
	return tableToRecipients(header, rows, ImportOptions{})
}

// ParseJSON reads a JSON array like
// [{"phone":"573166203787","name":"Gerlén","empresa":"Fututel"}], auto-detecting columns.
func ParseJSON(data []byte) ([]*domainCampaign.Recipient, error) {
	return parseJSON(data, ImportOptions{})
}

func parseJSON(data []byte, opts ImportOptions) ([]*domainCampaign.Recipient, error) {
	rows, err := decodeJSONRows(data)
	if err != nil {
		return nil, err
	}
	pc := strings.ToLower(strings.TrimSpace(opts.PhoneColumn))
	nc := strings.ToLower(strings.TrimSpace(opts.NameColumn))

	seen := make(map[string]bool)
	var out []*domainCampaign.Recipient
	for _, row := range rows {
		rawPhone := ""
		for k, v := range row {
			if isPhoneColumn(strings.ToLower(strings.TrimSpace(k)), pc) {
				rawPhone = anyToString(v)
				break
			}
		}
		phone, ok := normalizePhone(rawPhone)
		if !ok || seen[phone] {
			continue
		}
		seen[phone] = true

		rec := &domainCampaign.Recipient{Phone: phone}
		vars := make(map[string]string)
		for k, v := range row {
			lk := strings.ToLower(strings.TrimSpace(k))
			if isPhoneColumn(lk, pc) {
				continue
			}
			if isNameColumn(lk, nc) {
				rec.Name = anyToString(v)
				continue
			}
			if s := anyToString(v); s != "" {
				vars[strings.TrimSpace(k)] = s
			}
		}
		if len(vars) > 0 {
			rec.Variables = vars
		}
		out = append(out, rec)
	}
	return out, nil
}

// --- table readers ---

func readCSV(data []byte) ([]string, [][]string, error) {
	r := csv.NewReader(bytes.NewReader(data))
	r.FieldsPerRecord = -1
	r.TrimLeadingSpace = true

	all, err := r.ReadAll()
	if err != nil {
		return nil, nil, fmt.Errorf("read CSV: %w", err)
	}
	if len(all) == 0 {
		return nil, nil, fmt.Errorf("empty CSV")
	}
	return all[0], all[1:], nil
}

func readXLSX(data []byte) ([]string, [][]string, error) {
	f, err := excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		return nil, nil, fmt.Errorf("invalid xlsx file: %w", err)
	}
	defer f.Close()

	sheet := f.GetSheetName(0)
	if sheet == "" {
		return nil, nil, fmt.Errorf("xlsx has no sheets")
	}
	rows, err := f.GetRows(sheet)
	if err != nil {
		return nil, nil, fmt.Errorf("read xlsx rows: %w", err)
	}
	if len(rows) == 0 {
		return nil, nil, fmt.Errorf("empty xlsx sheet")
	}
	return rows[0], rows[1:], nil
}

// --- shared table mapping ---

// columnMapping resolves the role of each header column.
type columnMapping struct {
	phoneIdx int
	nameIdx  int
	varCols  map[int]string // column index -> variable key (the header text)
}

func mapColumns(header []string, phoneCol, nameCol string) columnMapping {
	m := columnMapping{phoneIdx: -1, nameIdx: -1, varCols: make(map[int]string)}
	pc := strings.ToLower(strings.TrimSpace(phoneCol))
	nc := strings.ToLower(strings.TrimSpace(nameCol))
	for i, h := range header {
		key := strings.ToLower(strings.TrimSpace(h))
		switch {
		case isPhoneColumn(key, pc) && m.phoneIdx == -1:
			m.phoneIdx = i
		case isNameColumn(key, nc) && m.nameIdx == -1:
			m.nameIdx = i
		case key != "":
			m.varCols[i] = strings.TrimSpace(h)
		}
	}
	return m
}

// isPhoneColumn reports whether a (lower-cased) column key is the phone column:
// an exact match of the explicit override, or a known alias when no override.
func isPhoneColumn(key, override string) bool {
	if override != "" {
		return key == override
	}
	return phoneAliases[key]
}

// isNameColumn is the name-column equivalent of isPhoneColumn.
func isNameColumn(key, override string) bool {
	if override != "" {
		return key == override
	}
	return nameAliases[key]
}

func tableToRecipients(header []string, rows [][]string, opts ImportOptions) ([]*domainCampaign.Recipient, error) {
	m := mapColumns(header, opts.PhoneColumn, opts.NameColumn)
	if m.phoneIdx == -1 {
		return nil, fmt.Errorf("file must have a phone column (phone/telefono/celular) — select which column holds the phone numbers")
	}

	seen := make(map[string]bool)
	var out []*domainCampaign.Recipient
	for _, row := range rows {
		if m.phoneIdx >= len(row) {
			continue
		}
		phone, ok := normalizePhone(row[m.phoneIdx])
		if !ok || seen[phone] {
			continue
		}
		seen[phone] = true

		rec := &domainCampaign.Recipient{Phone: phone}
		if m.nameIdx >= 0 && m.nameIdx < len(row) {
			rec.Name = strings.TrimSpace(row[m.nameIdx])
		}
		vars := make(map[string]string)
		for idx, key := range m.varCols {
			if idx < len(row) {
				if v := strings.TrimSpace(row[idx]); v != "" {
					vars[key] = v
				}
			}
		}
		if len(vars) > 0 {
			rec.Variables = vars
		}
		out = append(out, rec)
	}
	return out, nil
}

// --- analysis ---

func analyzeTable(format string, header []string, rows [][]string) *ImportAnalysis {
	analysis := &ImportAnalysis{Format: format, RowCount: len(rows)}

	for _, h := range header {
		col := strings.TrimSpace(h)
		analysis.Columns = append(analysis.Columns, col)
		key := strings.ToLower(col)
		switch {
		case phoneAliases[key]:
			if analysis.PhoneColumn == "" {
				analysis.PhoneColumn = col
			}
		case nameAliases[key]:
			if analysis.NameColumn == "" {
				analysis.NameColumn = col
			}
			analysis.Tags = append(analysis.Tags, "{nombre}")
		case col != "":
			analysis.Tags = append(analysis.Tags, "{"+col+"}")
		}
	}

	for i, row := range rows {
		if i >= 5 {
			break
		}
		sample := make(map[string]string)
		for j, col := range analysis.Columns {
			if j < len(row) {
				sample[col] = strings.TrimSpace(row[j])
			}
		}
		analysis.SampleRows = append(analysis.SampleRows, sample)
	}
	return analysis
}

func analyzeJSON(data []byte) (*ImportAnalysis, error) {
	rows, err := decodeJSONRows(data)
	if err != nil {
		return nil, err
	}
	analysis := &ImportAnalysis{Format: FormatJSON, RowCount: len(rows)}

	keySet := make(map[string]bool)
	var orderedKeys []string
	scan := rows
	if len(scan) > 20 {
		scan = scan[:20]
	}
	for _, row := range scan {
		for k := range row {
			if !keySet[k] {
				keySet[k] = true
				orderedKeys = append(orderedKeys, k)
			}
		}
	}
	sort.Strings(orderedKeys)

	for _, k := range orderedKeys {
		analysis.Columns = append(analysis.Columns, k)
		key := strings.ToLower(strings.TrimSpace(k))
		switch {
		case phoneAliases[key]:
			if analysis.PhoneColumn == "" {
				analysis.PhoneColumn = k
			}
		case nameAliases[key]:
			if analysis.NameColumn == "" {
				analysis.NameColumn = k
			}
			analysis.Tags = append(analysis.Tags, "{nombre}")
		default:
			analysis.Tags = append(analysis.Tags, "{"+k+"}")
		}
	}

	for i, row := range rows {
		if i >= 5 {
			break
		}
		sample := make(map[string]string)
		for k, v := range row {
			sample[k] = anyToString(v)
		}
		analysis.SampleRows = append(analysis.SampleRows, sample)
	}
	return analysis, nil
}

func decodeJSONRows(data []byte) ([]map[string]any, error) {
	var rows []map[string]any
	if err := json.Unmarshal(data, &rows); err != nil {
		return nil, fmt.Errorf("invalid JSON array of recipients: %w", err)
	}
	return rows, nil
}

// anyToString renders a decoded JSON value as a clean string, keeping integers
// integer-looking rather than "1.2345e+06".
func anyToString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(t)
	case bool:
		return strconv.FormatBool(t)
	case float64:
		if t == math.Trunc(t) && math.Abs(t) < 1e15 {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'f', -1, 64)
	default:
		return fmt.Sprintf("%v", t)
	}
}
