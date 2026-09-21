package bridge

import (
	"strings"
	"testing"
)

func schemaV3() *Schema {
	return &Schema{
		Name: "v3", Version: 4,
		Fields: map[uint32]Field{
			1: {ID: 1, Name: "id", Type: TyInt, Required: true},
			2: {ID: 2, Name: "fullname", Type: TyString},
			3: {ID: 3, Name: "score", Type: TyInt},
			8: {ID: 8, Name: "extra", Type: TyString},
		},
	}
}

func TestPlanClassification(t *testing.T) {
	pair, err := CompilePair(replayV1(), replayV2())
	if err != nil {
		t.Fatal(err)
	}
	class := map[uint32]FieldClass{}
	for _, e := range pair.AB.Entries {
		class[e.FieldID] = e.Class
	}
	for _, id := range []uint32{1, 2, 4, 6} {
		if class[id] != ClassLossless {
			t.Errorf("field %d: got %s, want lossless", id, class[id])
		}
	}
	if class[3] != ClassExtension {
		t.Errorf("field 3: got %s, want extension", class[3])
	}
}

func TestRenameKeepsID(t *testing.T) {
	pair, err := CompilePair(replayV1(), replayV2())
	if err != nil {
		t.Fatal(err)
	}
	var renamed *PlanEntry
	for i, e := range pair.AB.Entries {
		if e.FieldID == 2 {
			renamed = &pair.AB.Entries[i]
		}
	}
	if renamed == nil || renamed.Class != ClassLossless || !strings.Contains(renamed.Reason, "renamed") {
		t.Fatalf("field 2 entry = %+v, want lossless rename", renamed)
	}
	m := NewMessage()
	m.Values[1] = IntVal(1)
	m.Values[2] = StrVal("ann")
	out, _, err := Convert(pair.AB, m)
	if err != nil {
		t.Fatal(err)
	}
	if got := out.Values[2]; got.Type != TyString || got.Str != "ann" {
		t.Fatalf("renamed field lost: %v", got)
	}
}

func TestNegotiationRejectRequired(t *testing.T) {
	v2strict := &Schema{
		Name: "v2s", Version: 2,
		Fields: map[uint32]Field{
			1: {ID: 1, Name: "id", Type: TyInt, Required: true},
			7: {ID: 7, Name: "tenant", Type: TyInt, Required: true}, // v1 cannot provide, no default
		},
	}
	_, err := Negotiate(replayV1(), v2strict)
	if err == nil || !strings.Contains(err.Error(), "negotiation rejected") {
		t.Fatalf("err = %v, want negotiation rejected", err)
	}
	if !strings.Contains(err.Error(), "field 7") {
		t.Fatalf("error should name field 7: %v", err)
	}
}

func TestNegotiationAcceptsDefaultedRequired(t *testing.T) {
	if _, err := Negotiate(replayV1(), replayV2()); err != nil {
		t.Fatalf("required field 6 has a migration-rule default, must negotiate: %v", err)
	}
}

func TestSessionDigestBinding(t *testing.T) {
	s1, err := Negotiate(replayV1(), replayV2())
	if err != nil {
		t.Fatal(err)
	}
	s2, err := Negotiate(replayV1(), replayV2())
	if err != nil {
		t.Fatal(err)
	}
	if s1.Digest() != s2.Digest() {
		t.Fatal("same schemas must bind the same digest")
	}
	s3, err := Negotiate(replayV1(), replayV2b())
	if err != nil {
		t.Fatal(err)
	}
	if s1.Digest() == s3.Digest() {
		t.Fatal("different plans must bind different digests")
	}
}

func TestExtensionConflictSchema(t *testing.T) {
	bad := &Schema{
		Name: "bad", Version: 1,
		Fields: map[uint32]Field{
			ExtFieldID: {ID: ExtFieldID, Name: "oops", Type: TyBytes},
		},
	}
	if err := bad.Validate(); err == nil {
		t.Fatal("schema defining the reserved extension ID must be rejected")
	}
	if _, err := CompilePair(replayV1(), bad); err == nil {
		t.Fatal("compiling against a schema with the reserved ID must fail")
	}
}
