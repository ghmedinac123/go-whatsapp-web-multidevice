package campaign

import (
	"strings"
	"testing"
)

func TestSpinVariableReplacement(t *testing.T) {
	e := NewSpintaxEngine()
	got := e.Spin("Hola {nombre}, gracias por contactar a {empresa}", map[string]string{
		"nombre":  "Gerlén",
		"empresa": "Fututel",
	})
	want := "Hola Gerlén, gracias por contactar a Fututel"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestSpinVariableCaseInsensitive(t *testing.T) {
	e := NewSpintaxEngine()
	got := e.Spin("{Nombre}", map[string]string{"nombre": "Ana"})
	if got != "Ana" {
		t.Fatalf("expected case-insensitive match, got %q", got)
	}
}

func TestSpinUnknownVariableKept(t *testing.T) {
	e := NewSpintaxEngine()
	got := e.Spin("Hola {empresa}", nil)
	if got != "Hola {empresa}" {
		t.Fatalf("unknown variable should be left literal, got %q", got)
	}
}

func TestSpinAlternationPicksValidOption(t *testing.T) {
	e := NewSpintaxEngine()
	valid := map[string]bool{"a": true, "b": true, "c": true}
	for i := 0; i < 200; i++ {
		got := e.Spin("{a|b|c}", nil)
		if !valid[got] {
			t.Fatalf("unexpected option %q", got)
		}
	}
}

func TestSpinNestedAlternation(t *testing.T) {
	e := NewSpintaxEngine()
	valid := map[string]bool{"hola ana": true, "hola luis": true, "hey ana": true, "hey luis": true}
	for i := 0; i < 200; i++ {
		got := e.Spin("{hola|hey} {ana|luis}", nil)
		if !valid[got] {
			t.Fatalf("unexpected combination %q", got)
		}
	}
}

// TestGenerateUniqueness is the headline requirement: 1000 messages from the same
// template must produce zero exact duplicates, even though the template's pure
// combination space (4*3*3*3 = 108) is far smaller than 1000.
func TestGenerateUniqueness(t *testing.T) {
	e := NewSpintaxEngine()
	const tmpl = "{Hola|Buenas|Hey|Qué tal} {nombre}, {tenemos|traemos|preparamos} " +
		"{una oferta|un descuento|una promo} {especial|exclusiva|de hoy}"

	seen := make(map[string]struct{}, 1000)
	for i := 0; i < 1000; i++ {
		msg := e.Generate(tmpl, map[string]string{"nombre": "Cliente"})
		if _, dup := seen[msg]; dup {
			t.Fatalf("duplicate message generated at iteration %d: %q", i, msg)
		}
		seen[msg] = struct{}{}
	}
	if len(seen) != 1000 {
		t.Fatalf("expected 1000 unique messages, got %d", len(seen))
	}
}

func TestGenerateUniquenessReadability(t *testing.T) {
	// The visible (zero-width-stripped) text must always be one of the real spins.
	e := NewSpintaxEngine()
	for i := 0; i < 50; i++ {
		msg := e.Generate("{a|b}", nil)
		visible := strings.ReplaceAll(msg, zeroWidthSpace, "")
		if visible != "a" && visible != "b" {
			t.Fatalf("visible text corrupted: %q", visible)
		}
	}
}
