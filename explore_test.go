package idedup

import (
	"fmt"
	"math/rand"
	"path/filepath"
	"testing"
)

// This file verifies the joint invariants of the dedup records, the
// business log and the reclamation watermark by state exploration:
// a bounded exhaustive BFS over interleaved operations, crashes and
// reclaims, plus a deeper randomized exploration. Every explored state
// is checked against a reference model; nothing relies on keeping all
// IDs forever (reclaim is part of the explored alphabet).

type step struct {
	kind    string // exec | confirm | reclaim | crash-exec | crash-reclaim
	id      string
	content string
	point   string
}

func (s step) String() string {
	switch s.kind {
	case "exec":
		return fmt.Sprintf("exec(%s,%s)", s.id, s.content)
	case "confirm":
		return fmt.Sprintf("confirm(%s)", s.id)
	case "reclaim":
		return "reclaim"
	case "crash-exec":
		return fmt.Sprintf("crash-exec(%s,%s)@%s", s.id, s.content, s.point)
	case "crash-reclaim":
		return "crash-reclaim@" + s.point
	}
	return s.kind
}

// invariantError aborts one explored path without killing the explorer.
type invariantError struct{ msg string }

func (e invariantError) Error() string { return e.msg }

// model is the reference state the store is checked against.
type model struct {
	order          []string // surviving committed IDs, commit order
	content        map[string]string
	reclaimedUpTo  string
	confirmedWM    string
	reclaimedCount uint64
	minRetain      int
}

func newModel(minRetain int) *model {
	return &model{content: map[string]string{}, minRetain: minRetain}
}

// expectedReclaim computes how many leading entries both gates allow to
// drop: caller-confirmed watermark AND the MinRetain retention rule.
func (m *model) expectedReclaim() int {
	k := 0
	for k < len(m.order) && m.order[k] <= m.confirmedWM {
		k++
	}
	protected := len(m.order) - m.minRetain
	if protected < 0 {
		protected = 0
	}
	if protected < k {
		return protected
	}
	return k
}

func (m *model) applyReclaim(n int) {
	if n == 0 {
		return
	}
	m.reclaimedUpTo = m.order[n-1]
	for _, id := range m.order[:n] {
		delete(m.content, id)
	}
	m.order = append([]string(nil), m.order[n:]...)
	m.reclaimedCount += uint64(n)
}

type env struct {
	dir string
	s   *Store
	m   *model
}

func (e *env) failf(format string, args ...interface{}) {
	panic(invariantError{fmt.Sprintf(format, args...)})
}

// check verifies every joint invariant of dedup records, business log
// and reclamation watermark against the reference model.
func (e *env) check(where string) {
	s, m := e.s, e.m
	log := s.BusinessLog()

	// I1: at most one business record per operation ID.
	seen := map[string]bool{}
	for _, en := range log {
		if seen[en.ID] {
			e.failf("%s: duplicate business record for %q", where, en.ID)
		}
		seen[en.ID] = true
	}
	// I2: business log == model survivors, same order, same content.
	if len(log) != len(m.order) {
		e.failf("%s: business log has %d records, model has %d", where, len(log), len(m.order))
	}
	for i, en := range log {
		if en.ID != m.order[i] {
			e.failf("%s: log[%d]=%q, model %q", where, i, en.ID, m.order[i])
		}
		if string(en.Content) != m.content[en.ID] {
			e.failf("%s: content of %q is %q, model %q", where, en.ID, en.Content, m.content[en.ID])
		}
		if string(en.Result) != "result:"+en.ID {
			e.failf("%s: result of %q is %q", where, en.ID, en.Result)
		}
	}
	// I3: dedup entries and business log are the same committed set.
	if len(s.entries) != len(s.order) || len(s.order) != len(log) {
		e.failf("%s: dedup/log size mismatch: entries=%d order=%d log=%d",
			where, len(s.entries), len(s.order), len(log))
	}
	for i, id := range s.order {
		if _, ok := s.entries[id]; !ok {
			e.failf("%s: order[%d]=%q missing dedup entry", where, i, id)
		}
		if i > 0 && s.order[i-1] >= id {
			e.failf("%s: commit order not monotonic at %q", where, id)
		}
	}
	// I4: reclamation never passed the caller-confirmed watermark.
	if m.reclaimedUpTo != "" && m.reclaimedUpTo > m.confirmedWM {
		e.failf("%s: reclaimed %q beyond confirmed %q", where, m.reclaimedUpTo, m.confirmedWM)
	}
	if got := s.ReclaimedWatermark(); got != m.reclaimedUpTo {
		e.failf("%s: reclaimed watermark %q, model %q", where, got, m.reclaimedUpTo)
	}
	if got := s.ConfirmedWatermark(); got != m.confirmedWM {
		e.failf("%s: confirmed watermark %q, model %q", where, got, m.confirmedWM)
	}
	if got := s.ReclaimedCount(); got != m.reclaimedCount {
		e.failf("%s: reclaimed count %d, model %d", where, got, m.reclaimedCount)
	}
	// I5: retention rule — the newest MinRetain committed entries live.
	total := int(m.reclaimedCount) + len(m.order)
	want := m.minRetain
	if total < want {
		want = total
	}
	if len(m.order) < want {
		e.failf("%s: retention violated: %d survivors, want >= %d", where, len(m.order), want)
	}
	// I6: survivors are all strictly above the reclaimed watermark.
	for _, id := range m.order {
		if m.reclaimedUpTo != "" && id <= m.reclaimedUpTo {
			e.failf("%s: survivor %q at/below reclaimed watermark %q", where, id, m.reclaimedUpTo)
		}
	}
}

func (e *env) reopen(where string) {
	if !e.s.crashed && !e.s.closed {
		e.s.Close()
	}
	s, err := Open(e.dir, Options{MinRetain: e.m.minRetain})
	if err != nil {
		e.failf("%s: reopen: %v", where, err)
	}
	e.s = s
	// Restart must not duplicate or lose anything.
	e.check(where + ":after-restart")
}

// runStep applies one step to the store and the model, checking the
// store's observable behavior against the model's expectation.
func (e *env) runStep(st step) {
	where := st.String()
	// A hook armed by a previous step whose operation never reached the
	// fault point must not leak into this step.
	e.s.SetFaultHook(nil)
	switch st.kind {
	case "exec", "crash-exec":
		if st.kind == "crash-exec" {
			e.s.SetFaultHook(armOnce(st.point))
		}
		_, dup := e.m.content[st.id]
		expired := e.m.reclaimedUpTo != "" && st.id <= e.m.reclaimedUpTo
		outOfOrder := !dup && !expired && len(e.m.order) > 0 && st.id <= e.m.order[len(e.m.order)-1]

		r, err := e.s.Execute(st.id, []byte(st.content), resultOf(st.id))
		switch {
		case dup && e.m.content[st.id] == st.content:
			if err != nil || string(r) != "result:"+st.id {
				e.failf("%s: retry got r=%q err=%v, want original result", where, r, err)
			}
		case dup:
			if err == nil || !isErr(err, ErrConflict) {
				e.failf("%s: got %v, want ErrConflict", where, err)
			}
		case expired:
			if err == nil || !isErr(err, ErrExpired) {
				e.failf("%s: got %v, want ErrExpired", where, err)
			}
		case outOfOrder:
			if err == nil || !isErr(err, ErrOutOfOrder) {
				e.failf("%s: got %v, want ErrOutOfOrder", where, err)
			}
		case st.kind == "crash-exec":
			if err == nil || !isErr(err, errInjected) {
				e.failf("%s: got %v, want injected crash", where, err)
			}
			committed := st.point == PointAfterSync || st.point == PointBeforeRespond
			if committed {
				e.m.order = append(e.m.order, st.id)
				e.m.content[st.id] = st.content
			}
			e.reopen(where)
			if committed {
				// Crash between commit and response: the retry must
				// return the original result, still exactly one record.
				r2, err2 := e.s.Execute(st.id, []byte(st.content), func() []byte {
					e.failf("%s: business function re-ran after crash", where)
					return nil
				})
				if err2 != nil || string(r2) != "result:"+st.id {
					e.failf("%s: retry after crash got r=%q err=%v", where, r2, err2)
				}
			}
		default:
			if err != nil || string(r) != "result:"+st.id {
				e.failf("%s: first execute got r=%q err=%v", where, r, err)
			}
			e.m.order = append(e.m.order, st.id)
			e.m.content[st.id] = st.content
		}

	case "confirm":
		if err := e.s.ConfirmWatermark(st.id); err != nil {
			e.failf("%s: %v", where, err)
		}
		if st.id > e.m.confirmedWM {
			e.m.confirmedWM = st.id
		}

	case "reclaim", "crash-reclaim":
		n := e.m.expectedReclaim()
		if st.kind == "crash-reclaim" && n > 0 {
			e.s.SetFaultHook(armOnce(st.point))
			if _, err := e.s.Reclaim(); err == nil || !isErr(err, errInjected) {
				e.failf("%s: want injected crash", where)
			}
			if st.point != PointSnapBeforeRename {
				e.m.applyReclaim(n) // snapshot was already durable
			}
			e.reopen(where)
		} else {
			got, err := e.s.Reclaim()
			if err != nil {
				e.failf("%s: %v", where, err)
			}
			if got != n {
				e.failf("%s: reclaimed %d, model expected %d", where, got, n)
			}
			e.m.applyReclaim(n)
		}
	}
	e.check(where)
}

func isErr(err, target error) bool {
	for err != nil {
		if err == target {
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

func (e *env) signature() string {
	b := ""
	for _, id := range e.m.order {
		b += id + "=" + e.m.content[id] + ";"
	}
	return fmt.Sprintf("wm=%s rw=%s rc=%d|%s", e.m.confirmedWM, e.m.reclaimedUpTo, e.m.reclaimedCount, b)
}

func runPath(t *testing.T, base string, seq int, steps []step, minRetain int) (sig string) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			if ie, ok := r.(invariantError); ok {
				t.Errorf("path %v: %v", steps, ie.msg)
				return
			}
			panic(r)
		}
	}()
	e := &env{dir: filepath.Join(base, fmt.Sprintf("n%d", seq)), m: newModel(minRetain)}
	s, err := Open(e.dir, Options{MinRetain: minRetain})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	e.s = s
	for _, st := range steps {
		e.runStep(st)
	}
	e.reopen("final")
	sig = e.signature()
	e.s.Close()
	return sig
}

// TestExploreJointInvariants enumerates all interleavings up to a bounded
// depth over executes, retries, conflicts, watermark confirms, reclaims
// and injected crashes, checking the joint invariants in every state.
func TestExploreJointInvariants(t *testing.T) {
	alphabet := []step{
		{kind: "exec", id: "1", content: "x"},
		{kind: "exec", id: "1", content: "y"}, // conflict with the above
		{kind: "exec", id: "2", content: "x"},
		{kind: "exec", id: "3", content: "x"},
		{kind: "confirm", id: "1"},
		{kind: "confirm", id: "2"},
		{kind: "reclaim"},
		{kind: "crash-exec", id: "1", content: "x", point: PointAfterSync},
		{kind: "crash-exec", id: "2", content: "x", point: PointAfterWrite},
		{kind: "crash-exec", id: "3", content: "x", point: PointBeforeRespond},
		{kind: "crash-reclaim", point: PointSnapBeforeRename},
		{kind: "crash-reclaim", point: PointSnapAfterRename},
	}
	const depth = 6
	const minRetain = 1
	base := t.TempDir()

	type node struct{ steps []step }
	seen := map[string]bool{"": true}
	queue := []node{{}}
	explored := 0
	dirSeq := 0
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		explored++
		if len(n.steps) >= depth {
			continue
		}
		for _, a := range alphabet {
			next := append(append([]step(nil), n.steps...), a)
			dirSeq++
			sig := runPath(t, base, dirSeq, next, minRetain)
			if t.Failed() {
				return
			}
			if !seen[sig] {
				seen[sig] = true
				queue = append(queue, node{next})
			}
		}
		if explored > 30000 {
			t.Fatalf("exploration did not converge: %d states", explored)
		}
	}
	t.Logf("explored %d paths, %d distinct states, depth %d", explored, len(seen), depth)
}

// TestExploreRandomized runs longer random sequences (deeper than the
// exhaustive bound) with a fixed seed set, checking the same invariants.
func TestExploreRandomized(t *testing.T) {
	ids := []string{"1", "2", "3", "4", "5"}
	contents := []string{"x", "y"}
	points := []string{
		PointBeforeWrite, PointAfterWrite, PointAfterSync, PointBeforeRespond,
		PointSnapBeforeRename, PointSnapAfterRename, PointSnapAfterTruncate,
	}
	base := t.TempDir()
	for seed := int64(0); seed < 200; seed++ {
		func() {
			defer func() {
				if r := recover(); r != nil {
					if ie, ok := r.(invariantError); ok {
						t.Errorf("seed %d: %v", seed, ie.msg)
						return
					}
					panic(r)
				}
			}()
			rng := rand.New(rand.NewSource(seed))
			e := &env{dir: filepath.Join(base, fmt.Sprintf("r%d", seed)), m: newModel(2)}
			s, err := Open(e.dir, Options{MinRetain: 2})
			if err != nil {
				t.Fatal(err)
			}
			e.s = s
			for i := 0; i < 40; i++ {
				var st step
				switch rng.Intn(8) {
				case 0, 1, 2, 3:
					st = step{kind: "exec", id: ids[rng.Intn(len(ids))], content: contents[rng.Intn(len(contents))]}
				case 4:
					st = step{kind: "confirm", id: ids[rng.Intn(len(ids))]}
				case 5:
					st = step{kind: "reclaim"}
				case 6:
					st = step{kind: "crash-exec", id: ids[rng.Intn(len(ids))], content: "x",
						point: points[rng.Intn(4)]}
				case 7:
					st = step{kind: "crash-reclaim", point: points[4+rng.Intn(3)]}
				}
				e.runStep(st)
			}
			e.reopen(fmt.Sprintf("seed %d final", seed))
			e.s.Close()
		}()
	}
}
