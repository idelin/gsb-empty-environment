package bridge

import (
	"errors"
	"strings"
	"testing"
)

func mustPlanSet(t *testing.T) (*Schema, *Schema, *PlanSet) {
	t.Helper()
	s1, s2, err := DemoSchemas()
	if err != nil {
		t.Fatalf("schemas: %v", err)
	}
	ps, err := NewPlanSet(s1, s2)
	if err != nil {
		t.Fatalf("planset: %v", err)
	}
	return s1, s2, ps
}

func sampleV1Frame() *Frame {
	return &Frame{Version: 1, Fields: []Field{
		{ID: 1, Kind: KindVarint, Num: 42},
		{ID: 2, Kind: KindBytes, Raw: []byte("alice")},
		{ID: 3, Kind: KindVarint, Num: 900},
	}}
}

func convertOne(t *testing.T, b *Binding, f *Frame) (*Frame, *Report) {
	t.Helper()
	var out *Frame
	var rep *Report
	if err := b.Convert(f, DefaultLimits(), func(fr *Frame, r *Report) {
		out, rep = fr, r
	}); err != nil {
		t.Fatalf("convert: %v", err)
	}
	if out == nil {
		t.Fatal("sink not called")
	}
	return out, rep
}

func TestPlanClassification(t *testing.T) {
	_, _, ps := mustPlanSet(t)
	l, e, u := ps.Fwd.Classification()
	if got := ids(l); got != "[1 2]" {
		t.Fatalf("fwd lossless = %s, want [1 2]", got)
	}
	if got := ids(e); got != "[3]" {
		t.Fatalf("fwd preserve-extension = %s, want [3]", got)
	}
	if len(u) != 0 {
		t.Fatalf("fwd unrepresentable = %v, want empty", u)
	}
	_, e2, _ := ps.Rev.Classification()
	if got := ids(e2); got != "[3 5 6]" {
		t.Fatalf("rev preserve-extension = %s, want [3 5 6]", got)
	}
}

func ids(xs []uint32) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, x := range xs {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(itoa(int(x)))
	}
	b.WriteByte(']')
	return b.String()
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [8]byte
	p := len(buf)
	for i > 0 {
		p--
		buf[p] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[p:])
}

func TestRenameKeepsIDLossless(t *testing.T) {
	_, _, ps := mustPlanSet(t)
	out, rep, err := ps.Fwd.Convert(sampleV1Frame(), 1, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	name, ok := out.field(2)
	if !ok || string(name.Raw) != "alice" {
		t.Fatalf("renamed field 2 not preserved: %v", out)
	}
	for _, id := range rep.Copied {
		if id == 2 {
			return
		}
	}
	t.Fatalf("field 2 not classified lossless: %s", rep)
}

func TestStrictV2RejectsNegotiation(t *testing.T) {
	s1, _, _ := mustPlanSet(t)
	s2strict, err := NewSchema(2, []FieldDef{
		{ID: 1, Name: "id", Type: TVarint, Required: true},
		{ID: 9, Name: "tenant", Type: TVarint, Required: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewPlanSet(s1, s2strict); err == nil {
		t.Fatal("expected refusal: v2 requires field v1 cannot provide")
	} else if !strings.Contains(err.Error(), "tenant") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestNegotiationDigestBinding(t *testing.T) {
	_, _, ps := mustPlanSet(t)
	b := NewBridge(ps)
	bind := b.Bind()
	if err := bind.Negotiate(ps.Digest()); err != nil {
		t.Fatalf("matching digest refused: %v", err)
	}
	if err := bind.Negotiate([32]byte{1, 2, 3}); err == nil {
		t.Fatal("foreign digest accepted")
	}
}

func TestUnknownFieldRoundTrip(t *testing.T) {
	_, _, ps := mustPlanSet(t)
	b := NewBridge(ps)
	bind := b.Bind()
	v2 := &Frame{Version: 2, Fields: []Field{
		{ID: 1, Kind: KindVarint, Num: 7},
		{ID: 6, Kind: KindVarint, Num: 3},
		{ID: 99, Kind: KindBytes, Raw: []byte{0xca, 0xfe}},
	}}
	v1, rep1 := convertOne(t, bind, v2)
	if rep1.Unknown != 1 {
		t.Fatalf("unknown field not preserved: %s", rep1)
	}
	if _, ok := v1.field(15); !ok {
		t.Fatal("extension carrier missing in v1 frame")
	}
	back, _ := convertOne(t, bind, v1)
	f99, ok := back.field(99)
	if !ok || len(f99.Raw) != 2 || f99.Raw[0] != 0xca {
		t.Fatalf("unknown field 99 lost on return trip: %v", back)
	}
	if d := Diff(v2, back); len(d) != 0 {
		t.Fatalf("round trip diff: %s", FormatDiff(d))
	}
}

func TestExtensionConflictRealFieldWins(t *testing.T) {
	_, _, ps := mustPlanSet(t)
	b := NewBridge(ps)
	carrier := Field{ID: 2, Kind: KindBytes, Raw: []byte("mallory")}
	v2 := &Frame{Version: 2, Fields: []Field{
		{ID: 1, Kind: KindVarint, Num: 7},
		{ID: 2, Kind: KindBytes, Raw: []byte("alice")},
		{ID: 15, Kind: KindBytes, Raw: carrier.appendWire(nil)},
	}}
	out, rep := convertOne(t, b.Bind(), v2)
	f2, _ := out.field(2)
	if string(f2.Raw) != "alice" {
		t.Fatalf("real field lost to extension entry: %q", f2.Raw)
	}
	if len(rep.Conflicts) != 1 || rep.Conflicts[0].ID != 2 {
		t.Fatalf("conflict not reported: %s", rep)
	}
}

func TestResourceLimits(t *testing.T) {
	lim := Limits{MaxFrameSize: 64, MaxFieldLen: 8, MaxFields: 3}

	// Oversized declared frame length: rejected at header time, before any
	// payload-sized allocation.
	p := NewParser(lim)
	hdr := []byte{1, 0, 0, 0, 200}
	if err := p.Feed(hdr); !errors.Is(err, ErrBadData) {
		t.Fatalf("oversize frame: got %v, want ErrBadData", err)
	}

	// Oversized field length inside an otherwise valid frame.
	big := EncodeFrame(1, []Field{{ID: 2, Kind: KindBytes, Raw: make([]byte, 100)}})
	p = NewParser(Limits{MaxFrameSize: 1024, MaxFieldLen: 8, MaxFields: 10})
	if err := p.Feed(big); err != nil {
		t.Fatal(err)
	}
	_, payload, err := p.Next()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parseFields(payload, Limits{MaxFieldLen: 8, MaxFields: 10}); !errors.Is(err, ErrBadData) {
		t.Fatalf("oversize field: got %v, want ErrBadData", err)
	}

	// Too many fields.
	many := EncodeFrame(1, []Field{
		{ID: 1, Kind: KindVarint, Num: 1},
		{ID: 2, Kind: KindVarint, Num: 2},
		{ID: 3, Kind: KindVarint, Num: 3},
		{ID: 4, Kind: KindVarint, Num: 4},
	})
	p = NewParser(Limits{MaxFrameSize: 1024, MaxFieldLen: 8, MaxFields: 3})
	if err := p.Feed(many); err != nil {
		t.Fatal(err)
	}
	_, payload, _ = p.Next()
	if _, err := parseFields(payload, Limits{MaxFieldLen: 8, MaxFields: 3}); !errors.Is(err, ErrBadData) {
		t.Fatalf("too many fields: got %v, want ErrBadData", err)
	}
}

func TestNeedMoreVsBadData(t *testing.T) {
	wire := EncodeFrame(1, sampleV1Frame().Fields)
	p := NewParser(DefaultLimits())
	// Every proper prefix is "need more data", never an error.
	for i := 1; i < len(wire); i++ {
		q := NewParser(DefaultLimits())
		if err := q.Feed(wire[:i]); err != nil {
			t.Fatalf("prefix %d: unexpected error %v", i, err)
		}
		if _, _, err := q.Next(); !errors.Is(err, ErrNeedMoreData) {
			t.Fatalf("prefix %d: got %v, want ErrNeedMoreData", i, err)
		}
	}
	if err := p.Feed(wire); err != nil {
		t.Fatal(err)
	}
	if _, _, err := p.Next(); err != nil {
		t.Fatalf("complete frame: %v", err)
	}
	// Trailing garbage after a complete frame is bad data.
	if err := p.Feed([]byte{0}); !errors.Is(err, ErrBadData) {
		t.Fatalf("trailing bytes: got %v, want ErrBadData", err)
	}
	// Bad version byte is bad data immediately.
	if err := NewParser(DefaultLimits()).Feed([]byte{9, 0, 0, 0, 1}); !errors.Is(err, ErrBadData) {
		t.Fatal("bad version accepted")
	}
}

func TestChunkedByteByByte(t *testing.T) {
	_, _, ps := mustPlanSet(t)
	b := NewBridge(ps)
	recv := NewReceiver(b, DefaultLimits())
	wire := EncodeFrame(1, sampleV1Frame().Fields)
	calls := 0
	sink := func(*Frame, *Report) { calls++ }
	for i, c := range wire {
		if err := recv.Feed([]byte{c}, sink); err != nil {
			t.Fatalf("byte %d: %v", i, err)
		}
		if i < len(wire)-1 && calls != 0 {
			t.Fatalf("frame published early at byte %d", i)
		}
	}
	if calls != 1 {
		t.Fatalf("sink calls = %d, want 1", calls)
	}
}

func TestHotReloadEpochBinding(t *testing.T) {
	s1, _, ps1 := mustPlanSet(t)
	// v2' retypes score back to varint: under the new plan field 3 is
	// lossless, under the old plan it is preserved via extension.
	s2alt, err := NewSchema(2, []FieldDef{
		{ID: 1, Name: "id", Type: TVarint, Required: true},
		{ID: 3, Name: "score", Type: TVarint},
		{ID: 6, Name: "level", Type: TVarint, Required: true,
			Rule: Rule{HasDefault: true, DefaultNum: 7}},
		{ID: 15, Name: "ext", Type: TBytes, Extension: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	ps2, err := NewPlanSet(s1, s2alt)
	if err != nil {
		t.Fatal(err)
	}
	b := NewBridge(ps1)
	wire := EncodeFrame(1, sampleV1Frame().Fields)

	// Frame starts under epoch 1, reload happens mid-frame.
	recv := NewReceiver(b, DefaultLimits())
	if err := recv.Feed(wire[:len(wire)/2], nil); err != nil {
		t.Fatal(err)
	}
	b.HotReload(ps2)
	var oldOut *Frame
	var oldRep *Report
	if err := recv.Feed(wire[len(wire)/2:], func(f *Frame, r *Report) {
		oldOut, oldRep = f, r
	}); err != nil {
		t.Fatal(err)
	}
	if oldRep.Epoch != 1 || oldOut.Epoch != 1 {
		t.Fatalf("in-flight frame used wrong epoch: %s", oldRep)
	}
	if _, ok := oldOut.field(3); ok {
		t.Fatal("old plan must preserve field 3 via extension, not copy it")
	}
	if _, ok := oldOut.field(15); !ok {
		t.Fatal("old plan must emit extension carrier")
	}

	// A frame starting after the reload uses the new plan.
	recv2 := NewReceiver(b, DefaultLimits())
	var newOut *Frame
	var newRep *Report
	if err := recv2.Feed(wire, func(f *Frame, r *Report) {
		newOut, newRep = f, r
	}); err != nil {
		t.Fatal(err)
	}
	if newRep.Epoch != 2 {
		t.Fatalf("new frame used stale epoch: %s", newRep)
	}
	score, ok := newOut.field(3)
	if !ok || score.Kind != KindVarint || score.Num != 900 {
		t.Fatalf("new plan must copy field 3 losslessly: %v", newOut)
	}
}

func TestNoPartialPublish(t *testing.T) {
	_, _, ps := mustPlanSet(t)
	b := NewBridge(ps)
	broken := &Frame{Version: 1, Fields: []Field{
		{ID: 2, Kind: KindBytes, Raw: []byte("no id field")},
	}}
	published := false
	err := b.Bind().Convert(broken, DefaultLimits(), func(*Frame, *Report) {
		published = true
	})
	if err == nil {
		t.Fatal("expected conversion failure")
	}
	if published {
		t.Fatal("half-converted frame was published")
	}
}

func TestMigrationRingStabilizes(t *testing.T) {
	_, _, ps := mustPlanSet(t)
	b := NewBridge(ps)
	bind := b.Bind()
	ring := func(f *Frame) *Frame {
		var out *Frame
		other := f
		if f.Version == 1 {
			v2, _ := convertOne(t, bind, f)
			other = v2
		}
		out, _ = convertOne(t, bind, other)
		return out
	}
	once := ring(sampleV1Frame())
	twice := ring(once)
	if d := Diff(once, twice); len(d) != 0 {
		t.Fatalf("ring did not stabilize: %s", FormatDiff(d))
	}
	// The one-way loss is exactly the injected v2-only field living in the
	// carrier; everything else survives.
	if d := Diff(sampleV1Frame(), once); len(d) != 1 || d[0].FieldID != 15 || d[0].What != "extra" {
		t.Fatalf("unexpected ring diff: %s", FormatDiff(d))
	}
}

func TestBidirectionalDiffReport(t *testing.T) {
	_, _, ps := mustPlanSet(t)
	b := NewBridge(ps)
	bind := b.Bind()
	v2frame, rep1 := convertOne(t, bind, sampleV1Frame())
	if got := ids(rep1.Copied); got != "[1 2]" {
		t.Fatalf("fwd copied = %s", got)
	}
	if got := ids(rep1.Extended); got != "[3]" {
		t.Fatalf("fwd extended = %s", got)
	}
	if got := ids(rep1.Injected); got != "[6]" {
		t.Fatalf("fwd injected = %s", got)
	}
	back, rep2 := convertOne(t, bind, v2frame)
	if got := ids(rep2.Restored); got != "[3]" {
		t.Fatalf("rev restored = %s", got)
	}
	if got := ids(rep2.Extended); got != "[6]" {
		t.Fatalf("rev extended = %s", got)
	}
	diff := Diff(sampleV1Frame(), back)
	if len(diff) != 1 || diff[0].FieldID != 15 {
		t.Fatalf("unexpected diff: %s", FormatDiff(diff))
	}
	if s := rep1.String(); !strings.Contains(s, "v1->v2") {
		t.Fatalf("report string: %s", s)
	}
}

func TestDemoReplayable(t *testing.T) {
	first := Demo()
	second := Demo()
	if first != second {
		t.Fatal("demo log is not deterministic")
	}
	for _, marker := range []string{
		"plan v1->v2 lossless=[1 2] preserve-extension=[3] unrepresentable=[]",
		"negotiation ok: epoch=1",
		"foreign peer refused",
		"diff(v2, ring v2->v1->v2): identical",
		"in-flight frame converted with epoch=1 (bridge now at epoch=2)",
		"sink called: false",
		"plan set refused",
		"need-more=true",
	} {
		if !strings.Contains(first, marker) {
			t.Fatalf("demo log missing %q", marker)
		}
	}
}
