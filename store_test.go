package netsec

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return store
}

func mustCommit(t *testing.T, store *Store, object string, content []byte, label *Label) *Handle {
	t.Helper()
	handle, err := store.Commit(context.Background(), CommitInput{Object: object, Content: content, Label: label})
	if err != nil {
		t.Fatalf("Commit %q: %v", object, err)
	}
	return handle
}

// seedLegacyUnlabeledVersion writes a current version whose manifest has no
// label, simulating an object whose security metadata went missing. This
// helper exists only to exercise default-deny handling.
func seedLegacyUnlabeledVersion(t *testing.T, store *Store, object string, content []byte) int64 {
	t.Helper()
	version := int64(1)
	dir := store.versionDir(object, version)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir version: %v", err)
	}
	record := manifest{
		Version: version,
		Object:  object,
		Digest:  sha256Hex(content),
		Label:   nil,
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), data, 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "content.bin"), content, 0o600); err != nil {
		t.Fatalf("write content: %v", err)
	}
	if err := os.WriteFile(store.currentPath(object), []byte("v1"), 0o600); err != nil {
		t.Fatalf("write current: %v", err)
	}
	return version
}

func TestCommitOpenBindsDigestAndLabel(t *testing.T) {
	store := newTestStore(t)
	content := []byte("classified payload")
	label := &Label{Level: LevelSecret, Owner: "alice"}

	handle := mustCommit(t, store, "doc-1", content, label)

	if handle.Version() != 1 {
		t.Fatalf("version = %d, want 1", handle.Version())
	}
	if handle.Digest() != sha256Hex(content) {
		t.Fatalf("digest not bound to content")
	}
	if handle.Label() == nil || handle.Label().Level != LevelSecret || handle.Label().Owner != "alice" {
		t.Fatalf("label not bound: %+v", handle.Label())
	}

	got, err := store.Open("doc-1")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if got.Version() != handle.Version() || got.Digest() != handle.Digest() {
		t.Fatalf("reopened handle does not match committed handle")
	}
	if string(got.Content()) != string(content) {
		t.Fatalf("content mismatch")
	}
	if err := got.Authorize(LevelSecret); err != nil {
		t.Fatalf("Authorize at label level: %v", err)
	}
	if err := got.Authorize(LevelConfidential); !errors.Is(err, ErrDenied) {
		t.Fatalf("Authorize below label: got %v, want ErrDenied", err)
	}
}

func TestHandleIsImmutableAcrossNewCommits(t *testing.T) {
	store := newTestStore(t)
	old := mustCommit(t, store, "doc", []byte("v1"), &Label{Level: LevelUnclassified})
	mustCommit(t, store, "doc", []byte("v2"), &Label{Level: LevelConfidential})

	if string(old.Content()) != "v1" || old.Version() != 1 || old.Label().Level != LevelUnclassified {
		t.Fatalf("old handle mutated by later commit: %+v", old)
	}
	pinned, err := store.OpenVersion("doc", 1)
	if err != nil {
		t.Fatalf("OpenVersion: %v", err)
	}
	if string(pinned.Content()) != "v1" {
		t.Fatalf("historical version not readable")
	}

	// Mutating returned bytes must not affect the handle's snapshot.
	bytes := pinned.Content()
	bytes[0] = 'X'
	if string(mustOpenDoc(t, store, 1).Content()) != "v1" {
		t.Fatalf("handle content was mutated through returned slice")
	}
}

func mustOpenDoc(t *testing.T, store *Store, version int64) *Handle {
	t.Helper()
	handle, err := store.OpenVersion("doc", version)
	if err != nil {
		t.Fatalf("OpenVersion(%d): %v", version, err)
	}
	return handle
}

func TestCommitRejectsMissingLabel(t *testing.T) {
	store := newTestStore(t)
	_, err := store.Commit(context.Background(), CommitInput{Object: "doc", Content: []byte("x")})
	if !errors.Is(err, ErrLabelRequired) {
		t.Fatalf("Commit without label: got %v, want ErrLabelRequired", err)
	}
	if _, err := store.CurrentVersion("doc"); !errors.Is(err, ErrVersionNotFound) {
		t.Fatalf("rejected commit left state: %v", err)
	}
}

func TestMissingLabelDefaultsToDeny(t *testing.T) {
	store := newTestStore(t)
	seedLegacyUnlabeledVersion(t, store, "legacy", []byte("no label here"))

	handle, err := store.Open("legacy")
	if err != nil {
		t.Fatalf("Open unlabeled version should still load for audit: %v", err)
	}
	if err := handle.Authorize(LevelTopSecret); !errors.Is(err, ErrMissingLabel) {
		t.Fatalf("Authorize unlabeled at top clearance: got %v, want ErrMissingLabel", err)
	}
}

func TestDowngradeRequiresThenConsumesApproval(t *testing.T) {
	store := newTestStore(t)
	mustCommit(t, store, "doc", []byte("secret"), &Label{Level: LevelSecret})

	// Without an approval, the downgrade is refused and nothing is published.
	_, err := store.Commit(context.Background(), CommitInput{
		Object:  "doc",
		Content: []byte("now public"),
		Label:   &Label{Level: LevelUnclassified},
	})
	if !errors.Is(err, ErrDowngradeApproval) {
		t.Fatalf("downgrade without approval: got %v, want ErrDowngradeApproval", err)
	}
	if v, _ := store.CurrentVersion("doc"); v != 1 {
		t.Fatalf("refused downgrade changed current to %d", v)
	}

	approval := DowngradeApproval{
		Object:      "doc",
		FromVersion: 1,
		FromLevel:   LevelSecret,
		ToLevel:     LevelUnclassified,
		Approver:    "security-officer",
		Reason:      "declassified per ticket",
	}
	if err := store.GrantDowngradeApproval(approval); err != nil {
		t.Fatalf("grant: %v", err)
	}

	handle := mustCommit(t, store, "doc", []byte("now public"), &Label{Level: LevelUnclassified})
	if handle.Version() != 2 || handle.Label().Level != LevelUnclassified {
		t.Fatalf("approved downgrade did not publish: %+v", handle)
	}

	// The approval is single-use. Raise the level again, then attempt another
	// downgrade without granting a new approval: it must be refused.
	mustCommit(t, store, "doc", []byte("raised"), &Label{Level: LevelSecret})
	_, err = store.Commit(context.Background(), CommitInput{
		Object:  "doc",
		Content: []byte("again"),
		Label:   &Label{Level: LevelUnclassified},
	})
	if !errors.Is(err, ErrDowngradeApproval) {
		t.Fatalf("reused approval: got %v, want ErrDowngradeApproval", err)
	}
}

func TestApprovalMustMatchExactChange(t *testing.T) {
	store := newTestStore(t)
	mustCommit(t, store, "doc", []byte("secret"), &Label{Level: LevelSecret})

	wrong := DowngradeApproval{
		Object:      "doc",
		FromVersion: 1,
		FromLevel:   LevelSecret,
		ToLevel:     LevelConfidential, // request below actually targets Unclassified
		Approver:    "security-officer",
	}
	if err := store.GrantDowngradeApproval(wrong); err != nil {
		t.Fatalf("grant: %v", err)
	}
	_, err := store.Commit(context.Background(), CommitInput{
		Object:  "doc",
		Content: []byte("x"),
		Label:   &Label{Level: LevelUnclassified},
	})
	if !errors.Is(err, ErrApprovalRejected) {
		t.Fatalf("mismatched approval: got %v, want ErrApprovalRejected", err)
	}
}

func TestEqualOrHigherLevelNeedsNoApproval(t *testing.T) {
	store := newTestStore(t)
	mustCommit(t, store, "doc", []byte("a"), &Label{Level: LevelConfidential})
	mustCommit(t, store, "doc", []byte("b"), &Label{Level: LevelConfidential})
	mustCommit(t, store, "doc", []byte("c"), &Label{Level: LevelTopSecret})
	if v, _ := store.CurrentVersion("doc"); v != 3 {
		t.Fatalf("current = %d, want 3", v)
	}
}
