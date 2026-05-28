package campaign

import (
	"bytes"
	"testing"

	"github.com/xuri/excelize/v2"
)

// buildXLSX writes a single-sheet workbook from a header + rows into bytes.
func buildXLSX(t *testing.T, header []string, rows [][]string) []byte {
	t.Helper()
	f := excelize.NewFile()
	defer f.Close()
	sheet := f.GetSheetName(0)
	all := append([][]string{header}, rows...)
	for r, row := range all {
		for c, val := range row {
			cell, _ := excelize.CoordinatesToCellName(c+1, r+1)
			_ = f.SetCellValue(sheet, cell, val)
		}
	}
	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		t.Fatalf("write xlsx: %v", err)
	}
	return buf.Bytes()
}

func TestNormalizePhone(t *testing.T) {
	cases := []struct {
		in    string
		want  string
		valid bool
	}{
		{"+57 316 620 3787", "573166203787", true},
		{"(573) 001-1122", "5730011122", true},
		{"573166203787", "573166203787", true},
		{"123", "", false},              // too short
		{"abcd", "", false},             // no digits
		{"", "", false},                 // empty
		{"1234567890123456", "", false}, // too long (16)
	}
	for _, c := range cases {
		got, ok := normalizePhone(c.in)
		if ok != c.valid || got != c.want {
			t.Fatalf("normalizePhone(%q) = (%q,%v), want (%q,%v)", c.in, got, ok, c.want, c.valid)
		}
	}
}

func TestParseCSV(t *testing.T) {
	csv := "phone,name,empresa\n" +
		"+57 316 6203787,Gerlén,Fututel\n" +
		"573001112233,Ana,\n" +
		"573166203787,dup,Other\n" + // duplicate of row 1 after normalization
		"abc,Invalid,X\n" // invalid phone

	recs, err := ParseCSV([]byte(csv))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("expected 2 recipients (dedup + skip invalid), got %d: %+v", len(recs), recs)
	}
	if recs[0].Phone != "573166203787" || recs[0].Name != "Gerlén" {
		t.Fatalf("row0 mismatch: %+v", recs[0])
	}
	if recs[0].Variables["empresa"] != "Fututel" {
		t.Fatalf("expected empresa=Fututel, got %+v", recs[0].Variables)
	}
	if recs[1].Phone != "573001112233" || recs[1].Variables != nil {
		t.Fatalf("row1 should have empty variables, got %+v", recs[1])
	}
}

func TestParseCSVRequiresPhoneColumn(t *testing.T) {
	if _, err := ParseCSV([]byte("nombre,empresa\nAna,Fututel\n")); err == nil {
		t.Fatal("expected error for missing phone column")
	}
}

func TestParseJSON(t *testing.T) {
	body := `[
		{"phone":"573166203787","name":"Gerlén","empresa":"Fututel","plan":3},
		{"telefono":"573001112233","nombre":"Ana"},
		{"phone":"573166203787","name":"dup"},
		{"phone":"123"}
	]`
	recs, err := ParseJSON([]byte(body))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("expected 2 recipients, got %d: %+v", len(recs), recs)
	}

	byPhone := map[string]*struct {
		name string
		vars map[string]string
	}{}
	for _, r := range recs {
		byPhone[r.Phone] = &struct {
			name string
			vars map[string]string
		}{r.Name, r.Variables}
	}
	g := byPhone["573166203787"]
	if g == nil || g.name != "Gerlén" {
		t.Fatalf("missing/incorrect Gerlén record: %+v", g)
	}
	if g.vars["empresa"] != "Fututel" || g.vars["plan"] != "3" {
		t.Fatalf("expected empresa=Fututel plan=3, got %+v", g.vars)
	}
	a := byPhone["573001112233"]
	if a == nil || a.name != "Ana" {
		t.Fatalf("missing/incorrect Ana record (telefono/nombre aliases): %+v", a)
	}
}

func TestParseJSONInvalid(t *testing.T) {
	if _, err := ParseJSON([]byte(`{"not":"an array"}`)); err == nil {
		t.Fatal("expected error for non-array JSON")
	}
}

func TestParseXLSX(t *testing.T) {
	data := buildXLSX(t,
		[]string{"phone", "name", "empresa"},
		[][]string{
			{"+57 316 6203787", "Gerlén", "Fututel"},
			{"573001112233", "Ana", ""},
			{"573166203787", "dup", "Other"}, // duplicate phone -> ignored
			{"abc", "Invalid", "X"},          // invalid phone -> skipped
		},
	)
	recs, err := ParseXLSX(data)
	if err != nil {
		t.Fatalf("parse xlsx: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("expected 2 recipients, got %d: %+v", len(recs), recs)
	}
	if recs[0].Phone != "573166203787" || recs[0].Name != "Gerlén" || recs[0].Variables["empresa"] != "Fututel" {
		t.Fatalf("row0 mismatch: %+v", recs[0])
	}
}

func TestParseRecipientsPhoneColumnOverride(t *testing.T) {
	// "linea" is not a phone alias and "cliente" is not a name alias.
	csv := "cliente,linea,empresa\nAna,573166203787,Fututel\n"

	if _, err := ParseRecipients([]byte(csv), FormatCSV, ImportOptions{}); err == nil {
		t.Fatal("expected error without an explicit phone column")
	}

	recs, err := ParseRecipients([]byte(csv), FormatCSV, ImportOptions{PhoneColumn: "linea", NameColumn: "cliente"})
	if err != nil {
		t.Fatalf("override parse: %v", err)
	}
	if len(recs) != 1 || recs[0].Phone != "573166203787" || recs[0].Name != "Ana" {
		t.Fatalf("override mismatch: %+v", recs)
	}
	if recs[0].Variables["empresa"] != "Fututel" {
		t.Fatalf("expected empresa var, got %+v", recs[0].Variables)
	}
	if _, leaked := recs[0].Variables["linea"]; leaked {
		t.Fatalf("phone column leaked into variables: %+v", recs[0].Variables)
	}
}

func TestParseRecipientsJSONPhoneOverride(t *testing.T) {
	body := `[{"cliente":"Ana","linea":"573166203787","empresa":"Fututel"}]`
	recs, err := ParseRecipients([]byte(body), FormatJSON, ImportOptions{PhoneColumn: "linea", NameColumn: "cliente"})
	if err != nil {
		t.Fatalf("json override: %v", err)
	}
	if len(recs) != 1 || recs[0].Phone != "573166203787" || recs[0].Name != "Ana" {
		t.Fatalf("json override mismatch: %+v", recs)
	}
}

func TestAnalyzeImportCSV(t *testing.T) {
	csv := "phone,nombre,empresa,ciudad\n573166203787,Gerlén,Fututel,Bogotá\n"
	a, err := AnalyzeImport([]byte(csv), FormatCSV)
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	if a.PhoneColumn != "phone" || a.NameColumn != "nombre" {
		t.Fatalf("column detection wrong: %+v", a)
	}
	wantTags := map[string]bool{"{nombre}": true, "{empresa}": true, "{ciudad}": true}
	if len(a.Tags) != 3 {
		t.Fatalf("expected 3 tags, got %v", a.Tags)
	}
	for _, tag := range a.Tags {
		if !wantTags[tag] {
			t.Fatalf("unexpected tag %q in %v", tag, a.Tags)
		}
	}
	if a.RowCount != 1 || len(a.SampleRows) != 1 || a.SampleRows[0]["empresa"] != "Fututel" {
		t.Fatalf("sample/rowcount wrong: %+v", a)
	}
}

func TestAnalyzeImportXLSX(t *testing.T) {
	data := buildXLSX(t, []string{"phone", "name", "plan"}, [][]string{{"573166203787", "Ana", "Pro"}})
	a, err := AnalyzeImport(data, FormatXLSX)
	if err != nil {
		t.Fatalf("analyze xlsx: %v", err)
	}
	if a.Format != FormatXLSX || a.PhoneColumn != "phone" {
		t.Fatalf("xlsx analysis wrong: %+v", a)
	}
	found := false
	for _, tag := range a.Tags {
		if tag == "{plan}" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected {plan} tag, got %v", a.Tags)
	}
}

func TestAnalyzeImportJSON(t *testing.T) {
	body := `[{"phone":"573166203787","name":"Ana","empresa":"Fututel"}]`
	a, err := AnalyzeImport([]byte(body), FormatJSON)
	if err != nil {
		t.Fatalf("analyze json: %v", err)
	}
	if a.PhoneColumn != "phone" || a.NameColumn != "name" {
		t.Fatalf("json column detection wrong: %+v", a)
	}
	found := false
	for _, tag := range a.Tags {
		if tag == "{empresa}" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected {empresa} tag, got %v", a.Tags)
	}
}
