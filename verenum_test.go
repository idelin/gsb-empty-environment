package verenum

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func newTestRegistry(t *testing.T) *Registry {
	t.Helper()
	r := New()
	must(t, r.Register(1, 1, "red"))
	must(t, r.Register(1, 2, "green"))
	// v2: id 1 renamed red -> crimson, old name kept as alias.
	must(t, r.Register(2, 1, "crimson", "red"))
	must(t, r.Register(2, 2, "green"))
	return r
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// 1. Stable numeric ID across rename; canonical output uses the current name.
func TestStableIDAndRename(t *testing.T) {
	r := newTestRegistry(t)

	id, ok := r.Lookup(1, "red")
	if !ok || id != 1 {
		t.Fatalf(`Lookup v1 "red" = %d,%v want 1,true`, id, ok)
	}
	if id2, ok := r.Lookup(2, "crimson"); !ok || id2 != 1 {
		t.Fatalf(`Lookup v2 "crimson" = %d,%v want 1,true`, id2, ok)
	}
	got, err := r.Migrate(1, 2, "red", "color")
	must(t, err)
	if got != "crimson" {
		t.Fatalf(`Migrate "red" v1->v2 = %q want "crimson"`, got)
	}

	raw, err := r.Decode(1, []byte(`"red"`))
	must(t, err)
	out, err := r.Encode(2, raw)
	must(t, err)
	if string(out) != `"crimson"` {
		t.Fatalf("encode at v2 = %s want crimson", out)
	}
}

// 2. Old-name alias still decodes at the new version.
func TestOldAliasDecodes(t *testing.T) {
	r := newTestRegistry(t)
	v, err := r.Decode(2, []byte(`"red"`))
	must(t, err)
	if !v.Known || v.ID != 1 {
		t.Fatalf(`old alias "red" at v2 = %+v want known id=1`, v)
	}
}

// 3. Unknown lexical value passes through byte-equivalently; unknown numbers
// are never coerced to the first declared member.
func TestUnknownPassthrough(t *testing.T) {
	r := newTestRegistry(t)

	v, err := r.Decode(2, []byte(`"fuchsia"`))
	must(t, err)
	if v.Known || v.Raw != "fuchsia" {
		t.Fatalf("unknown name = %+v", v)
	}
	out, err := r.Encode(2, v)
	must(t, err)
	if string(out) != `"fuchsia"` {
		t.Fatalf("lexical passthrough changed: %s", out)
	}

	n, err := r.Decode(2, []byte(`9999`))
	must(t, err)
	if n.Known || n.ID != 9999 {
		t.Fatalf("unknown number = %+v, want 9999 unknown (not coerced to id 1)", n)
	}
	numOut, err := r.Encode(2, n)
	must(t, err)
	if string(numOut) != `9999` {
		t.Fatalf("numeric passthrough = %s", numOut)
	}

	// Known numbers still resolve.
	k, err := r.Decode(1, []byte(`1`))
	must(t, err)
	if !k.Known || k.ID != 1 {
		t.Fatalf("known number = %+v", k)
	}
}

// 4. Same alias mapped to two IDs inside one version is rejected.
func TestConflictingAliasRejected(t *testing.T) {
	r := New()
	if err := r.Register(1, 1, "red"); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(1, 2, "green", "red"); err == nil {
		t.Fatal("expected rejection of alias red -> id 2 while red is id 1")
	}
	var ce *ConflictError
	if err := r.Register(1, 2, "green", "red"); !errors.As(err, &ce) {
		t.Fatalf("want *ConflictError, got %T", err)
	}
	// Duplicate alias for the SAME id is idempotent and legal.
	if err := r.Register(1, 1, "red", "rouge"); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(1, 1, "red", "rouge"); err != nil {
		t.Fatalf("same alias -> same id must be accepted: %v", err)
	}
}

// 5. Merging two different IDs onto one active name is rejected; alias
// collisions across versions merge but migration returns ambiguity.
func TestMergeAndMigrationAmbiguity(t *testing.T) {
	r := newTestRegistry(t)
	// v3: id 3 steals the old alias "red" -> two IDs across versions.
	must(t, r.Register(3, 1, "crimson"))
	must(t, r.Register(3, 2, "green"))
	must(t, r.Register(3, 3, "scarlet", "red"))

	_, err := r.Migrate(1, 3, "red", "items[2].status")
	var amb *AmbiguityError
	if !errors.As(err, &amb) {
		t.Fatalf("want *AmbiguityError, got %v", err)
	}
	if amb.Path != "items[2].status" || amb.Name != "red" {
		t.Fatalf("ambiguity metadata = %+v", amb)
	}
	if _, ok := amb.Chains[1]; !ok {
		t.Fatalf("missing chain for id 1: %+v", amb.Chains)
	}
	if _, ok := amb.Chains[3]; !ok {
		t.Fatalf("missing chain for id 3: %+v", amb.Chains)
	}
	msg := err.Error()
	if !strings.Contains(msg, "items[2].status") {
		t.Fatalf("error must carry original field path: %s", msg)
	}

	// Active-name collision across two distinct IDs cannot be merged at all.
	bad := New()
	must(t, bad.Register(1, 1, "red"))
	must(t, bad.Register(2, 2, "red"))
	if _, err := bad.Migrate(1, 2, "red", "x"); !errors.As(err, new(*MergeError)) {
		t.Fatalf("want *MergeError for shared active name, got %v", err)
	}
}

// 6. Multi-hop rename: old names keep resolving through intermediate
// versions and output the newest canonical name.
func TestMultiHopRename(t *testing.T) {
	r := newTestRegistry(t)
	must(t, r.Register(3, 1, "carmine", "crimson", "red"))
	got, err := r.Migrate(1, 3, "red", "p")
	must(t, err)
	if got != "carmine" {
		t.Fatalf(`multi-hop migrate = %q want "carmine"`, got)
	}
}

// 7. VerifyPath reports the shortest conflict chain with the field path; a
// consistent path passes.
func TestVerifyPath(t *testing.T) {
	good := newTestRegistry(t)
	if err := good.VerifyPath([]Version{1, 2}, "payload.color"); err != nil {
		t.Fatalf("consistent path rejected: %v", err)
	}

	bad := newTestRegistry(t)
	must(t, bad.Register(3, 1, "crimson"))
	must(t, bad.Register(3, 3, "scarlet", "red"))
	err := bad.VerifyPath([]Version{1, 2, 3}, "payload.color")
	var pc *PathConflictError
	if !errors.As(err, &pc) {
		t.Fatalf("want *PathConflictError, got %v", err)
	}
	if pc.Path != "payload.color" || pc.Name != "red" {
		t.Fatalf("path conflict metadata = %+v", pc)
	}
	if len(pc.Chains[1]) == 0 || len(pc.Chains[3]) == 0 {
		t.Fatalf("expected shortest chains for both IDs: %+v", pc.Chains)
	}
}

// 8. Historical schema snapshots are unaffected by later alias withdrawal.
func TestSnapshotUnaffectedByWithdrawal(t *testing.T) {
	r := newTestRegistry(t)
	snap, ok := r.Snapshot(2)
	if !ok {
		t.Fatal("missing v2 snapshot")
	}
	if id, ok := snap.Lookup("red"); !ok || id != 1 {
		t.Fatalf("snapshot lookup red = %d,%v", id, ok)
	}
	if !r.WithdrawAlias(2, "red") {
		t.Fatal("withdrawal reported missing alias")
	}
	if _, ok := r.Lookup(2, "red"); ok {
		t.Fatal("live graph should no longer know withdrawn alias")
	}
	if id, ok := snap.Lookup("red"); !ok || id != 1 {
		t.Fatalf("historical snapshot changed after withdrawal: %d,%v", id, ok)
	}
}

// 9. Decoder hardening: length is validated before allocation; non-enum JSON
// tokens rejected; no host code executed (stdlib JSON only).
func TestDecodeLimitsAndSafety(t *testing.T) {
	r := newTestRegistry(t)

	if _, err := r.Decode(2, make([]byte, MaxInputLen+1)); !errors.Is(err, ErrInputTooLarge) {
		t.Fatalf("oversized input: %v", err)
	}
	long := bytes.Repeat([]byte("a"), MaxNameLen+1)
	token := append([]byte{'"'}, long...)
	token = append(token, '"')
	if _, err := r.Decode(2, token); !errors.Is(err, ErrNameTooLong) {
		t.Fatalf("oversized name: %v", err)
	}
	if _, err := r.Decode(2, nil); !errors.Is(err, ErrEmptyInput) {
		t.Fatalf("empty input: %v", err)
	}
	if _, err := r.Decode(2, []byte(`["red"]`)); !errors.Is(err, ErrBadToken) {
		t.Fatalf("array token must be rejected: %v", err)
	}
	if _, err := r.Decode(2, []byte(`1.5`)); !errors.Is(err, ErrBadToken) {
		t.Fatalf("float token must be rejected: %v", err)
	}
	if _, err := r.Decode(2, []byte(`{}`)); !errors.Is(err, ErrBadToken) {
		t.Fatalf("object token must be rejected: %v", err)
	}
}

// 10. Renaming does not change serialized identity: a value written as an ID
// before the rename keeps the same ID and re-emits the new name.
func TestRenamePreservesIdentity(t *testing.T) {
	r := newTestRegistry(t)
	before, err := r.Decode(1, []byte(`1`))
	must(t, err)
	after, err := r.Decode(2, []byte(`1`))
	must(t, err)
	if before.ID != after.ID || !before.Known || !after.Known {
		t.Fatalf("identity drift across rename: %+v vs %+v", before, after)
	}
	out, err := r.Encode(2, after)
	must(t, err)
	if string(out) != `"crimson"` {
		t.Fatalf("renamed value encodes old name: %s", out)
	}
	back, err := r.Decode(2, []byte(`"crimson"`))
	must(t, err)
	if back.ID != 1 || !back.Known {
		t.Fatalf("new name does not resolve to stable id: %+v", back)
	}
}

// 11. Ambiguity error renders the shortest conflict chain in human form.
func TestAmbiguityChainRendering(t *testing.T) {
	r := newTestRegistry(t)
	must(t, r.Register(3, 1, "crimson"))
	must(t, r.Register(3, 3, "scarlet", "red"))
	_, err := r.Migrate(1, 3, "red", "items[2].status")
	msg := err.Error()
	for _, want := range []string{"ambiguous name", "items[2].status", "id=1", "id=3", "alias", "crimson", "scarlet"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("chain output missing %q:\n%s", want, msg)
		}
	}
	t.Log("\n" + msg)
}

// 12. Unknown name migration is explicitly reported, never auto-selected.
func TestUnknownNameMigration(t *testing.T) {
	r := newTestRegistry(t)
	_, err := r.Migrate(1, 2, "fuchsia", "p")
	var unk *UnknownNameError
	if !errors.As(err, &unk) {
		t.Fatalf("want *UnknownNameError, got %v", err)
	}
}
