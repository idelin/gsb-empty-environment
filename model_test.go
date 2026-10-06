package diagnostics

import (
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

// modelFile is an independent full-set model of File: it keeps every
// stage's raw contribution plus the whole document history as token
// lists, and recomputes the merged set from scratch after every
// operation. Range mapping goes through the token model from
// mapping_test.go, not through MapRange.
type modelFile struct {
	version uint64
	docs    map[uint64][]int  // token list of each version
	batches map[uint64][]Edit // edit batch taking version v to v+1
	stages  map[string]modelContrib
	nextTok int
}

type modelContrib struct {
	version uint64
	diags   []Diagnostic
}

func newModelFile(docLen int) *modelFile {
	doc := make([]int, docLen)
	for i := range doc {
		doc[i] = i
	}
	return &modelFile{
		docs:    map[uint64][]int{0: doc},
		batches: make(map[uint64][]Edit),
		stages:  make(map[string]modelContrib),
		nextTok: docLen,
	}
}

func (m *modelFile) applyEdits(edits []Edit) {
	doc := append([]int(nil), m.docs[m.version]...)
	for _, e := range edits {
		doc = modelApplyEdit(doc, e, &m.nextTok)
	}
	m.batches[m.version] = append([]Edit(nil), edits...)
	m.version++
	m.docs[m.version] = doc
}

func (m *modelFile) submit(stage string, version uint64, diags []Diagnostic) {
	if len(diags) == 0 {
		delete(m.stages, stage)
		return
	}
	m.stages[stage] = modelContrib{version, append([]Diagnostic(nil), diags...)}
}

// mapThrough maps r from version from to the current version using only
// the token documents.
func (m *modelFile) mapThrough(from uint64, r Range) (Range, bool) {
	nextID := m.nextTok // local; real tokens are all below m.nextTok
	for v := from; v < m.version; v++ {
		doc := m.docs[v]
		for _, e := range m.batches[v] {
			next := modelApplyEdit(doc, e, &nextID)
			var ok bool
			r, ok = modelMapRange(doc, next, r, e)
			if !ok {
				return Range{}, false
			}
			doc = next
		}
	}
	return r, true
}

// merged recomputes the expected merged set from the full contribution
// set, grouping with linear scans rather than maps.
func (m *modelFile) merged() []Merged {
	var stages []string
	for s := range m.stages {
		stages = append(stages, s)
	}
	sort.Strings(stages)

	var out []Merged
	for _, stage := range stages {
		c := m.stages[stage]
		for _, dg := range c.diags {
			current, ok := m.mapThrough(c.version, dg.Range)
			if !ok {
				continue
			}
			id := Identity{Version: c.version, Range: dg.Range, RuleID: dg.RuleID}
			idx := -1
			for i := range out {
				if out[i].Identity == id {
					idx = i
					break
				}
			}
			if idx < 0 {
				out = append(out, Merged{Identity: id, Current: current})
				idx = len(out) - 1
			}
			acc := &out[idx]
			if dg.Severity > acc.Severity {
				acc.Severity = dg.Severity
			}
			acc.Stages = appendIfMissing(acc.Stages, stage)
			acc.Messages = appendIfMissing(acc.Messages, dg.Message)
		}
	}
	for i := range out {
		sort.Strings(out[i].Stages)
		sort.Strings(out[i].Messages)
	}
	return out
}

func asSet(list []Merged) map[Identity]Merged {
	set := make(map[Identity]Merged, len(list))
	for _, m := range list {
		set[m.Identity] = m
	}
	return set
}

// TestFileAgainstFullSetModel drives File with random submissions,
// revocations and edit batches — including stale responses bound to old
// analysis versions — and after every step compares the merged output
// against the independently recomputed full-set model.
func TestFileAgainstFullSetModel(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	f := NewFile()
	model := newModelFile(20)
	stageNames := []string{"lint", "types", "lint", "security"} // lint intentionally twice
	rules := []string{"r0", "r1", "r2"}
	messages := []string{"m0", "m1"}

	for step := 0; step < 3000; step++ {
		switch rng.Intn(10) {
		case 0, 1, 2, 3, 4: // submit (possibly stale, possibly empty = revoke)
			stage := stageNames[rng.Intn(len(stageNames))]
			version := model.version
			if version > 0 {
				back := uint64(rng.Intn(3)) // may go stale by up to 2
				if back > version {
					back = version
				}
				version -= back
			}
			docLen := len(model.docs[version])
			var diags []Diagnostic
			for i := 0; i < rng.Intn(4); i++ {
				s := rng.Intn(docLen + 1)
				e := s + rng.Intn(docLen-s+1)
				diags = append(diags, Diagnostic{
					RuleID:   rules[rng.Intn(len(rules))],
					Severity: Severity(rng.Intn(4)),
					Message:  messages[rng.Intn(len(messages))],
					Range:    Range{s, e},
				})
			}
			if err := f.Submit(stage, version, diags); err != nil {
				t.Fatalf("step %d: Submit: %v", step, err)
			}
			model.submit(stage, version, diags)

		case 5, 6, 7: // apply an edit batch
			var edits []Edit
			docLen := len(model.docs[model.version])
			for i := 0; i < 1+rng.Intn(2); i++ {
				s := rng.Intn(docLen + 1)
				e := s + rng.Intn(docLen-s+1)
				edits = append(edits, Edit{s, e, rng.Intn(6)})
				docLen += edits[len(edits)-1].NewLen - (e - s)
			}
			f.ApplyEdits(edits...)
			model.applyEdits(edits)

		case 8: // revoke
			stage := stageNames[rng.Intn(len(stageNames))]
			f.Revoke(stage)
			delete(model.stages, stage)

		case 9: // future version must be rejected and change nothing
			if err := f.Submit("lint", model.version+1, []Diagnostic{d("r0", SeverityError, 0, 1)}); err != ErrFutureVersion {
				t.Fatalf("step %d: future submit err = %v", step, err)
			}
		}

		if f.Version() != model.version {
			t.Fatalf("step %d: version %d, model %d", step, f.Version(), model.version)
		}
		got := asSet(f.Diagnostics())
		want := asSet(model.merged())
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("step %d: merged mismatch\ngot:  %+v\nwant: %+v", step, got, want)
		}
	}
}
