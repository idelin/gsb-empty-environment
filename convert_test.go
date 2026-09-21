package bridge

import (
	"errors"
	"testing"
)

func sampleV1Message() *Message {
	m := NewMessage()
	m.Values[1] = IntVal(7)
	m.Values[2] = StrVal("ann")
	m.Values[3] = IntVal(42)
	return m
}

func TestMigrationRing(t *testing.T) {
	v1, v2, v3 := replayV1(), replayV2(), schemaV3()
	p12, err := CompilePair(v1, v2)
	if err != nil {
		t.Fatal(err)
	}
	p23, err := CompilePair(v2, v3)
	if err != nil {
		t.Fatal(err)
	}
	p31, err := CompilePair(v3, v1)
	if err != nil {
		t.Fatal(err)
	}
	orig := sampleV1Message()
	m1, _, err := Convert(p12.AB, orig)
	if err != nil {
		t.Fatal(err)
	}
	m2, _, err := Convert(p23.AB, m1)
	if err != nil {
		t.Fatal(err)
	}
	m3, _, err := Convert(p31.AB, m2)
	if err != nil {
		t.Fatal(err)
	}
	report := CompareMessages(orig, m3)
	if !report.Clean() {
		t.Fatalf("ring v1->v2->v3->v1 not clean:\n%s", report)
	}
	for id, want := range orig.Values {
		if !m3.Values[id].Equal(want) {
			t.Errorf("field %d = %v, want %v", id, m3.Values[id], want)
		}
	}
}

func TestRoundTripBothDirections(t *testing.T) {
	pair, err := CompilePair(replayV1(), replayV2())
	if err != nil {
		t.Fatal(err)
	}
	back, report, err := RoundTrip(pair, AtoB, sampleV1Message())
	if err != nil {
		t.Fatal(err)
	}
	if !report.Clean() {
		t.Fatalf("v1->v2->v1 not clean:\n%s", report)
	}
	if !back.Values[3].Equal(IntVal(42)) {
		t.Fatal("extension-preserved field 3 not restored")
	}

	m := NewMessage()
	m.Values[1] = IntVal(1)
	m.Values[2] = StrVal("x")
	m.Values[4] = BytesVal([]byte{0xde, 0xad})
	m.Values[6] = IntVal(5)
	back2, report2, err := RoundTrip(pair, BtoA, m)
	if err != nil {
		t.Fatal(err)
	}
	if !report2.Clean() {
		t.Fatalf("v2->v1->v2 not clean:\n%s", report2)
	}
	if !back2.Values[4].Equal(BytesVal([]byte{0xde, 0xad})) || !back2.Values[6].Equal(IntVal(5)) {
		t.Fatal("v2-only fields not restored after storage in v1")
	}
}

func TestUnknownFieldPreservedThroughV1(t *testing.T) {
	pair, err := CompilePair(replayV1(), replayV2())
	if err != nil {
		t.Fatal(err)
	}
	m := NewMessage()
	m.Values[1] = IntVal(1)
	m.Values[2] = StrVal("x")
	m.Values[6] = IntVal(2)
	m.Unknown = []RawField{{ID: 99, Type: TyBytes, Data: []byte{1, 2, 3}}}
	back, report, err := RoundTrip(pair, BtoA, m)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Clean() {
		t.Fatalf("round trip with unknown field not clean:\n%s", report)
	}
	found := false
	for _, f := range flattenUnknown(back) {
		if f.ID == 99 && string(f.Data) == string([]byte{1, 2, 3}) {
			found = true
		}
	}
	if !found {
		t.Fatal("unknown field 99 lost after v2->v1->v2")
	}
}

func TestExtensionConflictPayload(t *testing.T) {
	pair, err := CompilePair(replayV1(), replayV2())
	if err != nil {
		t.Fatal(err)
	}
	m := NewMessage()
	m.Values[1] = IntVal(1)
	// An unknown field colliding with the reserved extension ID, but with
	// a payload that is not a valid extension blob.
	m.Unknown = []RawField{{ID: ExtFieldID, Type: TyBytes, Data: []byte{0xFF, 0xFF}}}
	_, _, err = Convert(pair.AB, m)
	if !errors.Is(err, ErrBadData) {
		t.Fatalf("err = %v, want ErrBadData for corrupt extension blob", err)
	}
}

func TestDefaultInjected(t *testing.T) {
	pair, err := CompilePair(replayV1(), replayV2())
	if err != nil {
		t.Fatal(err)
	}
	out, tr, err := Convert(pair.AB, sampleV1Message())
	if err != nil {
		t.Fatal(err)
	}
	if !out.Values[6].Equal(IntVal(1)) {
		t.Fatalf("field 6 = %v, want default 1", out.Values[6])
	}
	if len(tr.Defaulted) != 1 || tr.Defaulted[0] != 6 {
		t.Fatalf("trace.Defaulted = %v, want [6]", tr.Defaulted)
	}
}

func TestDiffReportDetectsLoss(t *testing.T) {
	pair, err := CompilePair(replayV1(), replayV2())
	if err != nil {
		t.Fatal(err)
	}
	back, _, err := RoundTrip(pair, AtoB, sampleV1Message())
	if err != nil {
		t.Fatal(err)
	}
	delete(back.Values, 3) // simulate loss
	report := CompareMessages(sampleV1Message(), back)
	if report.Clean() {
		t.Fatal("report must be dirty after simulated loss")
	}
	found := false
	for _, e := range report.Entries {
		if e.Kind == DiffLost && e.FieldID == 3 {
			found = true
		}
	}
	if !found {
		t.Fatalf("report missing LOST entry for field 3:\n%s", report)
	}
}
