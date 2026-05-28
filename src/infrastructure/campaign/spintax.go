package campaign

import (
	"crypto/sha256"
	"encoding/hex"
	"math/rand"
	"strings"
	"sync"
	"time"
)

// zeroWidthSpace is appended to force message uniqueness without changing how the
// text reads. It is also a mild anti-ban measure: identical bodies are a strong
// spam signal, so each delivered message differs by at least one invisible rune.
const zeroWidthSpace = "​"

// SpintaxEngine resolves spintax templates into concrete messages.
//
// Two brace forms are supported:
//   - Alternation: {a|b|c} picks one option at random (nesting allowed).
//   - Variable:    {nombre} is replaced by the matching value (case-insensitive);
//     an unknown placeholder is left untouched so typos stay visible.
//
// Generate additionally guarantees every returned message is unique for the life
// of the engine, so a campaign never sends two byte-identical bodies.
type SpintaxEngine struct {
	mu   sync.Mutex
	rng  *rand.Rand
	seen map[string]struct{}
}

// NewSpintaxEngine builds an engine with its own seeded RNG and empty dedupe set.
func NewSpintaxEngine() *SpintaxEngine {
	return &SpintaxEngine{
		rng:  rand.New(rand.NewSource(time.Now().UnixNano())),
		seen: make(map[string]struct{}),
	}
}

// Spin resolves spintax and variables into one message. It does NOT guarantee
// uniqueness, so it is safe for stateless use such as the UI preview.
func (e *SpintaxEngine) Spin(template string, vars map[string]string) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.spinLocked(template, vars)
}

// Generate behaves like Spin but guarantees the result has not been returned by a
// previous Generate call on this engine. Collisions (common when the template's
// combination space is smaller than the recipient count) get invisible variation.
func (e *SpintaxEngine) Generate(template string, vars map[string]string) string {
	e.mu.Lock()
	defer e.mu.Unlock()

	msg := e.spinLocked(template, vars)
	key := hashMessage(msg)
	if _, clash := e.seen[key]; clash {
		for n := 1; ; n++ {
			candidate := msg + strings.Repeat(zeroWidthSpace, n)
			key = hashMessage(candidate)
			if _, clash := e.seen[key]; !clash {
				msg = candidate
				break
			}
		}
	}
	e.seen[key] = struct{}{}
	return msg
}

// Reset clears the uniqueness memory (e.g. when reusing an engine for a new run).
func (e *SpintaxEngine) Reset() {
	e.mu.Lock()
	e.seen = make(map[string]struct{})
	e.mu.Unlock()
}

// spinLocked does the actual resolution; the caller must hold e.mu (the RNG is
// not concurrency safe).
func (e *SpintaxEngine) spinLocked(template string, vars map[string]string) string {
	return e.process(template, lowerKeys(vars))
}

// process walks the template once, resolving each top-level brace group. It
// recurses into the chosen option so nested groups and variables resolve too.
func (e *SpintaxEngine) process(s string, vars map[string]string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] != '{' {
			b.WriteByte(s[i])
			i++
			continue
		}
		end := matchBrace(s, i)
		if end == -1 {
			// Unbalanced brace: emit literally and move on.
			b.WriteByte(s[i])
			i++
			continue
		}
		inner := s[i+1 : end]
		options := splitTopLevel(inner)
		switch {
		case len(options) > 1:
			choice := options[e.rng.Intn(len(options))]
			b.WriteString(e.process(choice, vars))
		case strings.Contains(inner, "{"):
			// Single option that still contains a nested group.
			b.WriteString(e.process(inner, vars))
		default:
			b.WriteString(resolveVariable(inner, vars))
		}
		i = end + 1
	}
	return b.String()
}

// resolveVariable replaces a {key} placeholder with its value, or leaves the
// literal {key} when the variable is unknown.
func resolveVariable(inner string, vars map[string]string) string {
	key := strings.ToLower(strings.TrimSpace(inner))
	if value, ok := vars[key]; ok {
		return value
	}
	return "{" + inner + "}"
}

// matchBrace returns the index of the '}' that closes the '{' at open, honoring
// nesting, or -1 when unbalanced.
func matchBrace(s string, open int) int {
	depth := 0
	for i := open; i < len(s); i++ {
		switch s[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// splitTopLevel splits inner content on '|' that sits at brace depth 0, so nested
// groups are kept intact within a single option.
func splitTopLevel(s string) []string {
	var parts []string
	depth := 0
	start := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '{':
			depth++
		case '}':
			if depth > 0 {
				depth--
			}
		case '|':
			if depth == 0 {
				parts = append(parts, s[start:i])
				start = i + 1
			}
		}
	}
	parts = append(parts, s[start:])
	return parts
}

// lowerKeys returns a copy of vars with lower-cased keys for case-insensitive
// placeholder matching.
func lowerKeys(vars map[string]string) map[string]string {
	if len(vars) == 0 {
		return map[string]string{}
	}
	out := make(map[string]string, len(vars))
	for k, v := range vars {
		out[strings.ToLower(strings.TrimSpace(k))] = v
	}
	return out
}

// hashMessage returns a compact, fixed-size key for the dedupe set so memory stays
// bounded regardless of message length.
func hashMessage(msg string) string {
	sum := sha256.Sum256([]byte(msg))
	return hex.EncodeToString(sum[:])
}
