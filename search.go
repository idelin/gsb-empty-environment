package netsec

import (
	"container/heap"
	"fmt"
	"sort"
	"strings"
)

// SearchStep records one DML processed by the explorer.
type SearchStep struct {
	DML              DML
	EffectiveSubject string
	OriginSubject    string
	Issuer           *Frame
	Frames           []Frame
}

// Finding is a shortest trigger chain that writes a table the origin subject
// is not allowed to write.
type Finding struct {
	Found       bool
	Origin      string
	Root        DML
	StatementID string
	Steps       []SearchStep
	Frames      []Frame
	Writes      []WriteRecord
	Target      WriteRecord
	Reason      string

	// Differential fields. CorrectBlocked is true when replaying Root against
	// the correct model was rejected or did not produce Target.
	CorrectBlocked bool
	CorrectWrote   bool
	CorrectErr     error
	CorrectFrames  []Frame
	CorrectWrites  []WriteRecord
}

// String renders every frame of the chain, which is the main audit artifact a
// test host wants to print.
func (f *Finding) String() string {
	if f == nil || !f.Found {
		if f == nil {
			return "no finding"
		}
		if f.Reason != "" {
			return "no finding: " + f.Reason
		}
		return "no finding"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "origin=%s root=%s %s row %s stmt=%s target=%s %s row %s\n",
		f.Origin, f.Root.Event, f.Root.Table, f.Root.RowID, f.StatementID,
		f.Target.Event, f.Target.Table, f.Target.RowID)
	for i, frame := range f.Frames {
		fmt.Fprintf(&b, "  [%d] %s\n", i, frame.String())
	}
	if f.CorrectBlocked {
		b.WriteString("  correct-model replay: blocked or target not written\n")
	}
	for i, frame := range f.CorrectFrames {
		fmt.Fprintf(&b, "  correct[%d] %s\n", i, frame.String())
	}
	return b.String()
}

// SearchConfig configures BFS.
type SearchConfig struct {
	Origin string
	Starts []DML

	// MaxDepth overrides Engine.Options.MaxDepth for the search when > 0.
	MaxDepth int

	// MaxStates bounds the search. Zero defaults to 20000.
	MaxStates int

	// MaxFindings bounds collected findings. Zero defaults to 32.
	MaxFindings int
}

// BFS searches one model for the shortest chain that writes a table the origin
// subject cannot write.
func BFS(m *Engine, origin string, starts []DML, maxDepth int) (*Finding, error) {
	findings, err := bfsFindings(m, SearchConfig{Origin: origin, Starts: starts, MaxDepth: maxDepth})
	if err != nil && len(findings) == 0 {
		return nil, err
	}
	if len(findings) == 0 {
		return &Finding{Found: false, Origin: origin, Reason: "no unauthorized-write chain found"}, nil
	}
	return findings[0], nil
}

// SearchBFS is the SearchConfig form of BFS.
func SearchBFS(m *Engine, cfg SearchConfig) (*Finding, error) {
	findings, err := bfsFindings(m, cfg)
	if err != nil && len(findings) == 0 {
		return nil, err
	}
	if len(findings) == 0 {
		return &Finding{Found: false, Origin: cfg.Origin, Reason: "no unauthorized-write chain found"}, nil
	}
	return findings[0], nil
}

// DifferentialBFS finds a shortest pending-model chain whose unauthorized
// write is not reproduced by the correct model.
func DifferentialBFS(correct, pending *Engine, origin string, starts []DML, maxDepth int) (*Finding, error) {
	return DifferentialSearch(correct, pending, SearchConfig{Origin: origin, Starts: starts, MaxDepth: maxDepth})
}

// FindExploit is a one-start convenience wrapper around BFS.
func FindExploit(m *Engine, origin string, start DML) (*Finding, error) {
	if m == nil {
		return nil, fmt.Errorf("netsec: nil model")
	}
	return BFS(m, origin, []DML{start}, m.Options.MaxDepth)
}

// FindDifferential is a one-start convenience wrapper around DifferentialBFS.
func FindDifferential(correct, pending *Engine, origin string, start DML) (*Finding, error) {
	if pending == nil {
		return nil, fmt.Errorf("netsec: nil pending model")
	}
	return DifferentialBFS(correct, pending, origin, []DML{start}, pending.Options.MaxDepth)
}

// DifferentialSearch is the SearchConfig form of DifferentialBFS.
func DifferentialSearch(correct, pending *Engine, cfg SearchConfig) (*Finding, error) {
	if correct == nil || pending == nil {
		return nil, fmt.Errorf("netsec: differential search requires both models")
	}
	findings, err := bfsFindings(pending, cfg)
	if err != nil && len(findings) == 0 {
		return nil, err
	}
	if len(findings) == 0 {
		return &Finding{Found: false, Origin: cfg.Origin, Reason: "pending model has no unauthorized-write chain"}, nil
	}
	for _, finding := range findings {
		clone := cloneEngine(correct)
		res, err := clone.ExecuteAs(cfg.Origin, finding.Root)
		finding.CorrectFrames = append([]Frame(nil), res.Frames...)
		finding.CorrectWrites = append([]WriteRecord(nil), res.Writes...)
		if err != nil {
			finding.CorrectBlocked = true
			finding.CorrectErr = err
			finding.Reason = "pending chain is rejected by the correct model: " + err.Error()
			return finding, nil
		}
		wroteTarget := false
		for _, w := range res.Writes {
			if w.Table != finding.Target.Table || w.Event != finding.Target.Event || w.RowID != finding.Target.RowID {
				continue
			}
			if !originAllows(correct, cfg.Origin, w.Event, w.Table, w.RowID, w.Row, 0, w.StatementID) {
				wroteTarget = true
				break
			}
		}
		if !wroteTarget {
			finding.CorrectBlocked = true
			finding.Reason = "pending chain writes an unauthorized table; correct model does not"
			return finding, nil
		}
		finding.CorrectWrote = true
	}
	return &Finding{Found: false, Origin: cfg.Origin, Reason: "all pending findings are reproduced by the correct model"}, nil
}

func cloneEngine(src *Engine) *Engine {
	dst := &Engine{
		SessionSubject: src.SessionSubject,
		Tables:         make(map[string]*Table, len(src.Tables)),
		Options:        src.Options,
		AuditTable:     src.AuditTable,
	}
	for name, t := range src.Tables {
		nt := &Table{
			Name:   t.Name,
			Policy: t.Policy,
			Rows:   make(map[RowID]Row, len(t.Rows)),
			Before: append([]*Trigger(nil), t.Before...),
			After:  append([]*Trigger(nil), t.After...),
		}
		for id, r := range t.Rows {
			nt.Rows[id] = CloneRow(r)
		}
		dst.Tables[name] = nt
	}
	return dst
}

type searchDB struct {
	tables map[string]map[RowID]Row
}

func newSearchDB(e *Engine) *searchDB {
	d := &searchDB{tables: make(map[string]map[RowID]Row, len(e.Tables))}
	for name, t := range e.Tables {
		rows := make(map[RowID]Row, len(t.Rows))
		for id, r := range t.Rows {
			rows[id] = CloneRow(r)
		}
		d.tables[name] = rows
	}
	return d
}

func (d *searchDB) clone() *searchDB {
	out := &searchDB{tables: make(map[string]map[RowID]Row, len(d.tables))}
	for name, rows := range d.tables {
		nrows := make(map[RowID]Row, len(rows))
		for id, r := range rows {
			nrows[id] = CloneRow(r)
		}
		out.tables[name] = nrows
	}
	return out
}

func (d *searchDB) row(table string, id RowID) (Row, bool) {
	rows, ok := d.tables[table]
	if !ok {
		return nil, false
	}
	r, ok := rows[id]
	if !ok {
		return nil, false
	}
	return CloneRow(r), true
}

func (d *searchDB) set(table string, id RowID, row Row) {
	if d.tables[table] == nil {
		d.tables[table] = make(map[RowID]Row)
	}
	d.tables[table][id] = CloneRow(row)
}

func (d *searchDB) del(table string, id RowID) {
	if rows := d.tables[table]; rows != nil {
		delete(rows, id)
	}
}

type searchTrial struct {
	db       *searchDB
	frame    Frame
	stack    []Frame
	chain    []Frame
	visited  map[triggerRowKey]bool
	origin   string
	writes   []WriteRecord
	requests []searchRequest
}

type searchRequest struct {
	dml     DML
	db      *searchDB
	stack   []Frame
	chain   []Frame
	visited map[triggerRowKey]bool
	writes  []WriteRecord
}

func (t *searchTrial) record(dml DML) error {
	dml = cloneDML(dml)
	if dml.Event != EventInsert && dml.Event != EventUpdate && dml.Event != EventDelete {
		return fmt.Errorf("%w: %q", ErrUnknownEvent, dml.Event)
	}
	t.requests = append(t.requests, searchRequest{
		dml:     dml,
		db:      t.db.clone(),
		stack:   append([]Frame(nil), t.stack...),
		chain:   append([]Frame(nil), t.chain...),
		visited: cloneVisited(t.visited),
		writes:  append([]WriteRecord(nil), t.writes...),
	})
	return nil
}

func (t *searchTrial) query(table string, id RowID) (Row, bool) {
	return t.db.row(table, id)
}

type searchState struct {
	db       *searchDB
	dml      DML
	caller   string
	issuer   *Frame
	origin   string
	root     DML
	stack    []Frame
	chain    []Frame
	visited  map[triggerRowKey]bool
	stmtID   string
	steps    []SearchStep
	writes   []WriteRecord
	sequence int
}

func (s *searchState) clone() *searchState {
	out := *s
	out.db = s.db.clone()
	out.dml = cloneDML(s.dml)
	if s.issuer != nil {
		f := s.issuer.Clone()
		out.issuer = &f
	}
	out.stack = append([]Frame(nil), s.stack...)
	out.chain = append([]Frame(nil), s.chain...)
	out.visited = cloneVisited(s.visited)
	out.steps = append([]SearchStep(nil), s.steps...)
	out.writes = append([]WriteRecord(nil), s.writes...)
	return &out
}

func cloneVisited(src map[triggerRowKey]bool) map[triggerRowKey]bool {
	out := make(map[triggerRowKey]bool, len(src)+1)
	for k, v := range src {
		out[k] = v
	}
	return out
}

type stateHeap []*searchState

func (h stateHeap) Len() int { return len(h) }
func (h stateHeap) Less(i, j int) bool {
	if len(h[i].chain) != len(h[j].chain) {
		return len(h[i].chain) < len(h[j].chain)
	}
	return h[i].sequence < h[j].sequence
}
func (h stateHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *stateHeap) Push(x any)   { *h = append(*h, x.(*searchState)) }
func (h *stateHeap) Pop() any {
	old := *h
	n := len(old)
	item := old[n-1]
	*h = old[:n-1]
	return item
}

func bfsFindings(m *Engine, cfg SearchConfig) ([]*Finding, error) {
	if m == nil {
		return nil, fmt.Errorf("netsec: nil model")
	}
	if cfg.Origin == "" {
		return nil, fmt.Errorf("netsec: search requires an origin subject")
	}
	if err := m.Prepare(); err != nil {
		return nil, err
	}
	if cfg.MaxStates <= 0 {
		cfg.MaxStates = 20000
	}
	if cfg.MaxFindings <= 0 {
		cfg.MaxFindings = 32
	}
	depth := cfg.MaxDepth
	if depth <= 0 {
		depth = m.Options.MaxDepth
	}
	starts := append([]DML(nil), cfg.Starts...)
	if len(starts) == 0 {
		starts = enumerateStarts(m, cfg.Origin)
	}

	var findings []*Finding
	states := 0
	for i, start := range starts {
		start = cloneDML(start)
		initial := &searchState{
			db:       newSearchDB(m),
			dml:      start,
			caller:   cfg.Origin,
			origin:   cfg.Origin,
			root:     cloneDML(start),
			visited:  make(map[triggerRowKey]bool),
			stmtID:   fmt.Sprintf("search-%d-%d", i+1, 1),
			sequence: 0,
		}
		q := &stateHeap{initial}
		heap.Init(q)
		for q.Len() > 0 {
			state := heap.Pop(q).(*searchState)
			states++
			if states > cfg.MaxStates {
				return findings, fmt.Errorf("netsec: search exceeded %d states", cfg.MaxStates)
			}
			finding, successors := expandSearch(m, state, depth)
			if finding != nil {
				findings = append(findings, finding)
				if len(findings) >= cfg.MaxFindings {
					sortFindings(findings)
					return findings, nil
				}
			}
			for _, next := range successors {
				next.sequence = states + len(*q)
				heap.Push(q, next)
			}
		}
	}
	sortFindings(findings)
	return findings, nil
}

func sortFindings(findings []*Finding) {
	sort.SliceStable(findings, func(i, j int) bool {
		if len(findings[i].Frames) != len(findings[j].Frames) {
			return len(findings[i].Frames) < len(findings[j].Frames)
		}
		return findings[i].StatementID < findings[j].StatementID
	})
}

func expandSearch(m *Engine, s *searchState, maxDepth int) (*Finding, []*searchState) {
	dml := cloneDML(s.dml)
	table, ok := m.Tables[dml.Table]
	if !ok {
		return nil, nil
	}

	effective := s.caller
	if s.issuer != nil {
		if s.issuer.Security == SecurityDefiner {
			if hasTable(s.issuer.WritableTables, dml.Table) {
				effective = s.issuer.EffectiveSubject
			} else if !m.Options.EnforceDefinerWritable || m.Options.DefinerScope == DefinerScopeUnrestricted {
				effective = s.issuer.EffectiveSubject
			} else if m.Options.DefinerScope == DefinerScopeInvokerFallback {
				effective = s.issuer.InvokerSubject
			} else {
				return nil, nil
			}
		} else {
			effective = s.issuer.InvokerSubject
		}
	}

	if s.issuer != nil && m.Options.EnforceReentrancy &&
		dml.Event == s.root.Event && dml.Table == s.root.Table && dml.RowID == s.root.RowID {
		return nil, nil
	}

	row, _, ok := prepareSearchRow(s.db, dml)
	if !ok {
		return nil, nil
	}
	if !policyAllowsSearch(table, effective, s.origin, s.stmtID, dml, row, len(s.stack)) {
		return nil, nil
	}

	curDB := s.db.clone()
	curRow := CloneRow(row)
	chain := append([]Frame(nil), s.chain...)
	visited := cloneVisited(s.visited)
	framesFired := []Frame{}
	successors := []*searchState{}
	writesAtIssue := s.writes

	fire := func(triggers []*Trigger, timing Timing, allowModify bool) bool {
		for _, tr := range triggers {
			if !tr.matches(dml.Event) {
				continue
			}
			frame := makeSearchFrame(s, effective, tr, dml, m.Options.PreserveOrigin)
			if maxDepth > 0 && frame.Depth > maxDepth {
				return false
			}
			probe := &TriggerContext{Frame: frame, row: CloneRow(curRow)}
			if tr.When != nil && !tr.When(probe, CloneRow(curRow)) {
				continue
			}
			if m.Options.EnforceDepth && tr.MaxDepth > 0 && frame.Depth > tr.MaxDepth {
				return false
			}
			key := triggerRowKey{triggerID: tr.ID, table: dml.Table, id: dml.RowID}
			if m.Options.EnforcePerTriggerRow && visited[key] {
				return false
			}
			visited[key] = true

			trial := &searchTrial{
				db:      curDB.clone(),
				frame:   frame,
				stack:   append(append([]Frame(nil), s.stack...), frame),
				chain:   append(append([]Frame(nil), chain...), frame),
				visited: cloneVisited(visited),
				origin:  s.origin,
				writes:  append([]WriteRecord(nil), writesAtIssue...),
			}
			ctx := &TriggerContext{Frame: frame, row: CloneRow(curRow), search: trial}
			if allowModify && timing == TimingBefore && tr.Modify != nil {
				next, err := tr.Modify(ctx, CloneRow(curRow))
				if err != nil {
					return false
				}
				if next != nil {
					curRow = CloneRow(next)
				}
				ctx.row = CloneRow(curRow)
			}
			if tr.Body != nil {
				if err := tr.Body(ctx); err != nil {
					return false
				}
			}
			for _, action := range tr.Actions {
				if err := trial.record(action.DML()); err != nil {
					return false
				}
			}
			step := SearchStep{
				DML:              cloneDML(dml),
				EffectiveSubject: effective,
				OriginSubject:    s.origin,
				Frames:           append(append([]Frame(nil), framesFired...), frame),
			}
			if s.issuer != nil {
				f := s.issuer.Clone()
				step.Issuer = &f
			}
			for _, req := range trial.requests {
				issuerCopy := frame.Clone()
				next := &searchState{
					db:      req.db.clone(),
					dml:     cloneDML(req.dml),
					caller:  frame.InvokerSubject,
					issuer:  &issuerCopy,
					origin:  s.origin,
					root:    cloneDML(s.root),
					stack:   append([]Frame(nil), req.stack...),
					chain:   append([]Frame(nil), req.chain...),
					visited: cloneVisited(req.visited),
					stmtID:  s.stmtID,
					steps:   append(append([]SearchStep(nil), s.steps...), step),
					writes:  append([]WriteRecord(nil), req.writes...),
				}
				successors = append(successors, next)
			}
			frameCopy := frame.Clone()
			chain = append(chain, frameCopy)
			framesFired = append(framesFired, frameCopy)
		}
		return true
	}

	if !fire(table.Before, TimingBefore, true) {
		return nil, nil
	}
	if m.Options.RecheckPolicyAfterBefore && !policyAllowsSearch(table, effective, s.origin, s.stmtID, dml, curRow, len(s.stack)) {
		return nil, nil
	}

	// Apply the base write after all BEFORE triggers have accepted the row.
	switch dml.Event {
	case EventInsert, EventUpdate:
		curDB.set(dml.Table, dml.RowID, curRow)
	case EventDelete:
		curDB.del(dml.Table, dml.RowID)
	}
	baseWrite := WriteRecord{
		Event:       dml.Event,
		Table:       dml.Table,
		RowID:       dml.RowID,
		Row:         CloneRow(curRow),
		Subject:     effective,
		Origin:      s.origin,
		StatementID: s.stmtID,
		TriggerID:   frameTriggerID(s.issuer),
		Depth:       len(s.stack),
	}

	writesAtIssue = append(append([]WriteRecord(nil), s.writes...), baseWrite)
	if !fire(table.After, TimingAfter, false) {
		// AFTER failure rolls the whole statement back, including the base
		// write and any audit rows.
		return nil, nil
	}

	step := SearchStep{
		DML:              cloneDML(dml),
		EffectiveSubject: effective,
		OriginSubject:    s.origin,
		Frames:           append([]Frame(nil), framesFired...),
	}
	if s.issuer != nil {
		f := s.issuer.Clone()
		step.Issuer = &f
	}
	steps := append(append([]SearchStep(nil), s.steps...), step)
	writes := append(append([]WriteRecord(nil), s.writes...), baseWrite)

	if !originAllows(m, s.origin, dml.Event, dml.Table, dml.RowID, curRow, len(s.stack), s.stmtID) {
		return &Finding{
			Found:       true,
			Origin:      s.origin,
			Root:        cloneDML(s.root),
			StatementID: s.stmtID,
			Steps:       steps,
			Frames:      append([]Frame(nil), chain...),
			Writes:      writes,
			Target:      baseWrite,
			Reason: fmt.Sprintf("origin %s may not %s %s row %s, but the chain wrote it as %s",
				s.origin, dml.Event, dml.Table, dml.RowID, effective),
		}, successors
	}

	// For direct actions of the current DML, each action branch continues from
	// the database snapshot captured at the point the action was issued.
	for _, next := range successors {
		next.steps = steps
	}
	return nil, successors
}

func makeSearchFrame(s *searchState, effective string, tr *Trigger, dml DML, preserveOrigin bool) Frame {
	origin := s.origin
	if s.issuer != nil {
		origin = s.issuer.OriginSubject
	}
	frameEffective := effective
	if tr.security() == SecurityDefiner {
		frameEffective = tr.Definer
		if !preserveOrigin {
			origin = tr.Definer
		}
	}
	return Frame{
		TriggerID:        tr.ID,
		Table:            dml.Table,
		EventTable:       dml.Table,
		RowID:            dml.RowID,
		Event:            dml.Event,
		EffectiveSubject: frameEffective,
		InvokerSubject:   effective,
		OriginSubject:    origin,
		StatementID:      s.stmtID,
		Timing:           normaliseTiming(tr.Timing),
		Security:         tr.security(),
		Definer:          tr.Definer,
		Depth:            len(s.stack) + 1,
		WritableTables:   append([]string(nil), tr.WritableTables...),
		RootEvent:        s.root.Event,
		RootTable:        s.root.Table,
		RootRowID:        s.root.RowID,
	}
}

func prepareSearchRow(db *searchDB, dml DML) (Row, Row, bool) {
	existing, exists := db.row(dml.Table, dml.RowID)
	switch dml.Event {
	case EventInsert:
		if exists {
			return nil, nil, false
		}
		return CloneRow(dml.Row), nil, true
	case EventUpdate:
		if !exists {
			return nil, nil, false
		}
		merged := CloneRow(existing)
		for k, v := range dml.Row {
			merged[k] = v
		}
		return merged, CloneRow(existing), true
	case EventDelete:
		if !exists {
			return nil, nil, false
		}
		return CloneRow(existing), CloneRow(existing), true
	default:
		return nil, nil, false
	}
}

func policyAllowsSearch(t *Table, subject, origin, stmtID string, dml DML, row Row, depth int) bool {
	if t.Policy == nil {
		return false
	}
	return t.Policy.Allows(PolicyContext{
		Subject:          subject,
		EffectiveSubject: subject,
		OriginSubject:    origin,
		Table:            dml.Table,
		Event:            dml.Event,
		RowID:            dml.RowID,
		Row:              CloneRow(row),
		Depth:            depth,
		StatementID:      stmtID,
	})
}

func originAllows(m *Engine, origin string, event Event, tableName string, id RowID, row Row, depth int, stmtID string) bool {
	t, ok := m.Tables[tableName]
	if !ok {
		return false
	}
	if t.Policy == nil {
		return false
	}
	return t.Policy.Allows(PolicyContext{
		Subject:          origin,
		EffectiveSubject: origin,
		OriginSubject:    origin,
		Table:            tableName,
		Event:            event,
		RowID:            id,
		Row:              CloneRow(row),
		Depth:            depth,
		StatementID:      stmtID,
	})
}

func frameTriggerID(f *Frame) string {
	if f == nil {
		return ""
	}
	return f.TriggerID
}

func enumerateStarts(m *Engine, origin string) []DML {
	names := make([]string, 0, len(m.Tables))
	for name := range m.Tables {
		names = append(names, name)
	}
	sort.Strings(names)
	var starts []DML
	for _, name := range names {
		t := m.Tables[name]
		ids := make([]string, 0, len(t.Rows))
		for id := range t.Rows {
			ids = append(ids, string(id))
		}
		sort.Strings(ids)
		for _, sid := range ids {
			id := RowID(sid)
			for _, event := range []Event{EventUpdate, EventDelete} {
				row, _ := t.Rows[id]
				if originAllows(m, origin, event, name, id, row, 0, "") {
					starts = append(starts, DML{Event: event, Table: name, RowID: id})
				}
			}
		}
		insertID := RowID("new-" + name)
		if _, exists := t.Rows[insertID]; !exists && originAllows(m, origin, EventInsert, name, insertID, Row{}, 0, "") {
			starts = append(starts, DML{Event: EventInsert, Table: name, RowID: insertID, Row: Row{}})
		}
	}
	return starts
}
