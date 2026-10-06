package diag

import (
	"reflect"
	"sort"
	"strings"
	"testing"
)

// lcg is a tiny deterministic PRNG so the randomized cross-checks are
// reproducible.
type lcg struct{ s uint64 }

func (l *lcg) next() uint64 {
	l.s = l.s*6364136223846793005 + 1442695040888963407
	return l.s >> 11
}

func (l *lcg) intn(n int) int { return int(l.next() % uint64(n)) }

var textPieces = []string{"a", "b", "c", " ", "\t", "TODO", "\n", "xy", ";"}

func randomText(rng *lcg, pieces int) string {
	var b strings.Builder
	for i := 0; i < pieces; i++ {
		b.WriteString(textPieces[rng.intn(len(textPieces))])
	}
	return b.String()
}

func randomEdit(rng *lcg, text string) Edit {
	n := len(text)
	start := rng.intn(n + 1)
	end := start
	if n > 0 && rng.intn(2) == 0 {
		end = start + rng.intn(n-start+1)
	}
	return Edit{Start: start, End: end, Text: randomText(rng, rng.intn(4))}
}

// analyzeTabs and analyzeTODO are stand-in analyzers: deterministic
// functions of the text, so mapped diagnostics can be checked against
// a full re-analysis of the current text.
func analyzeTabs(text string) []Diagnostic {
	var out []Diagnostic
	for i := 0; i < len(text); i++ {
		if text[i] == '\t' {
			out = append(out, Diagnostic{
				RuleID:   "lint/tab",
				Range:    Range{i, i + 1},
				Severity: SeverityWarning,
				Message:  "tab character",
			})
		}
	}
	return out
}

func analyzeTODO(text string) []Diagnostic {
	var out []Diagnostic
	for i := 0; i+4 <= len(text); i++ {
		if text[i:i+4] == "TODO" {
			out = append(out, Diagnostic{
				RuleID:   "style/todo",
				Range:    Range{i, i + 4},
				Severity: SeverityInfo,
				Message:  "TODO comment",
			})
		}
	}
	return out
}

func analyze(phase, text string) []Diagnostic {
	switch phase {
	case "lint":
		return analyzeTabs(text)
	case "style":
		return analyzeTODO(text)
	default: // "combo"
		return append(analyzeTabs(text), analyzeTODO(text)...)
	}
}

// record is one phase response kept by the reference model.
type record struct {
	phase   string
	version FileVersion
	diags   []Diagnostic
}

// model is the full-set reference: it keeps every refresh and
// recomputes the merged snapshot from scratch, independently of the
// Store's internal bookkeeping.
type model struct{ recs []record }

func (m *model) refresh(phase string, v FileVersion, diags []Diagnostic) {
	for i, r := range m.recs {
		if r.phase == phase {
			if len(diags) == 0 {
				m.recs = append(m.recs[:i], m.recs[i+1:]...)
			} else {
				m.recs[i] = record{phase, v, diags}
			}
			return
		}
	}
	if len(diags) > 0 {
		m.recs = append(m.recs, record{phase, v, diags})
	}
}

func (m *model) snapshot(doc *Document) []Merged {
	byPhase := map[string]record{}
	var order []string
	for _, r := range m.recs {
		if _, ok := byPhase[r.phase]; !ok {
			order = append(order, r.phase)
		}
		byPhase[r.phase] = r
	}
	sort.Strings(order)

	acc := map[Key]*Merged{}
	for _, ph := range order {
		r := byPhase[ph]
		set := map[Key]Diagnostic{}
		for _, d := range r.diags {
			k := Key{Version: r.version, RuleID: d.RuleID, Range: d.Range}
			if prev, ok := set[k]; !ok || d.Severity > prev.Severity {
				set[k] = d
			}
		}
		for k, d := range set {
			mapped, ok := doc.MapRange(k.Version, k.Range)
			if !ok {
				continue
			}
			mg, ok := acc[k]
			if !ok {
				mg = &Merged{RuleID: k.RuleID, Range: mapped, Severity: d.Severity, Message: d.Message, Origin: k}
				acc[k] = mg
			} else if d.Severity > mg.Severity {
				mg.Severity = d.Severity
				mg.Message = d.Message
			}
			mg.Phases = append(mg.Phases, ph)
		}
	}
	out := make([]Merged, 0, len(acc))
	for _, mg := range acc {
		out = append(out, *mg)
	}
	sort.Slice(out, func(i, j int) bool { return mergedLess(out[i], out[j]) })
	return out
}

// TestRandomizedAgainstFullSetModel drives the store with a random
// interleaving of document edits and phase responses (including stale
// responses bound to older analysis versions, revocations, and
// duplicate contributions) and checks the snapshot against the
// full-set reference model after every single step.
func TestRandomizedAgainstFullSetModel(t *testing.T) {
	rng := &lcg{s: 20261007}
	doc := NewDocument(randomText(rng, 40))
	st := NewStore()
	var m model
	type snapshot struct {
		v    FileVersion
		text string
	}
	history := []snapshot{{doc.Version(), doc.Text()}}
	phases := []string{"lint", "style", "combo"}

	for step := 0; step < 400; step++ {
		switch rng.intn(10) {
		case 0, 1, 2, 3: // edit the document
			e := randomEdit(rng, doc.Text())
			if err := doc.ApplyEdit(e); err != nil {
				t.Fatalf("step %d: %v", step, err)
			}
			history = append(history, snapshot{doc.Version(), doc.Text()})
			if len(history) > 8 {
				history = history[1:]
			}
		default: // a phase response arrives
			phase := phases[rng.intn(len(phases))]
			v, text := doc.Version(), doc.Text()
			if rng.intn(3) == 0 && len(history) > 1 {
				// Stale response: analyzed against an older version.
				h := history[rng.intn(len(history)-1)]
				v, text = h.v, h.text
			}
			var diags []Diagnostic
			if rng.intn(5) != 0 { // 1 in 5 refreshes revokes the phase
				diags = analyze(phase, text)
				if len(diags) > 0 && rng.intn(3) == 0 {
					// Duplicate contribution with a random severity.
					dup := diags[rng.intn(len(diags))]
					dup.Severity = Severity(rng.intn(4))
					diags = append(diags, dup)
				}
			}
			st.Refresh(phase, v, diags)
			m.refresh(phase, v, diags)
		}

		got := st.Snapshot(doc)
		want := m.snapshot(doc)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("step %d: store and full-set model diverge\n got: %+v\nwant: %+v", step, got, want)
		}
	}
}

// editTracker over-approximates, in current-version coordinates, the
// set of ranges whose content may differ from the originally analyzed
// text. A range disjoint from every tracked region is guaranteed to
// hold original text at its original position.
type editTracker struct{ affected []Range }

func (tr *editTracker) apply(e Edit) {
	delta := len(e.Text) - (e.End - e.Start)
	clamp := func(p int, isEnd bool) int {
		if p <= e.Start {
			return p
		}
		if p >= e.End {
			return p + delta
		}
		if isEnd {
			return e.Start + len(e.Text)
		}
		return e.Start
	}
	next := make([]Range, 0, len(tr.affected)+1)
	for _, r := range tr.affected {
		next = append(next, Range{clamp(r.Start, false), clamp(r.End, true)})
	}
	next = append(next, Range{e.Start, e.Start + len(e.Text)})
	tr.affected = next
}

func (tr *editTracker) touches(r Range) bool {
	for _, a := range tr.affected {
		if a.Start < r.End && r.Start < a.End {
			return true
		}
	}
	return false
}

// TestFullReanalysisAfterEdits refreshes a phase once, then applies
// many edits without any further refresh. Every surviving snapshot
// diagnostic must reappear in a full re-analysis of the current text
// at the mapped range, and every re-analyzed diagnostic missing from
// the snapshot must intersect an edited region.
func TestFullReanalysisAfterEdits(t *testing.T) {
	rng := &lcg{s: 42}
	doc := NewDocument(randomText(rng, 60))
	st := NewStore()
	st.Refresh("lint", doc.Version(), analyzeTabs(doc.Text()))

	var tr editTracker
	for i := 0; i < 60; i++ {
		e := randomEdit(rng, doc.Text())
		if err := doc.ApplyEdit(e); err != nil {
			t.Fatal(err)
		}
		tr.apply(e)
	}

	snap := st.Snapshot(doc)
	re := analyzeTabs(doc.Text())

	type rk struct {
		rule string
		r    Range
	}
	reSet := map[rk]int{}
	for _, d := range re {
		reSet[rk{d.RuleID, d.Range}]++
	}
	snapSet := map[rk]int{}
	for _, mg := range snap {
		if mg.Origin.Version != 1 {
			t.Errorf("identity lost: origin version %d, want 1", mg.Origin.Version)
		}
		snapSet[rk{mg.RuleID, mg.Range}]++
		if reSet[rk{mg.RuleID, mg.Range}] == 0 {
			t.Errorf("snapshot diagnostic %+v not confirmed by full re-analysis", mg)
		}
	}
	for _, d := range re {
		if snapSet[rk{d.RuleID, d.Range}] == 0 && !tr.touches(d.Range) {
			t.Errorf("re-analyzed diagnostic %+v untouched by edits but missing from snapshot", d)
		}
	}
}

// TestStaleResponseBoundToAnalysisVersion: a response computed at
// version 1 arrives after the document has moved on. Its diagnostics
// are mapped from version 1; the one whose text was deleted is
// dropped, the other survives at its mapped position.
func TestStaleResponseBoundToAnalysisVersion(t *testing.T) {
	doc := NewDocument("a\tb\tc") // tabs at offsets 1 and 3
	stale := analyzeTabs(doc.Text())

	// Document moves to version 2 before the response arrives.
	mustEdit(t, doc, Edit{Start: 2, End: 4, Text: ""}) // delete "b\t" -> "a\tc"

	st := NewStore()
	st.Refresh("lint", 1, stale)

	snap := st.Snapshot(doc)
	if len(snap) != 1 {
		t.Fatalf("want exactly the untouched tab to survive, got %+v", snap)
	}
	if snap[0].Range != (Range{1, 2}) {
		t.Errorf("mapped range: got %+v", snap[0].Range)
	}
	if snap[0].Origin != (Key{Version: 1, RuleID: "lint/tab", Range: Range{1, 2}}) {
		t.Errorf("identity: got %+v", snap[0].Origin)
	}
}

// TestSameCurrentRangeDifferentIdentityNotMerged: two phases analyze
// different versions; after mapping, their ranges coincide in the
// current text. They must still not merge, because dedup by current
// text range is unsound.
func TestSameCurrentRangeDifferentIdentityNotMerged(t *testing.T) {
	doc := NewDocument("ab")
	st := NewStore()
	st.Refresh("p1", 1, []Diagnostic{
		{RuleID: "r", Range: Range{0, 1}, Severity: SeverityWarning, Message: "a at v1"},
	})
	mustEdit(t, doc, Edit{Start: 0, End: 0, Text: "x"}) // "xab", v2
	st.Refresh("p2", 2, []Diagnostic{
		{RuleID: "r", Range: Range{1, 2}, Severity: SeverityInfo, Message: "a at v2"},
	})

	snap := st.Snapshot(doc)
	if len(snap) != 2 {
		t.Fatalf("identities mapping to the same current range must not merge, got %+v", snap)
	}
	for _, mg := range snap {
		if mg.Range != (Range{1, 2}) {
			t.Errorf("mapped range: got %+v", mg.Range)
		}
	}
	origins := map[Key]bool{}
	for _, mg := range snap {
		origins[mg.Origin] = true
	}
	if !origins[Key{Version: 1, RuleID: "r", Range: Range{0, 1}}] ||
		!origins[Key{Version: 2, RuleID: "r", Range: Range{1, 2}}] {
		t.Errorf("origins: got %+v", origins)
	}
}
