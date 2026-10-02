package logtmpl

import (
	"sort"
	"strings"
)

// Slot is one position of a template: either a literal token or a typed
// wildcard. Wildcards keep their token type (and, for error codes, the
// family) so an error code is never generalized into a bare number.
type Slot struct {
	Wild   bool
	Lit    string    // literal text (when Wild == false)
	Type   TokenType // token type of the literal, or wildcard constraint
	Family string    // error-code family constraint ("" = none)
	Capped bool      // split was refused here due to budget; do not retry
	Values map[string]int
}

func literalSlot(t Token) Slot {
	return Slot{Lit: t.Text, Type: t.Type}
}

func (s Slot) String() string {
	if !s.Wild {
		return s.Lit
	}
	if s.Type == TokErrorCode && s.Family != "" {
		return "<ERR:" + s.Family + ">"
	}
	return "<" + s.Type.String() + ">"
}

func slotEqual(a, b Slot) bool {
	if a.Wild != b.Wild {
		return false
	}
	if a.Wild {
		return a.Type == b.Type && a.Family == b.Family
	}
	return a.Lit == b.Lit
}

// Template is a sequence of slots plus bookkeeping. Examples preserves the
// example-line -> template mapping (capped by Config.MaxExamples); Count
// always tracks the true total.
type Template struct {
	ID       int
	Slots    []Slot
	Count    int
	Examples []string
	Frozen   map[int]bool // positions forbidden from generalization
}

// Signature is the canonical, comparable string form of a template.
func (t *Template) Signature() string {
	parts := make([]string, len(t.Slots))
	for i, s := range t.Slots {
		parts[i] = s.String()
	}
	return strings.Join(parts, " ")
}

// WildRatio is the fraction of generalized positions, used to enforce the
// over-generalization limit.
func (t *Template) WildRatio() float64 {
	if len(t.Slots) == 0 {
		return 0
	}
	w := 0
	for _, s := range t.Slots {
		if s.Wild {
			w++
		}
	}
	return float64(w) / float64(len(t.Slots))
}

// FrozenPositions lists the positions forbidden from generalization, sorted.
func (t *Template) FrozenPositions() []int {
	var out []int
	for p := range t.Frozen {
		out = append(out, p)
	}
	sort.Ints(out)
	return out
}

// Clone deep-copies the template so journal entries are immutable snapshots.
func (t *Template) Clone() *Template {
	c := &Template{
		ID:       t.ID,
		Count:    t.Count,
		Slots:    make([]Slot, len(t.Slots)),
		Examples: append([]string(nil), t.Examples...),
		Frozen:   map[int]bool{},
	}
	for i, s := range t.Slots {
		c.Slots[i] = s
		if s.Values != nil {
			m := make(map[string]int, len(s.Values))
			for k, v := range s.Values {
				m[k] = v
			}
			c.Slots[i].Values = m
		}
	}
	for k, v := range t.Frozen {
		c.Frozen[k] = v
	}
	return c
}

// templateFromTokens builds an all-literal template. Declared frozen-field
// policy: position 0 is frozen when it is a WORD (message-type anchor).
// Error codes at position 0 may still generalize within their family.
func templateFromTokens(toks []Token, id int) *Template {
	slots := make([]Slot, len(toks))
	for i, tk := range toks {
		slots[i] = literalSlot(tk)
	}
	frozen := map[int]bool{}
	if len(toks) > 0 && toks[0].Type == TokWord {
		frozen[0] = true
	}
	return &Template{ID: id, Slots: slots, Count: 1, Frozen: frozen}
}

// mergeSlots computes the generalized slot for one position.
// Returns (merged slot, cost, compatible). The merged slot carries no
// Values; mergeTemplates fills those in with real counts.
func mergeSlots(a, b Slot) (Slot, int, bool) {
	if !a.Wild && !b.Wild {
		if a.Lit == b.Lit {
			return a, 0, true
		}
		if a.Type != b.Type {
			return Slot{}, 0, false // no cross-type generalization
		}
		switch a.Type {
		case TokPunct:
			return Slot{}, 0, false // punctuation stays literal
		case TokErrorCode:
			fa, fb := ErrFamily(a.Lit), ErrFamily(b.Lit)
			if fa != fb {
				return Slot{}, 0, false // error-code families stay distinct
			}
			return Slot{Wild: true, Type: TokErrorCode, Family: fa}, 2, true
		default:
			return Slot{Wild: true, Type: a.Type}, 2, true
		}
	}
	if a.Wild && b.Wild {
		if a.Type == b.Type && a.Family == b.Family {
			return Slot{Wild: true, Type: a.Type, Family: a.Family}, 0, true
		}
		return Slot{}, 0, false
	}
	wild, lit := a, b
	if b.Wild {
		wild, lit = b, a
	}
	if lit.Type != wild.Type {
		return Slot{}, 0, false
	}
	if wild.Type == TokErrorCode && wild.Family != "" && ErrFamily(lit.Lit) != wild.Family {
		return Slot{}, 0, false
	}
	return Slot{Wild: true, Type: wild.Type, Family: wild.Family}, 1, true
}

// Distance is the public template-compatibility distance. It is purely
// structural: templates with different token counts are incompatible, and
// each position contributes the generalization cost (0 = identical,
// 1 = literal absorbed by wildcard, 2 = two literals generalized).
// Cluster policy (frozen positions, wildcard-ratio cap, split barriers)
// is enforced separately by the Clusterer.
func Distance(a, b *Template) (int, bool) {
	if len(a.Slots) != len(b.Slots) {
		return 0, false
	}
	total := 0
	for i := range a.Slots {
		_, c, ok := mergeSlots(a.Slots[i], b.Slots[i])
		if !ok {
			return 0, false
		}
		total += c
	}
	return total, true
}

// mergeTemplates generalizes two templates into one. It enforces the
// frozen-position policy and returns the generalized positions.
func mergeTemplates(a, b *Template) (*Template, int, []int, bool) {
	if len(a.Slots) != len(b.Slots) {
		return nil, 0, nil, false
	}
	frozen := map[int]bool{}
	for p := range a.Frozen {
		frozen[p] = true
	}
	for p := range b.Frozen {
		frozen[p] = true
	}
	out := &Template{Slots: make([]Slot, len(a.Slots)), Frozen: frozen}
	cost := 0
	var positions []int
	for i := range a.Slots {
		if frozen[i] && !slotEqual(a.Slots[i], b.Slots[i]) {
			return nil, 0, nil, false // forbidden-to-generalize field
		}
		s, c, ok := mergeSlots(a.Slots[i], b.Slots[i])
		if !ok {
			return nil, 0, nil, false
		}
		cost += c
		if c > 0 {
			positions = append(positions, i)
		}
		if s.Wild {
			s.Values = mergedValues(a, b, i)
		}
		out.Slots[i] = s
	}
	out.Count = a.Count + b.Count
	return out, cost, positions, true
}

// mergedValues tracks observed values of a wildcard slot with real counts,
// used by the split mechanism.
func mergedValues(a, b *Template, i int) map[string]int {
	m := map[string]int{}
	add := func(s Slot, count int) {
		if s.Wild {
			for k, v := range s.Values {
				m[k] += v
			}
		} else {
			m[s.Lit] += count
		}
	}
	add(a.Slots[i], a.Count)
	add(b.Slots[i], b.Count)
	return m
}

func mergeExamples(a, b []string, max int) []string {
	out := append([]string(nil), a...)
	for _, e := range b {
		if len(out) >= max {
			break
		}
		out = append(out, e)
	}
	return out
}
