package enumkit

import (
	"errors"
	"testing"
)

// newColorEnum builds a three-version enum:
//
//	v1: 1=RED    2=GREEN    3=BLUE
//	v2: 1=RED    2=EMERALD  3=BLUE   (alias GREEN->2)
//	v3: 1=CRIMSON 2=EMERALD 3=BLUE   (alias RED->1, GREEN alias retracted)
//
// The registry's current canonical names follow the latest renames.
func newColorEnum(t *testing.T) *Registry {
	t.Helper()
	r := New("color")
	must(t, r.Define(1, "RED"), "Define 1")
	must(t, r.Define(2, "GREEN"), "Define 2")
	must(t, r.Define(3, "BLUE"), "Define 3")
	must(t, r.AddVersion(Version{
		Name:   "v1",
		Active: map[int64]string{1: "RED", 2: "GREEN", 3: "BLUE"},
	}), "Add v1")
	must(t, r.Rename(2, "EMERALD"), "rename 2")
	must(t, r.AddVersion(Version{
		Name:    "v2",
		Active:  map[int64]string{1: "RED", 2: "EMERALD", 3: "BLUE"},
		Aliases: []Alias{{Name: "GREEN", ID: 2}},
	}), "Add v2")
	must(t, r.Rename(1, "CRIMSON"), "rename 1")
	must(t, r.AddVersion(Version{
		Name:    "v3",
		Active:  map[int64]string{1: "CRIMSON", 2: "EMERALD", 3: "BLUE"},
		Aliases: []Alias{{Name: "RED", ID: 1}},
	}), "Add v3")
	return r
}

func must(t *testing.T, err error, what string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", what, err)
	}
}

func TestDecodeOldAliasAndCanonicalName(t *testing.T) {
	r := newColorEnum(t)
	v, err := r.Decode("v2", "GREEN")
	must(t, err, "decode GREEN at v2")
	if !v.Known() || v.ID != 2 || v.Kind != KnownName {
		t.Fatalf("GREEN at v2 = %+v", v)
	}
	if got := r.Canonical(v); got != "EMERALD" {
		t.Fatalf("canonical = %q, want current name EMERALD", got)
	}
}

func TestCanonicalFollowsCurrentName(t *testing.T) {
	r := newColorEnum(t)
	v, err := r.Decode("v3", "RED")
	must(t, err, "decode RED alias at v3")
	if v.ID != 1 {
		t.Fatalf("RED alias = id %d, want 1", v.ID)
	}
	if got := r.Canonical(v); got != "CRIMSON" {
		t.Fatalf("canonical = %q, want CRIMSON", got)
	}
	if got, _ := r.NameAt("v1", v.ID); got != "RED" {
		t.Fatalf("v1 active name = %q, want RED", got)
	}
}

func TestUnknownNamePassthrough(t *testing.T) {
	r := newColorEnum(t)
	// GREEN alias was retracted in v3: the token stays lexical.
	v, err := r.Decode("v3", "GREEN")
	must(t, err, "decode GREEN at v3")
	if v.Known() {
		t.Fatalf("GREEN at v3 unexpectedly known: %+v", v)
	}
	if v.Kind != UnknownName || v.Raw != "GREEN" {
		t.Fatalf("unknown name = %+v", v)
	}
	if got := r.Canonical(v); got != "GREEN" {
		t.Fatalf("canonical of unknown = %q, want lexical passthrough GREEN", got)
	}
}

func TestUnknownNumberNotCoerced(t *testing.T) {
	r := newColorEnum(t)
	v, err := r.Decode("v1", "99")
	must(t, err, "decode 99")
	if v.Known() || v.Kind != UnknownNumber || v.ID != 99 {
		t.Fatalf("99 = %+v, want UnknownNumber(99), never the first member", v)
	}
	if got := r.Canonical(v); got != "99" {
		t.Fatalf("canonical of unknown number = %q, want 99", got)
	}
	v2, err := r.Decode("v1", "0")
	must(t, err, "decode 0")
	if v2.Known() {
		t.Fatalf("0 resolved to #%d, must not be coerced", v2.ID)
	}
}

func TestNumericLiteralResolvesToStableID(t *testing.T) {
	r := newColorEnum(t)
	v, err := r.Decode("v3", "2")
	must(t, err, "decode 2")
	if v.Kind != KnownNumber || v.ID != 2 {
		t.Fatalf("numeric 2 = %+v", v)
	}
	if got := r.Canonical(v); got != "EMERALD" {
		t.Fatalf("canonical = %q, want EMERALD", got)
	}
}

func TestAliasToTwoIDsInOneVersionRejected(t *testing.T) {
	r := New("color")
	must(t, r.Define(1, "RED"), "def1")
	must(t, r.Define(2, "GREEN"), "def2")
	err := r.AddVersion(Version{
		Name:    "v1",
		Active:  map[int64]string{1: "RED", 2: "GREEN"},
		Aliases: []Alias{{Name: "RED", ID: 2}},
	})
	var ce *ConflictError
	if !errors.As(err, &ce) {
		t.Fatalf("err = %v, want ConflictError", err)
	}
	if ce.Name != "RED" || len(ce.IDs) != 2 {
		t.Fatalf("conflict = %+v", ce)
	}
}

func TestDuplicateAliasSameIDAccepted(t *testing.T) {
	r := New("color")
	must(t, r.Define(1, "RED"), "def1")
	err := r.AddVersion(Version{
		Name:    "v1",
		Active:  map[int64]string{1: "RED"},
		Aliases: []Alias{{Name: "RED", ID: 1}},
	})
	if err != nil {
		t.Fatalf("same-name same-id alias rejected: %v", err)
	}
}

func TestActiveNameCollisionInOneVersionRejected(t *testing.T) {
	r := New("color")
	must(t, r.Define(1, "RED"), "def1")
	must(t, r.Define(2, "BLUE"), "def2")
	err := r.AddVersion(Version{Name: "v1", Active: map[int64]string{1: "RED", 2: "RED"}})
	if err == nil {
		t.Fatal("two IDs sharing active name accepted")
	}
}

func TestSnapshotUnaffectedByRetraction(t *testing.T) {
	r := newColorEnum(t)
	snap, err := r.Snapshot("v2")
	must(t, err, "snapshot v2")
	if id, ok := snap.Resolve("GREEN"); !ok || id != 2 {
		t.Fatalf("snapshot GREEN = %d,%v", id, ok)
	}
	must(t, r.RetractAlias("v2", "GREEN"), "retract GREEN")
	if err := r.RetractAlias("v2", "GREEN"); err == nil {
		t.Fatal("retracting unknown alias did not error")
	}
	// Live view lost the alias; snapshot did not.
	v, err := r.Decode("v2", "GREEN")
	must(t, err, "decode after retract")
	if v.Known() {
		t.Fatal("live registry still resolves retracted alias")
	}
	if id, ok := snap.Resolve("GREEN"); !ok || id != 2 {
		t.Fatalf("snapshot mutated by retraction: %d,%v", id, ok)
	}
	if n, ok := snap.ActiveName(2); !ok || n != "EMERALD" {
		t.Fatalf("snapshot active name = %q,%v", n, ok)
	}
	// Active names cannot be retracted.
	if err := r.RetractAlias("v3", "CRIMSON"); err == nil {
		t.Fatal("active name retraction accepted")
	}
}

func TestRenameKeepsSerializationIdentity(t *testing.T) {
	r := newColorEnum(t)
	before, err := r.Decode("v1", "RED")
	must(t, err, "decode RED at v1")
	wire, err := before.MarshalBinary()
	must(t, err, "marshal RED")
	// Replay the same bytes after the v3 rename: identity is the ID.
	again, err := UnmarshalValue(wire)
	must(t, err, "unmarshal")
	if again.ID != 1 || !again.Known() {
		t.Fatalf("identity changed by rename: %+v", again)
	}
	if got := r.Canonical(again); got != "CRIMSON" {
		t.Fatalf("canonical after rename = %q, want CRIMSON", got)
	}
	if name, _ := r.NameAt("v3", again.ID); name != "CRIMSON" {
		t.Fatalf("v3 name = %q, want CRIMSON", name)
	}
}
