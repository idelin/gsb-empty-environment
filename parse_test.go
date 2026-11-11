package pgoutput

import (
	"testing"
	"time"
)

func feed(t *testing.T, a *Assembler, raw []byte) *Result {
	t.Helper()
	msg, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse(%q) error: %v", raw[0], err)
	}
	res, err := a.Handle(msg)
	if err != nil {
		t.Fatalf("Handle(%q) error: %v", raw[0], err)
	}
	return res
}

func feedExpectErr(t *testing.T, a *Assembler, raw []byte) {
	t.Helper()
	msg, err := Parse(raw)
	if err != nil {
		return // parse-level error is acceptable
	}
	if _, err := a.Handle(msg); err == nil {
		t.Fatalf("expected error handling message %q, got none", raw[0])
	}
}

func TestParseRelationAndType(t *testing.T) {
	relWire := wireRelation(42, "public", "widgets", 'd',
		RelationColumn{Key: true, Name: "id", DataType: 23, TypeModifier: -1},
		RelationColumn{Name: "body", DataType: 25, TypeModifier: -1},
	)
	msg, err := Parse(relWire)
	if err != nil {
		t.Fatalf("Parse relation: %v", err)
	}
	rel := msg.(Relation)
	if rel.ID != 42 || rel.Namespace != "public" || rel.Name != "widgets" || rel.ReplicaIdentity != 'd' {
		t.Fatalf("unexpected relation header: %+v", rel)
	}
	if len(rel.Columns) != 2 || !rel.Columns[0].Key || rel.Columns[1].Key {
		t.Fatalf("unexpected columns: %+v", rel.Columns)
	}

	typeWire := wireType(25, "pg_catalog", "text")
	typ := ParseOrDie(t, typeWire).(Type)
	if typ.ID != 25 || typ.Namespace != "pg_catalog" || typ.Name != "text" {
		t.Fatalf("unexpected type: %+v", typ)
	}

	// CopyData wrapper must decode to the same inner message.
	wrapped, err := Parse(copyData(relWire))
	if err != nil {
		t.Fatalf("Parse CopyData: %v", err)
	}
	if wrapped.(Relation).ID != 42 {
		t.Fatalf("wrapped relation mismatch: %+v", wrapped)
	}
}

func ParseOrDie(t *testing.T, raw []byte) any {
	t.Helper()
	msg, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	return msg
}

func TestParseDMLRoundTrip(t *testing.T) {
	row := []TupleColumn{textCol("7"), textCol("hello")}

	ins := ParseOrDie(t, wireInsert(1, row)).(Insert)
	if ins.RelationID != 1 || ins.Row[1].String() != "hello" || ins.Row[0].Value == nil {
		t.Fatalf("bad insert: %+v", ins)
	}

	upd := ParseOrDie(t, wireUpdate(1, 'O',
		[]TupleColumn{textCol("7"), unchangedCol()},
		[]TupleColumn{textCol("7"), textCol("world")},
	)).(Update)
	if upd.OldKey || !upd.Old[1].Unchanged || upd.New[1].String() != "world" {
		t.Fatalf("bad update: %+v", upd)
	}

	del := ParseOrDie(t, wireDelete(1, 'K',
		[]TupleColumn{textCol("7")},
	)).(Delete)
	if !del.OldKey || del.Old[0].String() != "7" {
		t.Fatalf("bad delete: %+v", del)
	}

	tr := ParseOrDie(t, wireTruncate(1, 1, 2)).(Truncate)
	if len(tr.RelationIDs) != 2 || tr.Options != 1 {
		t.Fatalf("bad truncate: %+v", tr)
	}
}

func TestParseStreamAndKeepalive(t *testing.T) {
	now := time.Now().UTC()

	v2start := ParseOrDie(t, wireStreamStart(99, true, 0)).(StreamStart)
	if !v2start.FirstSegment || v2start.XID != 99 || v2start.SubXID != 0 {
		t.Fatalf("bad v2 stream start: %+v", v2start)
	}

	v4start := ParseOrDie(t, wireStreamStart(99, true, 100)).(StreamStart)
	if v4start.SubXID != 100 {
		t.Fatalf("bad v4 stream start: %+v", v4start)
	}

	if _, ok := ParseOrDie(t, wireStreamStop()).(StreamStop); !ok {
		t.Fatal("stream stop decode failed")
	}

	sc := ParseOrDie(t, wireStreamCommit(99, 0x100, 0x108, now)).(StreamCommit)
	if sc.XID != 99 || sc.CommitLSN != 0x100 || sc.EndLSN != 0x108 {
		t.Fatalf("bad stream commit: %+v", sc)
	}

	sa := ParseOrDie(t, wireStreamAbort(99, 100, 0x90, 0x98, now)).(StreamAbort)
	if !sa.SubAbort || sa.XID != 99 || sa.SubXID != 100 {
		t.Fatalf("bad stream abort: %+v", sa)
	}

	topAbort := ParseOrDie(t, wireStreamAbort(99, 99, 0x90, 0x98, now)).(StreamAbort)
	if topAbort.SubAbort {
		t.Fatalf("top-level abort misclassified: %+v", topAbort)
	}

	k := ParseOrDie(t, wireKeepalive(0x200, now, true)).(Keepalive)
	if k.LSN != 0x200 || !k.ReplyRequested {
		t.Fatalf("bad keepalive: %+v", k)
	}
}

func TestParseErrors(t *testing.T) {
	if _, err := Parse(nil); err == nil {
		t.Fatal("expected error on empty input")
	}
	if _, err := Parse([]byte{'Z'}); err == nil {
		t.Fatal("expected error on unknown message type")
	}
	if _, err := Parse([]byte{'B', 0, 1}); err == nil {
		t.Fatal("expected error on truncated begin")
	}
	if _, err := Parse(append(wireStreamStop(), 0xFF)); err == nil {
		t.Fatal("expected error on trailing bytes")
	}
	if _, err := Parse(copyData([]byte{'B'})); err == nil {
		t.Fatal("expected error on bad CopyData length")
	}
	if _, err := Parse(wireInsert(1, []TupleColumn{
		{Flag: 'x'},
	})); err == nil {
		t.Fatal("expected error on bad tuple tag")
	}
}

func TestLSNString(t *testing.T) {
	if got := LSN(0x0123456789abcdef).String(); got != "1234567/89ABCDEF" {
		t.Fatalf("LSN String = %q", got)
	}
	if got := LSN(0xff).String(); got != "0/000000FF" {
		t.Fatalf("LSN String = %q", got)
	}
}
