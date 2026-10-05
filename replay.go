package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

// Entry is one record of the operation log (JSON Lines).
type Entry struct {
	Seq  int64  `json:"seq"`
	Op   string `json:"op"`             // create | rename | unlink
	Path string `json:"path,omitempty"` // create, unlink
	Old  string `json:"old,omitempty"`  // rename source
	New  string `json:"new,omitempty"`  // rename target
	ID   int64  `json:"id,omitempty"`   // create: stable node id
	Dir  bool   `json:"dir,omitempty"`  // create: is a directory
}

// StepReport describes the outcome of replaying one log entry.
type StepReport struct {
	Seq     int64
	Op      string
	OK      bool
	Err     string
	Affects map[string]int64 // path -> id touched by this step (after apply)
	Mapping map[string]int64 // full path -> id snapshot after this step
	Notes   []string         // anomalies detected at this step
}

// ReplayResult is the full outcome of a replay run.
type ReplayResult struct {
	Steps   []StepReport
	Mapping map[string]int64 // final path -> id mapping
	Failed  bool             // any step failed to apply
}

// ParseLog reads JSON-Lines log entries from r.
func ParseLog(r io.Reader) ([]Entry, error) {
	var entries []Entry
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<20)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		var e Entry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			return nil, fmt.Errorf("line %d: invalid log entry: %w", lineNo, err)
		}
		entries = append(entries, e)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return entries, nil
}

// pathState tracks what identity last lived at a path and whether it
// is currently alive, so we can flag delete-then-recreate at one path.
type pathState struct {
	lastID int64
	alive  bool
	seen   bool
}

// Replayer applies a log to a Tree while tracking identity and anomalies.
type Replayer struct {
	tree      *Tree
	seenSeq   map[int64]bool
	maxSeq    int64
	haveSeq   bool
	histories map[string]*pathState
}

func NewReplayer() *Replayer {
	return &Replayer{
		tree:      NewTree(),
		seenSeq:   map[int64]bool{},
		histories: map[string]*pathState{},
	}
}

func (r *Replayer) noteSeq(rep *StepReport, seq int64) {
	if r.seenSeq[seq] {
		rep.Notes = append(rep.Notes, fmt.Sprintf("duplicate log entry: seq %d already replayed", seq))
	}
	r.seenSeq[seq] = true
	if r.haveSeq && seq > r.maxSeq+1 {
		rep.Notes = append(rep.Notes, fmt.Sprintf("sequence gap: expected seq %d, got %d (missing %d entr%s)",
			r.maxSeq+1, seq, seq-r.maxSeq-1, plural(seq-r.maxSeq-1)))
	}
	if !r.haveSeq || seq > r.maxSeq {
		r.maxSeq = seq
	}
	r.haveSeq = true
}

func plural(n int64) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}

// markDead records that the node currently at path p (id id) is gone.
func (r *Replayer) markDead(p string, id int64) {
	st := r.histories[p]
	if st == nil {
		st = &pathState{}
		r.histories[p] = st
	}
	st.lastID = id
	st.alive = false
	st.seen = true
}

// markAlive records a live identity at path p and flags recreation.
func (r *Replayer) markAlive(rep *StepReport, p string, id int64) {
	st := r.histories[p]
	if st == nil {
		st = &pathState{}
		r.histories[p] = st
	}
	if st.seen && !st.alive && st.lastID != id {
		rep.Notes = append(rep.Notes, fmt.Sprintf("path %q recreated with new identity: id %d replaces deleted id %d",
			p, id, st.lastID))
	}
	st.lastID = id
	st.alive = true
	st.seen = true
}

// Apply replays one entry and returns its report. Failed entries leave
// the tree unchanged (operations are atomic) but are still recorded.
func (r *Replayer) Apply(e Entry) StepReport {
	rep := StepReport{Seq: e.Seq, Op: e.Op, Affects: map[string]int64{}}
	r.noteSeq(&rep, e.Seq)

	switch e.Op {
	case "create":
		if err := r.tree.Create(e.Path, e.ID, e.Dir); err != nil {
			rep.Err = err.Error()
			break
		}
		r.markAlive(&rep, e.Path, e.ID)
		rep.Affects[e.Path] = e.ID
		rep.OK = true

	case "unlink":
		n, err := r.tree.resolve(e.Path)
		if err != nil {
			rep.Err = fmt.Sprintf("unlink %q: %v", e.Path, err)
			break
		}
		id := n.ID
		if err := r.tree.Unlink(e.Path); err != nil {
			rep.Err = err.Error()
			break
		}
		r.markDead(e.Path, id)
		rep.Affects[e.Path] = id
		rep.OK = true

	case "rename":
		src, err := r.tree.resolve(e.Old)
		if err != nil {
			rep.Err = fmt.Sprintf("rename %q: %v", e.Old, err)
			break
		}
		srcID := src.ID
		// Capture the replaced target's identity before the move.
		var replacedID int64
		var replaced bool
		if plan, perr := r.tree.planRename(e.Old, e.New); perr == nil && plan.replaced != nil {
			replacedID = plan.replaced.ID
			replaced = true
		}
		if err := r.tree.Rename(e.Old, e.New); err != nil {
			rep.Err = err.Error()
			break
		}
		r.markDead(e.Old, srcID)
		if replaced {
			r.markDead(e.New, replacedID)
			rep.Affects[e.New+" (replaced)"] = replacedID
		}
		r.markAlive(&rep, e.New, srcID)
		rep.Affects[e.New] = srcID
		rep.OK = true

	default:
		rep.Err = fmt.Sprintf("unknown op %q", e.Op)
	}
	if !rep.OK {
		rep.Notes = append([]string{"transaction aborted: tree left unchanged"}, rep.Notes...)
	}
	return rep
}

// Replay applies all entries in order and returns the full result.
func (r *Replayer) Replay(entries []Entry) ReplayResult {
	res := ReplayResult{}
	for _, e := range entries {
		rep := r.Apply(e)
		rep.Mapping = r.tree.Mapping()
		if !rep.OK {
			res.Failed = true
		}
		res.Steps = append(res.Steps, rep)
	}
	res.Mapping = r.tree.Mapping()
	return res
}

// FormatStep renders one step report for output.
func FormatStep(rep StepReport, mapping map[string]int64) string {
	var b strings.Builder
	status := "ok"
	if !rep.OK {
		status = "FAIL"
	}
	fmt.Fprintf(&b, "seq=%d op=%s [%s]", rep.Seq, rep.Op, status)
	if rep.Err != "" {
		fmt.Fprintf(&b, " error=%q", rep.Err)
	}
	if len(rep.Affects) > 0 {
		paths := make([]string, 0, len(rep.Affects))
		for p := range rep.Affects {
			paths = append(paths, p)
		}
		sort.Strings(paths)
		var pairs []string
		for _, p := range paths {
			pairs = append(pairs, fmt.Sprintf("%s->#%d", p, rep.Affects[p]))
		}
		fmt.Fprintf(&b, " affects={%s}", strings.Join(pairs, ", "))
	}
	for _, n := range rep.Notes {
		fmt.Fprintf(&b, "\n    note: %s", n)
	}
	if mapping != nil {
		fmt.Fprintf(&b, "\n    mapping: %s", FormatMapping(mapping))
	}
	return b.String()
}

// FormatMapping renders a path -> id mapping in stable order.
func FormatMapping(m map[string]int64) string {
	paths := make([]string, 0, len(m))
	for p := range m {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	var pairs []string
	for _, p := range paths {
		pairs = append(pairs, fmt.Sprintf("%s=#%d", p, m[p]))
	}
	return "{" + strings.Join(pairs, ", ") + "}"
}
