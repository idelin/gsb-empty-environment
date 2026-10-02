package logtmpl

import (
	"fmt"
	"sort"
	"strings"
)

// Config declares all clustering policy knobs.
type Config struct {
	MergeThreshold   int     // max Distance for absorb/merge (default 4)
	MaxTemplates     int     // live-template budget; 0 = unlimited
	MaxWildcardRatio float64 // over-generalization limit (default 0.5)
	MaxExamples      int     // example lines kept per template (default 5)
	MaxSlotValues    int     // distinct wildcard values before split (default 8)
	// Deterministic selects the declared determinism policy:
	//   true  - records are processed in canonical (sorted) order at
	//           Finalize, so the result is independent of input order.
	//   false - records are processed in arrival order (streaming).
	Deterministic bool
}

func (c Config) withDefaults() Config {
	if c.MergeThreshold == 0 {
		c.MergeThreshold = 4
	}
	if c.MaxWildcardRatio == 0 {
		c.MaxWildcardRatio = 0.5
	}
	if c.MaxExamples == 0 {
		c.MaxExamples = 5
	}
	if c.MaxSlotValues == 0 {
		c.MaxSlotValues = 8
	}
	return c
}

// Evidence documents why a template was created: which templates/records it
// generalizes, which positions were generalized, and which positions are
// forbidden from generalization.
type Evidence struct {
	Kind      string   // "generalize" | "merge" | "split"
	Sources   []int    // source template IDs
	Positions []int    // generalized positions
	Forbidden []int    // positions forbidden from generalization
	Distance  int      // compatibility distance of the merge
	Samples   []string // example lines from the sources
}

// JournalEntry is one entry of the append-only template journal. Templates
// evolve exclusively through "add" and "retract" entries, so generalization
// is never monotonic: a retract undoes a template that was split or merged.
type JournalEntry struct {
	Seq      int
	Op       string // "add" | "retract"
	Template Template
	Reason   string
	Evidence *Evidence
}

// UnclassifiedRecord is a record that could not be placed. Budget
// exhaustion never swallows records: they are retained here with a reason.
type UnclassifiedRecord struct {
	Line   string
	Reason string
}

// Clusterer performs online template clustering.
type Clusterer struct {
	cfg          Config
	live         map[int]*Template
	nextID       int
	journal      []JournalEntry
	tel          *Telemetry
	unclassified []UnclassifiedRecord
	pending      []string
	barriers     map[string]bool // signatures a split forbids re-creating
}

// New creates a Clusterer. tel may be nil (telemetry discarded).
func New(cfg Config, tel *Telemetry) *Clusterer {
	if tel == nil {
		tel = &Telemetry{}
	}
	return &Clusterer{
		cfg:      cfg.withDefaults(),
		live:     map[int]*Template{},
		tel:      tel,
		barriers: map[string]bool{},
	}
}

// Add feeds one log record. In deterministic mode the record is buffered
// until Finalize; in streaming mode it is processed immediately.
func (c *Clusterer) Add(line string) {
	if c.cfg.Deterministic {
		c.pending = append(c.pending, line)
		return
	}
	c.process(line)
}

// Finalize flushes buffered records. Deterministic mode sorts records into
// canonical order first, which is what makes the result order-independent.
func (c *Clusterer) Finalize() {
	if !c.cfg.Deterministic {
		return
	}
	sort.Strings(c.pending)
	for _, line := range c.pending {
		c.process(line)
	}
	c.pending = nil
}

// Templates returns the live templates sorted by ID.
func (c *Clusterer) Templates() []*Template {
	ids := c.sortedIDs()
	out := make([]*Template, 0, len(ids))
	for _, id := range ids {
		out = append(out, c.live[id])
	}
	return out
}

// Journal returns the append-only add/retract journal.
func (c *Clusterer) Journal() []JournalEntry {
	return append([]JournalEntry(nil), c.journal...)
}

// Unclassified returns records that were not placed (never dropped).
func (c *Clusterer) Unclassified() []UnclassifiedRecord {
	return append([]UnclassifiedRecord(nil), c.unclassified...)
}

func (c *Clusterer) sortedIDs() []int {
	ids := make([]int, 0, len(c.live))
	for id := range c.live {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	return ids
}

func (c *Clusterer) process(line string) {
	toks := Tokenize(line)
	cand := templateFromTokens(toks, -1)
	cand.Examples = []string{line}

	// Try compatible live templates, best (distance, ID) first.
	type hit struct {
		t    *Template
		dist int
	}
	var hits []hit
	for _, id := range c.sortedIDs() {
		t := c.live[id]
		d, ok := Distance(t, cand)
		if ok && d <= c.cfg.MergeThreshold {
			hits = append(hits, hit{t, d})
		}
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].dist != hits[j].dist {
			return hits[i].dist < hits[j].dist
		}
		return hits[i].t.ID < hits[j].t.ID
	})
	for _, h := range hits {
		merged, cost, positions, ok := mergeTemplates(h.t, cand)
		if !ok {
			continue
		}
		if cost == 0 || merged.Signature() == h.t.Signature() {
			// Exact match: absorb in place.
			h.t.Count++
			h.t.addExample(line, c.cfg.MaxExamples)
			h.t.observe(toks)
			c.tel.Emit("record_classified", map[string]string{
				"template": fmt.Sprint(h.t.ID),
			})
			c.afterChange()
			return
		}
		if !c.policyOK(merged) {
			continue // over-generalization limit or split barrier
		}
		// Generalize: retract the old template, add the merged one.
		ev := &Evidence{
			Kind:      "generalize",
			Sources:   []int{h.t.ID},
			Positions: positions,
			Forbidden: merged.FrozenPositions(),
			Distance:  cost,
			Samples:   mergeExamples(h.t.Examples, []string{line}, c.cfg.MaxExamples),
		}
		merged.Examples = mergeExamples(h.t.Examples, []string{line}, c.cfg.MaxExamples)
		c.retract(h.t, "generalized")
		c.addTemplate(merged, ev)
		c.tel.Emit("record_classified", map[string]string{
			"template": fmt.Sprint(merged.ID),
		})
		c.afterChange()
		return
	}

	// No compatible template: create a new one, budget permitting.
	if c.cfg.MaxTemplates > 0 && len(c.live) >= c.cfg.MaxTemplates {
		c.unclassified = append(c.unclassified, UnclassifiedRecord{
			Line:   line,
			Reason: "template budget exhausted",
		})
		c.tel.Emit("record_unclassified", map[string]string{
			"reason": "budget_exhausted",
		})
		return
	}
	c.addTemplate(cand, nil)
	c.tel.Emit("record_classified", map[string]string{
		"template": fmt.Sprint(cand.ID),
	})
	c.afterChange()
}

// policyOK enforces the over-generalization limit and split barriers.
func (c *Clusterer) policyOK(t *Template) bool {
	if t.WildRatio() > c.cfg.MaxWildcardRatio {
		return false
	}
	return !c.barriers[t.Signature()]
}

// afterChange runs the merge sweep and the split pass after each change.
func (c *Clusterer) afterChange() {
	c.mergeSweep()
	c.splitPass()
}

// mergeSweep merges the closest compatible pair of live templates while
// any pair is within threshold and policy. Merges are primarily triggered
// by record absorption; this sweep keeps the template set consistent after
// splits. Pair selection is deterministic: smallest (distance, idA, idB).
func (c *Clusterer) mergeSweep() {
	for {
		ids := c.sortedIDs()
		bestD, bestA, bestB := c.cfg.MergeThreshold+1, -1, -1
		var bestM *Template
		var bestPos []int
		for x := 0; x < len(ids); x++ {
			for y := x + 1; y < len(ids); y++ {
				a, b := c.live[ids[x]], c.live[ids[y]]
				d, ok := Distance(a, b)
				if !ok || d > c.cfg.MergeThreshold {
					continue
				}
				m, _, pos, ok := mergeTemplates(a, b)
				if !ok || !c.policyOK(m) {
					continue
				}
				if d < bestD || d == bestD && (ids[x] < bestA || ids[x] == bestA && ids[y] < bestB) {
					bestD, bestA, bestB, bestM, bestPos = d, ids[x], ids[y], m, pos
				}
			}
		}
		if bestA < 0 {
			return
		}
		a, b := c.live[bestA], c.live[bestB]
		bestM.Examples = mergeExamples(a.Examples, b.Examples, c.cfg.MaxExamples)
		ev := &Evidence{
			Kind:      "merge",
			Sources:   []int{bestA, bestB},
			Positions: bestPos,
			Forbidden: bestM.FrozenPositions(),
			Distance:  bestD,
			Samples:   bestM.Examples,
		}
		c.retract(a, "merged")
		c.retract(b, "merged")
		c.addTemplate(bestM, ev)
	}
}

// splitPass splits a template when one of its wildcard slots has observed
// more than MaxSlotValues distinct values. The split retracts the parent
// and installs a barrier so the merge sweep cannot re-create the same
// over-general template: generalization is not monotonic.
func (c *Clusterer) splitPass() {
	for {
		var victim *Template
		vpos := -1
		for _, id := range c.sortedIDs() {
			t := c.live[id]
			for i, s := range t.Slots {
				if s.Wild && !s.Capped && len(s.Values) > c.cfg.MaxSlotValues {
					victim, vpos = t, i
					break
				}
			}
			if victim != nil {
				break
			}
		}
		if victim == nil {
			return
		}
		slot := victim.Slots[vpos]
		nChildren := len(slot.Values)
		if c.cfg.MaxTemplates > 0 && len(c.live)-1+nChildren > c.cfg.MaxTemplates {
			// Budget refuses the split; cap the slot so we do not loop.
			victim.Slots[vpos].Capped = true
			continue
		}
		values := make([]string, 0, nChildren)
		for v := range slot.Values {
			values = append(values, v)
		}
		sort.Strings(values)
		parentSig := victim.Signature()
		parentID := victim.ID
		c.retract(victim, "split")
		c.barriers[parentSig] = true
		for _, v := range values {
			child := victim.Clone()
			child.Slots[vpos] = Slot{Lit: v, Type: slot.Type}
			child.Count = slot.Values[v]
			child.Examples = filterExamples(victim.Examples, v, vpos, c.cfg.MaxExamples)
			c.addTemplate(child, &Evidence{
				Kind:      "split",
				Sources:   []int{parentID},
				Positions: []int{vpos},
				Forbidden: child.FrozenPositions(),
				Samples:   child.Examples,
			})
		}
		c.tel.Emit("template_split", map[string]string{
			"parent":   fmt.Sprint(parentID),
			"position": fmt.Sprint(vpos),
			"barrier":  parentSig,
		})
	}
}

func filterExamples(examples []string, value string, pos, max int) []string {
	var out []string
	for _, e := range examples {
		if len(out) >= max {
			break
		}
		toks := Tokenize(e)
		if pos < len(toks) && toks[pos].Text == value {
			out = append(out, e)
		}
	}
	return out
}

func (t *Template) addExample(line string, max int) {
	if len(t.Examples) < max {
		t.Examples = append(t.Examples, line)
	}
}

// observe records the literal values seen under each wildcard slot.
func (t *Template) observe(toks []Token) {
	for i, s := range t.Slots {
		if !s.Wild || i >= len(toks) {
			continue
		}
		if s.Values == nil {
			s.Values = map[string]int{}
		}
		s.Values[toks[i].Text]++
		t.Slots[i] = s
	}
}

func (c *Clusterer) addTemplate(t *Template, ev *Evidence) {
	t.ID = c.nextID
	c.nextID++
	c.live[t.ID] = t
	c.journal = append(c.journal, JournalEntry{
		Seq:      len(c.journal),
		Op:       "add",
		Template: *t.Clone(),
		Evidence: ev,
	})
	fields := map[string]string{
		"id":        fmt.Sprint(t.ID),
		"signature": t.Signature(),
		"count":     fmt.Sprint(t.Count),
	}
	if ev != nil {
		fields["evidence_kind"] = ev.Kind
		fields["evidence_sources"] = joinInts(ev.Sources)
		fields["evidence_positions"] = joinInts(ev.Positions)
		fields["evidence_forbidden"] = joinInts(ev.Forbidden)
	}
	c.tel.Emit("template_add", fields)
}

func (c *Clusterer) retract(t *Template, reason string) {
	delete(c.live, t.ID)
	c.journal = append(c.journal, JournalEntry{
		Seq:      len(c.journal),
		Op:       "retract",
		Template: *t.Clone(),
		Reason:   reason,
	})
	c.tel.Emit("template_retract", map[string]string{
		"id":        fmt.Sprint(t.ID),
		"reason":    reason,
		"signature": t.Signature(),
	})
}

func joinInts(xs []int) string {
	parts := make([]string, len(xs))
	for i, x := range xs {
		parts[i] = fmt.Sprint(x)
	}
	return strings.Join(parts, ",")
}
