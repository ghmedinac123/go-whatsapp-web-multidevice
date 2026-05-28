package campaign

import "testing"

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
