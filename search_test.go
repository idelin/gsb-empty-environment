package netsec

import (
	"errors"
	"testing"
)

func leakModel(opts Options) *Engine {
	notes := NewTable("notes")
	secret := NewTable("secret")
	audit := NewTable("audit")
	notes.Policy = SubjectPolicy{
		"clerk": Events(EventInsert),
		"admin": AllEvents(),
	}
	secret.Policy = SubjectPolicy{"admin": AllEvents()}
	audit.Policy = SubjectPolicy{"admin": AllEvents()}

	_ = notes.AddAfter(&Trigger{
		ID:             "leak",
		Event:          EventInsert,
		Security:       SecurityDefiner,
		Definer:        "admin",
		WritableTables: []string{"audit"},
		Actions: []Action{{
			Event: EventInsert,
			Table: "secret",
			RowID: "s1",
			Row:   Row{"leaked": true},
		}},
	})

	e := NewEngineWithOptions("clerk", opts)
	_ = e.AddTable(notes)
	_ = e.AddTable(secret)
	_ = e.AddTable(audit)
	return e
}

func TestDifferentialBFSFindsDefinerEscape(t *testing.T) {
	correct := leakModel(StrictOptions())
	pending := leakModel(PendingOptions())
	start := DML{Event: EventInsert, Table: "notes", RowID: "n1", Row: Row{"body": "hello"}}

	finding, err := DifferentialBFS(correct, pending, "clerk", []DML{start}, 8)
	if err != nil {
		t.Fatalf("DifferentialBFS: %v", err)
	}
	if !finding.Found {
		t.Fatalf("no finding: %s", finding)
	}
	if finding.Target.Table != "secret" || finding.Target.RowID != "s1" {
		t.Fatalf("bad target: %#v", finding.Target)
	}
	if !finding.CorrectBlocked || !errors.Is(finding.CorrectErr, ErrDefinerTable) {
		t.Fatalf("correct replay not blocked: %#v", finding.CorrectErr)
	}
	if len(finding.Frames) != 1 || finding.Frames[0].TriggerID != "leak" {
		t.Fatalf("frames = %#v", finding.Frames)
	}
	if finding.Frames[0].EffectiveSubject != "admin" {
		t.Fatalf("definer frame did not use admin: %#v", finding.Frames[0])
	}
	if len(finding.CorrectFrames) != 1 {
		t.Fatalf("correct frames = %#v", finding.CorrectFrames)
	}
	if finding.CorrectFrames[0].OriginSubject != "clerk" {
		t.Fatalf("correct model did not preserve origin: %#v", finding.CorrectFrames[0])
	}
	if !containsString(finding.String(), "effective=admin") {
		t.Fatalf("finding string missing frame context:\n%s", finding)
	}

	if _, err := correct.ExecuteAs("clerk", start); !errors.Is(err, ErrDefinerTable) {
		t.Fatalf("correct model err = %v, want ErrDefinerTable", err)
	}
	if _, err := pending.ExecuteAs("clerk", start); err != nil {
		t.Fatalf("pending model err = %v, want success", err)
	}
	if _, ok := pending.Tables["secret"].Rows["s1"]; !ok {
		t.Fatal("pending model did not write secret")
	}
}

func TestDifferentialBFSIgnoresChainCorrectModelAlsoAllows(t *testing.T) {
	newModel := func(opts Options) *Engine {
		notes, secret := NewTable("notes"), NewTable("secret")
		notes.Policy = SubjectPolicy{"clerk": Events(EventInsert), "admin": AllEvents()}
		secret.Policy = SubjectPolicy{"admin": AllEvents()}
		_ = notes.AddAfter(&Trigger{
			ID:             "declared-write",
			Event:          EventInsert,
			Security:       SecurityDefiner,
			Definer:        "admin",
			WritableTables: []string{"secret"},
			Actions: []Action{{
				Event: EventInsert,
				Table: "secret",
				RowID: "s1",
				Row:   Row{"allowed": true},
			}},
		})
		e := NewEngineWithOptions("clerk", opts)
		_ = e.AddTable(notes)
		_ = e.AddTable(secret)
		return e
	}
	correct := newModel(StrictOptions())
	pending := newModel(PendingOptions())
	start := DML{Event: EventInsert, Table: "notes", RowID: "n1", Row: Row{}}

	finding, err := DifferentialBFS(correct, pending, "clerk", []DML{start}, 8)
	if err != nil {
		t.Fatalf("DifferentialBFS: %v", err)
	}
	if finding.Found {
		t.Fatalf("expected no differential finding, got %s", finding)
	}
}

func TestBFSReturnsShortestTriggerChain(t *testing.T) {
	notes, middle, secret := NewTable("notes"), NewTable("middle"), NewTable("secret")
	notes.Policy = SubjectPolicy{"clerk": Events(EventInsert), "admin": AllEvents()}
	middle.Policy = SubjectPolicy{"admin": AllEvents()}
	secret.Policy = SubjectPolicy{"admin": AllEvents()}

	// Fast path: one frame and one unauthorized write.
	_ = notes.AddAfter(&Trigger{
		ID:       "fast",
		Event:    EventInsert,
		Security: SecurityDefiner,
		Definer:  "admin",
		Actions: []Action{{
			Event: EventInsert,
			Table: "secret",
			RowID: "s-fast",
			Row:   Row{"path": "fast"},
		}},
	})
	// Slow path: two frames.
	_ = notes.AddAfter(&Trigger{
		ID:       "slow",
		Event:    EventInsert,
		Security: SecurityDefiner,
		Definer:  "admin",
		Actions: []Action{{
			Event: EventInsert,
			Table: "middle",
			RowID: "m1",
			Row:   Row{"path": "slow"},
		}},
	})
	_ = middle.AddAfter(&Trigger{
		ID:       "slow2",
		Event:    EventInsert,
		Security: SecurityDefiner,
		Definer:  "admin",
		Actions: []Action{{
			Event: EventInsert,
			Table: "secret",
			RowID: "s-slow",
			Row:   Row{"path": "slow"},
		}},
	})

	e := NewEngineWithOptions("clerk", PendingOptions())
	_ = e.AddTable(notes)
	_ = e.AddTable(middle)
	_ = e.AddTable(secret)
	finding, err := BFS(e, "clerk", []DML{{
		Event: EventInsert,
		Table: "notes",
		RowID: "n1",
		Row:   Row{},
	}}, 8)
	if err != nil {
		t.Fatalf("BFS: %v", err)
	}
	if !finding.Found {
		t.Fatalf("no finding: %s", finding)
	}
	if finding.Target.Table != "secret" || finding.Target.RowID != "s-fast" {
		t.Fatalf("did not choose shortest chain: %#v", finding.Target)
	}
	if len(finding.Frames) != 1 || finding.Frames[0].TriggerID != "fast" {
		t.Fatalf("frames = %#v", finding.Frames)
	}
}

func TestBFSUsesSameTableDifferentRows(t *testing.T) {
	notes, secret := NewTable("notes"), NewTable("secret")
	notes.Policy = SubjectPolicy{"clerk": Events(EventInsert), "admin": AllEvents()}
	secret.Policy = SubjectPolicy{"admin": Events(EventInsert)}
	notes.AddRow("template", Row{"kind": "template"})
	_ = notes.AddAfter(&Trigger{
		ID:       "copy-row",
		Event:    EventInsert,
		Security: SecurityDefiner,
		Definer:  "admin",
		When:     func(ctx *TriggerContext, row Row) bool { return ctx.Frame.RowID == "n1" },
		Actions: []Action{{
			Event: EventInsert,
			Table: "notes",
			RowID: "n2",
			Row:   Row{"kind": "copy"},
		}},
	})
	_ = notes.AddAfter(&Trigger{
		ID:       "escalate",
		Event:    EventInsert,
		Security: SecurityDefiner,
		Definer:  "admin",
		When:     func(ctx *TriggerContext, row Row) bool { return ctx.Frame.RowID == "n2" },
		Actions: []Action{{
			Event: EventInsert,
			Table: "secret",
			RowID: "s1",
			Row:   Row{"from": "n2"},
		}},
	})
	e := NewEngineWithOptions("clerk", PendingOptions())
	_ = e.AddTable(notes)
	_ = e.AddTable(secret)
	finding, err := BFS(e, "clerk", []DML{{
		Event: EventInsert,
		Table: "notes",
		RowID: "n1",
		Row:   Row{"kind": "root"},
	}}, 8)
	if err != nil {
		t.Fatalf("BFS: %v", err)
	}
	if !finding.Found {
		t.Fatalf("no finding: %s", finding)
	}
	if len(finding.Frames) != 2 {
		t.Fatalf("expected two-frame chain through a new row, got %#v", finding.Frames)
	}
	if finding.Frames[0].TriggerID != "copy-row" || finding.Frames[1].TriggerID != "escalate" {
		t.Fatalf("frames = %#v", finding.Frames)
	}
}

func TestBFSRecordsNestedDefinerInvokerFrames(t *testing.T) {
	notes, audit, secret := NewTable("notes"), NewTable("audit"), NewTable("secret")
	notes.Policy = SubjectPolicy{"clerk": Events(EventInsert), "admin": AllEvents()}
	audit.Policy = SubjectPolicy{"clerk": Events(EventInsert), "admin": AllEvents()}
	secret.Policy = SubjectPolicy{"admin": AllEvents()}

	_ = notes.AddAfter(&Trigger{
		ID:             "d1",
		Event:          EventInsert,
		Security:       SecurityDefiner,
		Definer:        "admin",
		WritableTables: []string{"audit"},
		Actions: []Action{{
			Event: EventInsert,
			Table: "audit",
			RowID: "a1",
			Row:   Row{"from": "d1"},
		}},
	})
	_ = audit.AddAfter(&Trigger{
		ID:       "i1",
		Event:    EventInsert,
		Security: SecurityInvoker,
		Actions: []Action{{
			Event: EventInsert,
			Table: "secret",
			RowID: "s1",
			Row:   Row{"from": "i1"},
		}},
	})

	e := NewEngineWithOptions("clerk", StrictOptions())
	_ = e.AddTable(notes)
	_ = e.AddTable(audit)
	_ = e.AddTable(secret)
	finding, err := BFS(e, "clerk", []DML{{
		Event: EventInsert,
		Table: "notes",
		RowID: "n1",
		Row:   Row{},
	}}, 8)
	if err != nil {
		t.Fatalf("BFS: %v", err)
	}
	if !finding.Found || finding.Target.Table != "secret" {
		t.Fatalf("unexpected finding: %s", finding)
	}
	if len(finding.Frames) != 2 {
		t.Fatalf("frames = %#v", finding.Frames)
	}
	d1, i1 := finding.Frames[0], finding.Frames[1]
	if d1.TriggerID != "d1" || d1.EffectiveSubject != "admin" || d1.OriginSubject != "clerk" {
		t.Fatalf("bad d1 frame: %#v", d1)
	}
	if i1.TriggerID != "i1" || i1.EffectiveSubject != "admin" || i1.InvokerSubject != "admin" || i1.OriginSubject != "clerk" {
		t.Fatalf("bad i1 frame: %#v", i1)
	}
}

func containsString(haystack, needle string) bool {
	return len(needle) == 0 || (len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0)
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
