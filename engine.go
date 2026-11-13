package netsec

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

// DefinerScope controls what happens when a definer trigger tries to write a
// table outside its declared WritableTables set.
type DefinerScope int

const (
	// DefinerScopeDeclared is the secure default: the write is denied.
	DefinerScopeDeclared DefinerScope = iota

	// DefinerScopeInvokerFallback retries the write with the invoker's
	// effective subject. This is a legitimate SQL-like fallback for callers
	// that want "definer only inside the whitelist, invoker elsewhere".
	DefinerScopeInvokerFallback

	// DefinerScopeUnrestricted lets a definer write any table. This models the
	// pending-review implementation where the writable-table whitelist is
	// missing.
	DefinerScopeUnrestricted
)

// Options controls the security checks performed by an Engine.
//
// The zero value is not the secure default; use StrictOptions, NewEngine, or
// PendingOptions to obtain a fully populated value.
type Options struct {
	EnforceReentrancy        bool
	EnforcePerTriggerRow     bool
	EnforceDepth             bool
	EnforceDefinerWritable   bool
	PreserveOrigin           bool
	RecheckPolicyAfterBefore bool

	DefinerScope DefinerScope

	// MaxDepth is the maximum trigger nesting depth. Zero defaults to 32.
	MaxDepth int
}

// StrictOptions is the correct, fail-closed model.
func StrictOptions() Options {
	return Options{
		EnforceReentrancy:        true,
		EnforcePerTriggerRow:     true,
		EnforceDepth:             true,
		EnforceDefinerWritable:   true,
		PreserveOrigin:           true,
		RecheckPolicyAfterBefore: true,
		DefinerScope:             DefinerScopeDeclared,
		MaxDepth:                 32,
	}
}

// CorrectOptions is an alias for StrictOptions.
func CorrectOptions() Options { return StrictOptions() }

// PendingOptions models the implementation under review: it has the trigger
// stack and recursion limit, but it fails to contain definer writes and lets a
// nested definer reset the origin subject. The other checks remain enabled so
// the differential search isolates the definer-context flaw.
func PendingOptions() Options {
	o := StrictOptions()
	o.EnforceDefinerWritable = false
	o.DefinerScope = DefinerScopeUnrestricted
	o.PreserveOrigin = false
	return o
}

// PermissiveOptions disables every guard. It is useful in tests that need to
// demonstrate the attack primitive without the protections.
func PermissiveOptions() Options {
	return Options{
		EnforceReentrancy:        false,
		EnforcePerTriggerRow:     false,
		EnforceDepth:             false,
		EnforceDefinerWritable:   false,
		PreserveOrigin:           false,
		RecheckPolicyAfterBefore: false,
		DefinerScope:             DefinerScopeUnrestricted,
		MaxDepth:                 32,
	}
}

func normaliseOptions(o Options) Options {
	if o.MaxDepth <= 0 {
		o.MaxDepth = 32
	}
	return o
}

// Engine is an in-memory database with trigger execution.
type Engine struct {
	mu sync.Mutex

	SessionSubject string
	Tables         map[string]*Table
	Options        Options

	// AuditTable is informational; audit writes are ordinary table writes and
	// are included in statement rollback like every other table.
	AuditTable string

	stmtSeq uint64
	last    *Result
}

// Model is an alias for Engine, for hosts that prefer model terminology.
type Model = Engine

// NewEngine creates a strict engine for a session subject.
func NewEngine(subject string) *Engine {
	return NewEngineWithOptions(subject, StrictOptions())
}

// NewModel is an alias for NewEngine.
func NewModel(subject string) *Model { return NewEngine(subject) }

// NewPendingEngine creates an engine using PendingOptions.
func NewPendingEngine(subject string) *Engine {
	return NewEngineWithOptions(subject, PendingOptions())
}

// NewEngineWithOptions creates an engine with explicit options.
func NewEngineWithOptions(subject string, opts Options) *Engine {
	return &Engine{
		SessionSubject: subject,
		Tables:         make(map[string]*Table),
		Options:        normaliseOptions(opts),
	}
}

// SetSessionSubject changes the subject used by future Execute calls. Trigger
// code cannot call this; it receives a TriggerContext instead.
func (e *Engine) SetSessionSubject(subject string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.SessionSubject = subject
}

// Session returns the current session subject.
func (e *Engine) Session() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.SessionSubject
}

// AddTable registers a table.
func (e *Engine) AddTable(t *Table) error {
	if t == nil || t.Name == "" {
		return fmt.Errorf("netsec: table with empty name")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.Tables == nil {
		e.Tables = make(map[string]*Table)
	}
	if _, exists := e.Tables[t.Name]; exists {
		return fmt.Errorf("netsec: duplicate table %q", t.Name)
	}
	if t.Rows == nil {
		t.Rows = make(map[RowID]Row)
	}
	e.Tables[t.Name] = t
	return nil
}

// Table returns a registered table.
func (e *Engine) GetTable(name string) (*Table, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	t, ok := e.Tables[name]
	return t, ok
}

// RegisterTrigger registers a trigger on its Table field.
func (e *Engine) RegisterTrigger(tr *Trigger) error {
	if tr == nil || tr.ID == "" {
		return fmt.Errorf("netsec: trigger with empty ID")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.prepareLocked(); err != nil {
		return err
	}
	t, ok := e.Tables[tr.Table]
	if !ok {
		return fmt.Errorf("netsec: trigger %s references unknown table %q", tr.ID, tr.Table)
	}
	if err := t.AddTrigger(tr); err != nil {
		return err
	}
	return e.prepareLocked()
}

// Prepare validates registrations and fills in derived trigger fields.
func (e *Engine) Prepare() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.prepareLocked()
}

func (e *Engine) prepareLocked() error {
	seen := make(map[string]string)
	names := make([]string, 0, len(e.Tables))
	for name := range e.Tables {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		t := e.Tables[name]
		if t == nil {
			return fmt.Errorf("netsec: nil table %q", name)
		}
		if t.Name == "" {
			t.Name = name
		}
		if t.Name != name {
			return fmt.Errorf("netsec: table key %q does not match name %q", name, t.Name)
		}
		if t.Rows == nil {
			t.Rows = make(map[RowID]Row)
		}
		for _, list := range []struct {
			timing Timing
			items  []*Trigger
		}{
			{TimingBefore, t.Before},
			{TimingAfter, t.After},
		} {
			for i, tr := range list.items {
				if tr == nil {
					return fmt.Errorf("netsec: nil trigger in %s.%s[%d]", name, list.timing, i)
				}
				if tr.ID == "" {
					return fmt.Errorf("netsec: trigger in %s.%s[%d] has empty ID", name, list.timing, i)
				}
				if other, ok := seen[tr.ID]; ok {
					return fmt.Errorf("%w: %s in %s and %s", ErrDuplicateTrigger, tr.ID, other, name)
				}
				seen[tr.ID] = name
				if tr.Table != "" && tr.Table != name {
					return fmt.Errorf("netsec: trigger %s registered under %s but declares table %s", tr.ID, name, tr.Table)
				}
				tr.Table = name
				if tr.Timing == "" {
					tr.Timing = list.timing
				} else if normaliseTiming(tr.Timing) != list.timing {
					return fmt.Errorf("netsec: trigger %s timing %s is in the %s list", tr.ID, tr.Timing, list.timing)
				}
				if tr.security() == SecurityDefiner && tr.Definer == "" {
					return fmt.Errorf("netsec: definer trigger %s has no definer subject", tr.ID)
				}
			}
		}
	}
	return nil
}

// Execute runs one statement as the current session subject.
func (e *Engine) Execute(dml DML) (*Result, error) {
	e.mu.Lock()
	subject := e.SessionSubject
	e.mu.Unlock()
	return e.ExecuteAs(subject, dml)
}

// Exec is a short alias for Execute.
func (e *Engine) Exec(dml DML) (*Result, error) { return e.Execute(dml) }

// ExecuteDML is an alias for ExecuteAs.
func (e *Engine) ExecuteDML(subject string, dml DML) (*Result, error) {
	return e.ExecuteAs(subject, dml)
}

// ExecuteAs runs one statement as subject. The subject is the origin subject
// for the whole statement; triggers cannot change it.
func (e *Engine) ExecuteAs(subject string, dml DML) (*Result, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if err := e.prepareLocked(); err != nil {
		return &Result{Origin: subject, Err: err}, err
	}
	dml.Event = normaliseEvent(dml.Event)
	if dml.Event != EventInsert && dml.Event != EventUpdate && dml.Event != EventDelete {
		err := fmt.Errorf("%w: %q", ErrUnknownEvent, dml.Event)
		return &Result{Origin: subject, Err: err}, err
	}
	st := &statement{
		id:        fmt.Sprintf("stmt-%d", atomic.AddUint64(&e.stmtSeq, 1)),
		origin:    subject,
		root:      cloneDML(dml),
		visited:   make(map[triggerRowKey]bool),
		snapshots: make(map[rowKey]rowSnapshot),
	}
	err := e.executeDML(st, nil, subject, dml)
	res := &Result{
		StatementID: st.id,
		Origin:      subject,
		Frames:      append([]Frame(nil), st.frames...),
		Writes:      append([]WriteRecord(nil), st.writes...),
		Err:         err,
	}
	if err != nil {
		e.rollback(st)
		res.RolledBack = true
	} else {
		st.committed = true
	}
	e.last = res
	return res, err
}

// LastResult returns the most recent statement result.
func (e *Engine) LastResult() *Result {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.last == nil {
		return nil
	}
	out := *e.last
	out.Frames = append([]Frame(nil), e.last.Frames...)
	out.Writes = append([]WriteRecord(nil), e.last.Writes...)
	return &out
}

type rowKey struct {
	table string
	id    RowID
}

type rowSnapshot struct {
	existed bool
	row     Row
}

type triggerRowKey struct {
	triggerID string
	table     string
	id        RowID
}

type statement struct {
	id        string
	origin    string
	root      DML
	frames    []Frame
	stack     []Frame
	visited   map[triggerRowKey]bool
	writes    []WriteRecord
	snapshots map[rowKey]rowSnapshot
	committed bool
}

func cloneDML(d DML) DML {
	d.Event = normaliseEvent(d.Event)
	d.Row = CloneRow(d.Row)
	return d
}

func (e *Engine) executeDML(st *statement, issuer *Frame, subject string, dml DML) error {
	dml = cloneDML(dml)
	if dml.Event != EventInsert && dml.Event != EventUpdate && dml.Event != EventDelete {
		return fmt.Errorf("%w: %q", ErrUnknownEvent, dml.Event)
	}
	if strings.TrimSpace(dml.Table) == "" {
		return fmt.Errorf("%w: empty table", ErrNoTable)
	}
	table, ok := e.Tables[dml.Table]
	if !ok {
		return fmt.Errorf("%w: %q", ErrNoTable, dml.Table)
	}

	// A trigger that re-issues the original event on the original row is a
	// re-entrant write, regardless of which trigger issued it.
	if issuer != nil && e.Options.EnforceReentrancy &&
		dml.Event == st.root.Event &&
		dml.Table == st.root.Table &&
		dml.RowID == st.root.RowID {
		return fmt.Errorf("%w: %s %s row %s in %s", ErrReentrant, dml.Event, dml.Table, dml.RowID, st.id)
	}

	row, existing, err := prepareDMLRow(table, dml)
	if err != nil {
		return err
	}
	if !e.policyAllows(table, subject, st, dml, row) {
		return fmt.Errorf("%w: subject %s may not %s %s row %s", ErrPolicyDenied, subject, dml.Event, dml.Table, dml.RowID)
	}

	// BEFORE triggers run against the incoming row. Each successful trigger
	// may replace the row for the next trigger and for the base write.
	for _, tr := range table.Before {
		if !tr.matches(dml.Event) {
			continue
		}
		frame, err := e.makeFrame(st, issuer, subject, tr, dml)
		if err != nil {
			return err
		}
		ctx := &TriggerContext{Frame: frame, row: CloneRow(row), engine: e, st: st}
		if tr.When != nil && !tr.When(ctx, CloneRow(row)) {
			continue
		}
		if err := e.checkTriggerGuard(st, tr, dml, &frame); err != nil {
			return err
		}
		e.markTrigger(st, tr, dml)
		row, err = e.runTrigger(ctx, tr, row, TimingBefore, true)
		if err != nil {
			return err
		}
	}

	if e.Options.RecheckPolicyAfterBefore && !e.policyAllows(table, subject, st, dml, row) {
		return fmt.Errorf("%w: subject %s may not write final row in %s", ErrPolicyAfterBefore, subject, dml.Table)
	}

	if err := e.applyWrite(st, table, dml, row, subject, issuer, existing); err != nil {
		return err
	}

	for _, tr := range table.After {
		if !tr.matches(dml.Event) {
			continue
		}
		frame, err := e.makeFrame(st, issuer, subject, tr, dml)
		if err != nil {
			return err
		}
		ctx := &TriggerContext{Frame: frame, row: CloneRow(row), engine: e, st: st}
		if tr.When != nil && !tr.When(ctx, CloneRow(row)) {
			continue
		}
		if err := e.checkTriggerGuard(st, tr, dml, &frame); err != nil {
			return err
		}
		e.markTrigger(st, tr, dml)
		if _, err := e.runTrigger(ctx, tr, row, TimingAfter, false); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) policyAllows(t *Table, subject string, st *statement, dml DML, row Row) bool {
	if t.Policy == nil {
		return false
	}
	return t.Policy.Allows(PolicyContext{
		Subject:          subject,
		EffectiveSubject: subject,
		OriginSubject:    st.origin,
		Table:            t.Name,
		Event:            dml.Event,
		RowID:            dml.RowID,
		Row:              CloneRow(row),
		Depth:            len(st.stack),
		StatementID:      st.id,
	})
}

func prepareDMLRow(t *Table, dml DML) (Row, Row, error) {
	existing, exists := t.Rows[dml.RowID]
	switch dml.Event {
	case EventInsert:
		if exists {
			return nil, nil, fmt.Errorf("%w: %s row %s", ErrRowExists, dml.Table, dml.RowID)
		}
		return CloneRow(dml.Row), nil, nil
	case EventUpdate:
		if !exists {
			return nil, nil, fmt.Errorf("%w: %s row %s", ErrRowNotFound, dml.Table, dml.RowID)
		}
		merged := CloneRow(existing)
		for k, v := range dml.Row {
			merged[k] = v
		}
		return merged, CloneRow(existing), nil
	case EventDelete:
		if !exists {
			return nil, nil, fmt.Errorf("%w: %s row %s", ErrRowNotFound, dml.Table, dml.RowID)
		}
		return CloneRow(existing), CloneRow(existing), nil
	default:
		return nil, nil, fmt.Errorf("%w: %q", ErrUnknownEvent, dml.Event)
	}
}

func (e *Engine) makeFrame(st *statement, issuer *Frame, subject string, tr *Trigger, dml DML) (Frame, error) {
	origin := st.origin
	if issuer != nil {
		origin = issuer.OriginSubject
	}
	effective := subject
	sec := tr.security()
	if sec == SecurityDefiner {
		if tr.Definer == "" {
			return Frame{}, fmt.Errorf("%w: %s", ErrDefinerMissing, tr.ID)
		}
		effective = tr.Definer
		if !e.Options.PreserveOrigin {
			origin = tr.Definer
		}
	}
	depth := len(st.stack) + 1
	return Frame{
		TriggerID:        tr.ID,
		Table:            dml.Table,
		EventTable:       dml.Table,
		RowID:            dml.RowID,
		Event:            dml.Event,
		EffectiveSubject: effective,
		InvokerSubject:   subject,
		OriginSubject:    origin,
		StatementID:      st.id,
		Timing:           normaliseTiming(tr.Timing),
		Security:         sec,
		Definer:          tr.Definer,
		Depth:            depth,
		WritableTables:   append([]string(nil), tr.WritableTables...),
		RootEvent:        st.root.Event,
		RootTable:        st.root.Table,
		RootRowID:        st.root.RowID,
	}, nil
}

func (e *Engine) checkTriggerGuard(st *statement, tr *Trigger, dml DML, frame *Frame) error {
	if e.Options.EnforceDepth {
		if e.Options.MaxDepth > 0 && frame.Depth > e.Options.MaxDepth {
			return fmt.Errorf("%w: depth %d > %d at trigger %s", ErrMaxDepth, frame.Depth, e.Options.MaxDepth, tr.ID)
		}
		if tr.MaxDepth > 0 && frame.Depth > tr.MaxDepth {
			return fmt.Errorf("%w: depth %d > %d at trigger %s", ErrTriggerMaxDepth, frame.Depth, tr.MaxDepth, tr.ID)
		}
	}
	if e.Options.EnforcePerTriggerRow {
		key := triggerRowKey{triggerID: tr.ID, table: dml.Table, id: dml.RowID}
		if st.visited[key] {
			return fmt.Errorf("%w: trigger %s on %s row %s", ErrTriggerRowOnce, tr.ID, dml.Table, dml.RowID)
		}
	}
	return nil
}

func (e *Engine) markTrigger(st *statement, tr *Trigger, dml DML) {
	key := triggerRowKey{triggerID: tr.ID, table: dml.Table, id: dml.RowID}
	st.visited[key] = true
}

func (e *Engine) runTrigger(ctx *TriggerContext, tr *Trigger, row Row, timing Timing, allowModify bool) (Row, error) {
	out := CloneRow(row)

	// The frame is pushed before Modify so nested DML issued from a BEFORE
	// trigger also sees the correct recursion depth.
	ctx.st.stack = append(ctx.st.stack, ctx.Frame)
	ctx.st.frames = append(ctx.st.frames, ctx.Frame.Clone())
	defer func() {
		ctx.st.stack = ctx.st.stack[:len(ctx.st.stack)-1]
	}()

	if allowModify && timing == TimingBefore && tr.Modify != nil {
		next, err := tr.Modify(ctx, CloneRow(out))
		if err != nil {
			return nil, fmt.Errorf("trigger %s rejected %s row %s: %w", tr.ID, ctx.Frame.Event, ctx.Frame.RowID, err)
		}
		if next != nil {
			out = CloneRow(next)
		}
		ctx.row = CloneRow(out)
	}

	if tr.Body != nil {
		if err := tr.Body(ctx); err != nil {
			return nil, fmt.Errorf("trigger %s failed: %w", tr.ID, err)
		}
	}
	for i, action := range tr.Actions {
		if err := ctx.Exec(action.DML()); err != nil {
			return nil, fmt.Errorf("trigger %s action %d: %w", tr.ID, i, err)
		}
	}
	return out, nil
}

func (e *Engine) applyWrite(st *statement, table *Table, dml DML, row Row, subject string, issuer *Frame, existing Row) error {
	key := rowKey{table: table.Name, id: dml.RowID}
	if _, saved := st.snapshots[key]; !saved {
		st.snapshots[key] = rowSnapshot{existed: existing != nil, row: CloneRow(existing)}
	}
	switch dml.Event {
	case EventDelete:
		delete(table.Rows, dml.RowID)
	case EventInsert, EventUpdate:
		table.Rows[dml.RowID] = CloneRow(row)
	}
	trID := ""
	if issuer != nil {
		trID = issuer.TriggerID
	}
	st.writes = append(st.writes, WriteRecord{
		Event:       dml.Event,
		Table:       table.Name,
		RowID:       dml.RowID,
		Row:         CloneRow(row),
		Subject:     subject,
		Origin:      st.origin,
		StatementID: st.id,
		TriggerID:   trID,
		Depth:       len(st.stack),
	})
	return nil
}

func (e *Engine) rollback(st *statement) {
	for key, snap := range st.snapshots {
		table, ok := e.Tables[key.table]
		if !ok {
			continue
		}
		if snap.existed {
			table.Rows[key.id] = CloneRow(snap.row)
		} else {
			delete(table.Rows, key.id)
		}
	}
}

// executeNested resolves the subject for a DML issued from a trigger frame.
func (e *Engine) executeNested(st *statement, issuer *Frame, dml DML) error {
	subject, err := e.nestedSubject(issuer, dml.Table)
	if err != nil {
		return err
	}
	return e.executeDML(st, issuer, subject, dml)
}

func (e *Engine) nestedSubject(issuer *Frame, table string) (string, error) {
	if issuer == nil {
		return "", fmt.Errorf("netsec: nested DML without issuer")
	}
	if issuer.Security != SecurityDefiner {
		return issuer.InvokerSubject, nil
	}
	if hasTable(issuer.WritableTables, table) {
		return issuer.EffectiveSubject, nil
	}
	if !e.Options.EnforceDefinerWritable || e.Options.DefinerScope == DefinerScopeUnrestricted {
		return issuer.EffectiveSubject, nil
	}
	if e.Options.DefinerScope == DefinerScopeInvokerFallback {
		return issuer.InvokerSubject, nil
	}
	return "", fmt.Errorf("%w: trigger %s (definer %s) may not write %s", ErrDefinerTable, issuer.TriggerID, issuer.Definer, table)
}
