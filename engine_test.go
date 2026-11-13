package netsec

import (
	"errors"
	"strings"
	"testing"
)

func mustAdd(t *testing.T, e *Engine, tables ...*Table) {
	t.Helper()
	for _, table := range tables {
		if err := e.AddTable(table); err != nil {
			t.Fatalf("AddTable(%s): %v", table.Name, err)
		}
	}
	if err := e.Prepare(); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
}

func TestBEFOREModificationRechecksPolicy(t *testing.T) {
	docs := NewTable("docs")
	docs.Policy = PolicyFunc(func(c PolicyContext) bool {
		return c.Row["status"] == "public"
	})
	docs.AddRow("r1", Row{"status": "public", "title": "old"})

	if err := docs.AddBefore(&Trigger{
		ID:       "corrupt",
		Event:    EventUpdate,
		Security: SecurityInvoker,
		Modify: func(ctx *TriggerContext, row Row) (Row, error) {
			row["status"] = "secret"
			return row, nil
		},
	}); err != nil {
		t.Fatal(err)
	}

	e := NewEngine("clerk")
	mustAdd(t, e, docs)
	res, err := e.Execute(DML{Event: EventUpdate, Table: "docs", RowID: "r1", Row: Row{"title": "new"}})
	if !errors.Is(err, ErrPolicyAfterBefore) {
		t.Fatalf("got err %v, want ErrPolicyAfterBefore", err)
	}
	if !res.RolledBack {
		t.Fatal("statement should be rolled back")
	}
	row, _ := docs.Row("r1")
	if row["status"] != "public" || row["title"] != "old" {
		t.Fatalf("row changed despite policy rejection: %#v", row)
	}
	if len(res.Frames) != 1 || res.Frames[0].TriggerID != "corrupt" {
		t.Fatalf("frames = %#v", res.Frames)
	}
}

func TestNilPolicyFailsClosed(t *testing.T) {
	a := NewTable("A")
	e := NewEngine("clerk")
	mustAdd(t, e, a)
	res, err := e.Execute(DML{Event: EventInsert, Table: "A", RowID: "r1", Row: Row{}})
	if !errors.Is(err, ErrPolicyDenied) {
		t.Fatalf("got err %v, want ErrPolicyDenied", err)
	}
	if !res.RolledBack || len(a.Rows) != 0 {
		t.Fatalf("denied write leaked: %#v", a.Rows)
	}
}

func TestBEFOREAcceptanceWritesModifiedRow(t *testing.T) {
	docs := NewTable("docs")
	docs.Policy = PolicyFunc(func(c PolicyContext) bool { return c.Row["status"] == "public" })
	docs.AddRow("r1", Row{"status": "public"})
	_ = docs.AddBefore(&Trigger{
		ID:    "annotate",
		Event: EventUpdate,
		Modify: func(ctx *TriggerContext, row Row) (Row, error) {
			row["reviewed"] = true
			return row, nil
		},
	})
	e := NewEngine("clerk")
	mustAdd(t, e, docs)
	if _, err := e.Execute(DML{Event: EventUpdate, Table: "docs", RowID: "r1", Row: Row{"title": "x"}}); err != nil {
		t.Fatalf("execute: %v", err)
	}
	row, _ := docs.Row("r1")
	if row["reviewed"] != true || row["title"] != "x" {
		t.Fatalf("modified row not written: %#v", row)
	}
}

func TestBEFOREModificationChangesWhichAfterTriggerFires(t *testing.T) {
	docs, audit := NewTable("docs"), NewTable("audit")
	docs.Policy = AllowAll
	audit.Policy = AllowAll
	docs.AddRow("r1", Row{"status": "draft"})
	_ = docs.AddBefore(&Trigger{
		ID:    "publish",
		Event: EventUpdate,
		Modify: func(ctx *TriggerContext, row Row) (Row, error) {
			row["status"] = "public"
			return row, nil
		},
	})
	_ = docs.AddAfter(&Trigger{
		ID:    "public-only-audit",
		Event: EventUpdate,
		When: func(ctx *TriggerContext, row Row) bool {
			return row["status"] == "public"
		},
		Body: func(ctx *TriggerContext) error {
			return ctx.Insert("audit", "a1", Row{"row": ctx.Frame.RowID})
		},
	})
	e := NewEngine("clerk")
	mustAdd(t, e, docs, audit)
	res, err := e.Execute(DML{Event: EventUpdate, Table: "docs", RowID: "r1", Row: Row{"title": "x"}})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if _, ok := audit.Rows["a1"]; !ok {
		t.Fatal("AFTER trigger did not see the BEFORE-modified row")
	}
	if len(res.Frames) != 2 || res.Frames[0].TriggerID != "publish" || res.Frames[1].TriggerID != "public-only-audit" {
		t.Fatalf("frames = %#v", res.Frames)
	}
}

func TestTwoTableCycleRejectsReentrantDML(t *testing.T) {
	a := NewTable("A")
	b := NewTable("B")
	a.Policy = AllowAll
	b.Policy = AllowAll
	_ = a.AddAfter(&Trigger{
		ID:    "a2b",
		Event: EventInsert,
		Body: func(ctx *TriggerContext) error {
			if ctx.Frame.RowID == "r1" {
				return ctx.Insert("B", "r1", Row{"from": "A"})
			}
			return nil
		},
	})
	_ = b.AddAfter(&Trigger{
		ID:    "b2a",
		Event: EventInsert,
		Body: func(ctx *TriggerContext) error {
			if ctx.Frame.RowID == "r1" {
				return ctx.Insert("A", "r1", Row{"from": "B"})
			}
			return nil
		},
	})
	e := NewEngine("clerk")
	mustAdd(t, e, a, b)
	res, err := e.Execute(DML{Event: EventInsert, Table: "A", RowID: "r1", Row: Row{"n": 1}})
	if !errors.Is(err, ErrReentrant) {
		t.Fatalf("got err %v, want ErrReentrant", err)
	}
	if !res.RolledBack {
		t.Fatal("cycle should roll back")
	}
	if _, ok := a.Rows["r1"]; ok {
		t.Error("A.r1 survived rollback")
	}
	if _, ok := b.Rows["r1"]; ok {
		t.Error("B.r1 survived rollback")
	}
	if len(res.Frames) != 2 || res.Frames[0].TriggerID != "a2b" || res.Frames[1].TriggerID != "b2a" {
		t.Fatalf("unexpected frames: %#v", res.Frames)
	}
	if res.Frames[1].Depth != 2 {
		t.Fatalf("nested frame depth = %d, want 2", res.Frames[1].Depth)
	}
	if res.Frames[1].OriginSubject != "clerk" {
		t.Fatalf("origin changed: %#v", res.Frames[1])
	}
}

func TestSameTableDifferentRowsAllowed(t *testing.T) {
	a := NewTable("A")
	a.Policy = AllowAll
	_ = a.AddAfter(&Trigger{
		ID:    "r1-to-r2",
		Event: EventInsert,
		When: func(ctx *TriggerContext, row Row) bool {
			return ctx.Frame.RowID == "r1"
		},
		Body: func(ctx *TriggerContext) error {
			return ctx.Insert("A", "r2", Row{"copied": true})
		},
	})
	e := NewEngine("clerk")
	mustAdd(t, e, a)
	res, err := e.Execute(DML{Event: EventInsert, Table: "A", RowID: "r1", Row: Row{"n": 1}})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if _, ok := a.Rows["r2"]; !ok {
		t.Fatal("second row was not written")
	}
	if len(res.Frames) != 1 {
		t.Fatalf("frames = %#v", res.Frames)
	}
}

func TestPerTriggerPerRowOnceAcrossEvents(t *testing.T) {
	a := NewTable("A")
	a.Policy = AllowAll
	a.AddRow("r1", Row{"n": 1})
	_ = a.AddBefore(&Trigger{
		ID:    "switch-event",
		Event: EventAny,
		Body: func(ctx *TriggerContext) error {
			if ctx.Frame.Event == EventUpdate {
				// Different event on the original row: not a same-kind
				// re-entrant DML, but the same trigger ID already ran for r1.
				return ctx.Delete("A", "r1")
			}
			return nil
		},
	})
	e := NewEngine("clerk")
	mustAdd(t, e, a)
	res, err := e.Execute(DML{Event: EventUpdate, Table: "A", RowID: "r1", Row: Row{"n": 2}})
	if !errors.Is(err, ErrTriggerRowOnce) {
		t.Fatalf("got err %v, want ErrTriggerRowOnce", err)
	}
	if !res.RolledBack {
		t.Fatal("statement should roll back")
	}
	if _, ok := a.Rows["r1"]; !ok {
		t.Fatal("row was deleted despite rollback")
	}
}

func TestGlobalDepthLimitRollsBack(t *testing.T) {
	a, b, c := NewTable("A"), NewTable("B"), NewTable("C")
	a.Policy, b.Policy, c.Policy = AllowAll, AllowAll, AllowAll
	_ = a.AddAfter(&Trigger{ID: "a-b", Event: EventInsert, Body: func(ctx *TriggerContext) error {
		return ctx.Insert("B", "b1", Row{})
	}})
	_ = b.AddAfter(&Trigger{ID: "b-c", Event: EventInsert, Body: func(ctx *TriggerContext) error {
		return ctx.Insert("C", "c1", Row{})
	}})
	_ = c.AddAfter(&Trigger{ID: "c-depth", Event: EventInsert})

	e := NewEngine("clerk")
	e.Options.MaxDepth = 2
	mustAdd(t, e, a, b, c)
	res, err := e.Execute(DML{Event: EventInsert, Table: "A", RowID: "a1", Row: Row{}})
	if !errors.Is(err, ErrMaxDepth) {
		t.Fatalf("got err %v, want ErrMaxDepth", err)
	}
	if !res.RolledBack {
		t.Fatal("depth failure should roll back")
	}
	for _, table := range []*Table{a, b, c} {
		if len(table.Rows) != 0 {
			t.Fatalf("%s was not rolled back: %#v", table.Name, table.Rows)
		}
	}
}

func TestTriggerSpecificMaxDepth(t *testing.T) {
	a, b := NewTable("A"), NewTable("B")
	a.Policy, b.Policy = AllowAll, AllowAll
	_ = a.AddAfter(&Trigger{ID: "a-b", Event: EventInsert, Body: func(ctx *TriggerContext) error {
		return ctx.Insert("B", "b1", Row{})
	}})
	_ = b.AddAfter(&Trigger{ID: "b-limited", Event: EventInsert, MaxDepth: 1})
	e := NewEngine("clerk")
	mustAdd(t, e, a, b)
	res, err := e.Execute(DML{Event: EventInsert, Table: "A", RowID: "a1", Row: Row{}})
	if !errors.Is(err, ErrTriggerMaxDepth) {
		t.Fatalf("got err %v, want ErrTriggerMaxDepth", err)
	}
	if !res.RolledBack || len(a.Rows) != 0 || len(b.Rows) != 0 {
		t.Fatalf("not rolled back: a=%#v b=%#v", a.Rows, b.Rows)
	}
}

func TestDefinerWritableScopeAndOrigin(t *testing.T) {
	notes := NewTable("notes")
	audit := NewTable("audit")
	notes.Policy = SubjectPolicy{"clerk": Events(EventInsert), "admin": AllEvents()}
	audit.Policy = SubjectPolicy{"admin": AllEvents()}

	_ = notes.AddAfter(&Trigger{
		ID:             "audit-write",
		Event:          EventInsert,
		Security:       SecurityDefiner,
		Definer:        "admin",
		WritableTables: []string{"audit"},
		Body: func(ctx *TriggerContext) error {
			return ctx.Insert("audit", "a1", Row{"note": ctx.Frame.RowID})
		},
	})
	e := NewEngine("clerk")
	mustAdd(t, e, notes, audit)
	res, err := e.Execute(DML{Event: EventInsert, Table: "notes", RowID: "n1", Row: Row{"body": "x"}})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if _, ok := audit.Rows["a1"]; !ok {
		t.Fatal("definer audit write missing")
	}
	if len(res.Frames) != 1 {
		t.Fatalf("frames = %#v", res.Frames)
	}
	frame := res.Frames[0]
	if frame.EffectiveSubject != "admin" || frame.OriginSubject != "clerk" || frame.InvokerSubject != "clerk" {
		t.Fatalf("bad definer frame: %#v", frame)
	}
}

func TestDefinerCannotWriteOutsideWhitelist(t *testing.T) {
	notes, accounts := NewTable("notes"), NewTable("accounts")
	notes.Policy = SubjectPolicy{"clerk": Events(EventInsert), "admin": AllEvents()}
	accounts.Policy = SubjectPolicy{"admin": AllEvents()}
	_ = notes.AddAfter(&Trigger{
		ID:             "escape",
		Event:          EventInsert,
		Security:       SecurityDefiner,
		Definer:        "admin",
		WritableTables: []string{"audit"},
		Body: func(ctx *TriggerContext) error {
			return ctx.Insert("accounts", "x1", Row{"owner": "attacker"})
		},
	})
	e := NewEngine("clerk")
	mustAdd(t, e, notes, accounts)
	res, err := e.Execute(DML{Event: EventInsert, Table: "notes", RowID: "n1", Row: Row{}})
	if !errors.Is(err, ErrDefinerTable) {
		t.Fatalf("got err %v, want ErrDefinerTable", err)
	}
	if !res.RolledBack {
		t.Fatal("definer escape should roll back")
	}
	if _, ok := accounts.Rows["x1"]; ok {
		t.Fatal("accounts write survived")
	}
}

func TestDefinerInvokerFallback(t *testing.T) {
	notes, scratch := NewTable("notes"), NewTable("scratch")
	notes.Policy = SubjectPolicy{"clerk": Events(EventInsert), "admin": AllEvents()}
	scratch.Policy = SubjectPolicy{"clerk": Events(EventInsert), "admin": AllEvents()}
	_ = notes.AddAfter(&Trigger{
		ID:             "fallback",
		Event:          EventInsert,
		Security:       SecurityDefiner,
		Definer:        "admin",
		WritableTables: []string{"audit"},
		Body: func(ctx *TriggerContext) error {
			return ctx.Insert("scratch", "s1", Row{"via": "fallback"})
		},
	})
	opts := StrictOptions()
	opts.DefinerScope = DefinerScopeInvokerFallback
	e := NewEngineWithOptions("clerk", opts)
	mustAdd(t, e, notes, scratch)
	res, err := e.Execute(DML{Event: EventInsert, Table: "notes", RowID: "n1", Row: Row{}})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if _, ok := scratch.Rows["s1"]; !ok {
		t.Fatal("fallback write missing")
	}
	if len(res.Writes) != 2 || res.Writes[1].Subject != "clerk" {
		t.Fatalf("fallback write subject = %#v", res.Writes)
	}
}

func TestNestedDefinerInvokerKeepsOrigin(t *testing.T) {
	notes, audit, accounts := NewTable("notes"), NewTable("audit"), NewTable("accounts")
	notes.Policy = SubjectPolicy{"clerk": Events(EventInsert), "admin": AllEvents()}
	audit.Policy = SubjectPolicy{"admin": AllEvents()}
	accounts.Policy = SubjectPolicy{"admin": AllEvents()}

	_ = notes.AddAfter(&Trigger{
		ID:             "d1",
		Event:          EventInsert,
		Security:       SecurityDefiner,
		Definer:        "admin",
		WritableTables: []string{"audit"},
		Body: func(ctx *TriggerContext) error {
			return ctx.Insert("audit", "a1", Row{"from": "d1"})
		},
	})
	_ = audit.AddAfter(&Trigger{
		ID:       "i1",
		Event:    EventInsert,
		Security: SecurityInvoker,
		Body: func(ctx *TriggerContext) error {
			return ctx.Insert("accounts", "x1", Row{"from": "i1"})
		},
	})
	e := NewEngine("clerk")
	mustAdd(t, e, notes, audit, accounts)
	res, err := e.Execute(DML{Event: EventInsert, Table: "notes", RowID: "n1", Row: Row{}})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(res.Frames) != 2 {
		t.Fatalf("frames = %#v", res.Frames)
	}
	d1, i1 := res.Frames[0], res.Frames[1]
	if d1.TriggerID != "d1" || d1.EffectiveSubject != "admin" || d1.OriginSubject != "clerk" {
		t.Fatalf("bad d1 frame: %#v", d1)
	}
	if i1.TriggerID != "i1" || i1.EffectiveSubject != "admin" || i1.InvokerSubject != "admin" || i1.OriginSubject != "clerk" {
		t.Fatalf("bad i1 frame: %#v", i1)
	}
	if _, ok := accounts.Rows["x1"]; !ok {
		t.Fatal("nested invoker write missing")
	}
}

func TestAfterFailureRollsBackAuditAndBase(t *testing.T) {
	orders, audit := NewTable("orders"), NewTable("audit")
	orders.Policy = SubjectPolicy{"clerk": Events(EventInsert)}
	audit.Policy = SubjectPolicy{"clerk": Events(EventInsert)}
	_ = orders.AddAfter(&Trigger{
		ID:    "audit-then-fail",
		Event: EventInsert,
		Body: func(ctx *TriggerContext) error {
			if err := ctx.Insert("audit", "a1", Row{"order": ctx.Frame.RowID}); err != nil {
				return err
			}
			return errors.New("audit sink failed")
		},
	})
	e := NewEngine("clerk")
	mustAdd(t, e, orders, audit)
	res, err := e.Execute(DML{Event: EventInsert, Table: "orders", RowID: "o1", Row: Row{"amount": 10}})
	if err == nil || !strings.Contains(err.Error(), "audit sink failed") {
		t.Fatalf("got err %v", err)
	}
	if !res.RolledBack {
		t.Fatal("AFTER failure should roll back")
	}
	if _, ok := orders.Rows["o1"]; ok {
		t.Fatal("base row survived AFTER rollback")
	}
	if _, ok := audit.Rows["a1"]; ok {
		t.Fatal("audit row survived AFTER rollback")
	}
	if len(res.Writes) != 2 {
		t.Fatalf("expected two attempted writes in trace, got %#v", res.Writes)
	}
}

func TestBEFOREVetoRollsBackNestedWrite(t *testing.T) {
	orders, audit := NewTable("orders"), NewTable("audit")
	orders.Policy = AllowAll
	audit.Policy = AllowAll
	_ = orders.AddBefore(&Trigger{
		ID:    "veto",
		Event: EventInsert,
		Body: func(ctx *TriggerContext) error {
			if err := ctx.Insert("audit", "a1", Row{"attempt": true}); err != nil {
				return err
			}
			return errors.New("rejected by policy trigger")
		},
	})
	e := NewEngine("clerk")
	mustAdd(t, e, orders, audit)
	res, err := e.Execute(DML{Event: EventInsert, Table: "orders", RowID: "o1", Row: Row{}})
	if err == nil {
		t.Fatal("expected rejection")
	}
	if !res.RolledBack {
		t.Fatal("should roll back")
	}
	if len(orders.Rows) != 0 || len(audit.Rows) != 0 {
		t.Fatalf("rows survived: orders=%#v audit=%#v", orders.Rows, audit.Rows)
	}
}

func TestDuplicateTriggerIDRejected(t *testing.T) {
	a, b := NewTable("A"), NewTable("B")
	_ = a.AddBefore(&Trigger{ID: "dup", Event: EventInsert})
	_ = b.AddBefore(&Trigger{ID: "dup", Event: EventInsert})
	e := NewEngine("clerk")
	if err := e.AddTable(a); err != nil {
		t.Fatal(err)
	}
	if err := e.AddTable(b); err != nil {
		t.Fatal(err)
	}
	if err := e.Prepare(); !errors.Is(err, ErrDuplicateTrigger) {
		t.Fatalf("got %v, want ErrDuplicateTrigger", err)
	}
}

func TestTriggerCannotChangeSessionOrCommit(t *testing.T) {
	a := NewTable("A")
	a.Policy = AllowAll
	var sawSessionErr, sawCommitErr bool
	_ = a.AddAfter(&Trigger{
		ID:    "no-control",
		Event: EventInsert,
		Body: func(ctx *TriggerContext) error {
			if err := ctx.SetSessionSubject("admin"); errors.Is(err, ErrTriggerCannotChangeSession) {
				sawSessionErr = true
			}
			if err := ctx.Commit(); errors.Is(err, ErrTriggerCannotCommit) {
				sawCommitErr = true
			}
			if err := ctx.Rollback(); !errors.Is(err, ErrTriggerCannotCommit) {
				t.Errorf("rollback error = %v", err)
			}
			return nil
		},
	})
	e := NewEngine("clerk")
	mustAdd(t, e, a)
	if _, err := e.Execute(DML{Event: EventInsert, Table: "A", RowID: "r1", Row: Row{}}); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !sawSessionErr || !sawCommitErr {
		t.Fatalf("forbidden operations not rejected: session=%v commit=%v", sawSessionErr, sawCommitErr)
	}
	if e.Session() != "clerk" {
		t.Fatalf("session changed to %s", e.Session())
	}
}
