package campaign

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"regexp"
	"strconv"
	"strings"

	domainCampaign "github.com/aldinokemal/go-whatsapp-web-multidevice/domains/campaign"
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

// normalizePhone strips formatting (spaces, +, dashes, parentheses) and validates
// the digit count, returning a digits-only phone and whether it is valid.
func normalizePhone(raw string) (string, bool) {
	digits := nonDigit.ReplaceAllString(raw, "")
	if len(digits) < 8 || len(digits) > 15 {
		return "", false
	}
	return digits, true
}

// ParseRecipients parses an import payload as CSV or JSON, deduping by phone and
// dropping rows with an invalid phone number.
func ParseRecipients(data []byte, isCSV bool) ([]*domainCampaign.Recipient, error) {
	if isCSV {
		return ParseCSV(data)
	}
	return ParseJSON(data)
}

// ParseCSV reads `phone,name,var1,var2,...` rows. The first row is the header; the
// phone column is required (phone/telefono/celular/...), name is optional, and any
// other column becomes a variable keyed by its header.
func ParseCSV(data []byte) ([]*domainCampaign.Recipient, error) {
	r := csv.NewReader(bytes.NewReader(data))
	r.FieldsPerRecord = -1 // tolerate ragged rows
	r.TrimLeadingSpace = true

	header, err := r.Read()
	if err == io.EOF {
		return nil, fmt.Errorf("empty CSV")
	}
	if err != nil {
		return nil, fmt.Errorf("read CSV header: %w", err)
	}

	phoneIdx, nameIdx := -1, -1
	varCols := make(map[int]string)
	for i, h := range header {
		key := strings.ToLower(strings.TrimSpace(h))
		switch {
		case phoneAliases[key] && phoneIdx == -1:
			phoneIdx = i
		case nameAliases[key] && nameIdx == -1:
			nameIdx = i
		case key != "":
			varCols[i] = strings.TrimSpace(h)
		}
	}
	if phoneIdx == -1 {
		return nil, fmt.Errorf("CSV must have a phone column (phone/telefono/celular)")
	}

	seen := make(map[string]bool)
	var out []*domainCampaign.Recipient
	for {
		row, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read CSV row: %w", err)
		}
		if phoneIdx >= len(row) {
			continue
		}
		phone, ok := normalizePhone(row[phoneIdx])
		if !ok || seen[phone] {
			continue
		}
		seen[phone] = true

		rec := &domainCampaign.Recipient{Phone: phone}
		if nameIdx >= 0 && nameIdx < len(row) {
			rec.Name = strings.TrimSpace(row[nameIdx])
		}
		vars := make(map[string]string)
		for idx, key := range varCols {
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

// ParseJSON reads a JSON array like
// [{"phone":"573166203787","name":"Gerlén","empresa":"Fututel"}]. The phone key is
// required; name is optional; every other key becomes a variable.
func ParseJSON(data []byte) ([]*domainCampaign.Recipient, error) {
	var rows []map[string]any
	if err := json.Unmarshal(data, &rows); err != nil {
		return nil, fmt.Errorf("invalid JSON array of recipients: %w", err)
	}

	seen := make(map[string]bool)
	var out []*domainCampaign.Recipient
	for _, row := range rows {
		rawPhone := ""
		for k, v := range row {
			if phoneAliases[strings.ToLower(strings.TrimSpace(k))] {
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
			if phoneAliases[lk] {
				continue
			}
			if nameAliases[lk] {
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
