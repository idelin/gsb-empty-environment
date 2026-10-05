package segvm

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func digestOf(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func segFileExists(t *testing.T, dir string, data []byte) bool {
	t.Helper()
	_, err := os.Stat(filepath.Join(dir, "segments", digestOf(data)+".seg"))
	return err == nil
}

func TestPublishReadAndHistory(t *testing.T) {
	dir := t.TempDir()
	m, err := Open(OSFS{}, dir)
	if err != nil {
		t.Fatal(err)
	}
	if m.Version() != 0 {
		t.Fatalf("fresh store version = %d, want 0", m.Version())
	}

	v1 := publish(t, m, []Change{
		{Name: "alpha", Data: []byte("alpha-1")},
		{Name: "beta", Data: []byte("beta-1")},
	}, nil)
	if v1 != 1 {
		t.Fatalf("v1 = %d", v1)
	}
	v2 := publish(t, m, []Change{{Name: "alpha", Data: []byte("alpha-2")}}, []string{"beta"})
	if v2 != 2 {
		t.Fatalf("v2 = %d", v2)
	}

	data, err := m.Read("alpha")
	if err != nil || string(data) != "alpha-2" {
		t.Fatalf("read alpha = %q, %v", data, err)
	}
	if _, err := m.Read("beta"); err == nil {
		t.Fatal("beta should be gone in version 2")
	}

	recs := m.Versions()
	if len(recs) != 2 || recs[0].Version != 1 || recs[1].Version != 2 {
		t.Fatalf("versions = %+v", recs)
	}
	if len(recs[0].Adds) != 2 || len(recs[1].Adds) != 1 || len(recs[1].Removes) != 1 {
		t.Fatalf("record contents = %+v", recs)
	}

	s1, err := m.StateAt(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(s1) != 2 || s1["alpha"].Digest == m.Segments()["alpha"].Digest {
		t.Fatal("StateAt(1) should hold the old alpha digest")
	}

	// Reopen and confirm durability.
	m2, err := Open(OSFS{}, dir)
	if err != nil {
		t.Fatal(err)
	}
	if m2.Version() != 2 {
		t.Fatalf("reopened version = %d", m2.Version())
	}
}

func TestSnapshotPinsProtectFromGC(t *testing.T) {
	dir := t.TempDir()
	m, err := Open(OSFS{}, dir)
	if err != nil {
		t.Fatal(err)
	}
	a, b, c := []byte("data-a"), []byte("data-b"), []byte("data-c")
	publish(t, m, []Change{{Name: "a", Data: a}, {Name: "b", Data: b}}, nil)

	snap := m.Snapshot()
	if snap.Version != 1 {
		t.Fatalf("snap version = %d", snap.Version)
	}

	// Version 2 removes a, keeps b (shared), adds c.
	publish(t, m, []Change{{Name: "c", Data: c}}, []string{"a"})

	res, err := m.GC()
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Removed) != 0 {
		t.Fatalf("GC removed %v while snapshot pins v1", res.Removed)
	}
	if got, err := snap.Read("a"); err != nil || string(got) != "data-a" {
		t.Fatalf("snapshot read a = %q, %v", got, err)
	}

	snap.Close()
	res, err = m.GC()
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Removed) != 1 {
		t.Fatalf("GC removed %v, want exactly a's file", res.Removed)
	}
	if segFileExists(t, dir, a) {
		t.Fatal("a's file should be reclaimed")
	}
	if !segFileExists(t, dir, b) || !segFileExists(t, dir, c) {
		t.Fatal("shared/current segments must survive GC")
	}
	if _, err := m.Read("b"); err != nil {
		t.Fatalf("read shared segment after GC: %v", err)
	}
}

func TestContentAddressedSharedSegment(t *testing.T) {
	dir := t.TempDir()
	m, err := Open(OSFS{}, dir)
	if err != nil {
		t.Fatal(err)
	}
	shared := []byte("shared-content")
	publish(t, m, []Change{{Name: "x", Data: shared}}, nil)
	publish(t, m, []Change{{Name: "y", Data: shared}}, nil) // same digest

	entries, err := os.ReadDir(filepath.Join(dir, "segments"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 deduplicated segment file, got %d", len(entries))
	}

	// Remove x; y still references the same file.
	publish(t, m, nil, []string{"x"})
	res, err := m.GC()
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Removed) != 0 {
		t.Fatalf("shared segment wrongly reclaimed: %v", res.Removed)
	}
	if got, err := m.Read("y"); err != nil || string(got) != "shared-content" {
		t.Fatalf("read y = %q, %v", got, err)
	}
}

func TestOrphanIdentification(t *testing.T) {
	dir := t.TempDir()
	m, err := Open(OSFS{}, dir)
	if err != nil {
		t.Fatal(err)
	}
	live := []byte("live")
	publish(t, m, []Change{{Name: "live", Data: live}}, nil)

	segDir := filepath.Join(dir, "segments")
	// Unknown foreign file: must be identified but never served or deleted.
	if err := os.WriteFile(filepath.Join(segDir, "notes.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Stale tmp file from an interrupted publish: reclaimed.
	if err := os.WriteFile(filepath.Join(segDir, "deadbeef.tmp-1"), []byte("partial"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Unreferenced but well-formed segment file: reclaimed.
	stale := []byte("stale")
	if err := os.WriteFile(filepath.Join(segDir, digestOf(stale)+".seg"), stale, 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := m.GC()
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Orphans) != 1 || res.Orphans[0] != "notes.txt" {
		t.Fatalf("orphans = %v", res.Orphans)
	}
	if len(res.Tmp) != 1 {
		t.Fatalf("tmp = %v", res.Tmp)
	}
	if len(res.Removed) != 1 {
		t.Fatalf("removed = %v", res.Removed)
	}
	if _, err := os.Stat(filepath.Join(segDir, "notes.txt")); err != nil {
		t.Fatal("orphan file must be left untouched")
	}
	// The orphan is not a segment and can never be read as one.
	if _, err := m.Read("notes.txt"); err == nil {
		t.Fatal("orphan must not be served as a segment")
	}
	if !segFileExists(t, dir, live) {
		t.Fatal("live segment must survive GC")
	}
}

func TestCorruptSegmentFileDetectedOnOpen(t *testing.T) {
	dir := t.TempDir()
	m, err := Open(OSFS{}, dir)
	if err != nil {
		t.Fatal(err)
	}
	victim := []byte("victim")
	publish(t, m, []Change{{Name: "v", Data: victim}}, nil)

	p := filepath.Join(dir, "segments", digestOf(victim)+".seg")
	if err := os.WriteFile(p, []byte("tampered"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(OSFS{}, dir); err == nil {
		t.Fatal("expected open to fail on digest mismatch")
	}
}

func TestPublishValidation(t *testing.T) {
	dir := t.TempDir()
	m, err := Open(OSFS{}, dir)
	if err != nil {
		t.Fatal(err)
	}
	publish(t, m, []Change{{Name: "a", Data: []byte("a")}}, nil)
	cases := []struct {
		adds    []Change
		removes []string
	}{
		{[]Change{{Name: "", Data: []byte("x")}}, nil},
		{[]Change{{Name: "a/b", Data: []byte("x")}}, nil},
		{[]Change{{Name: "a", Data: []byte("x")}, {Name: "a", Data: []byte("y")}}, nil},
		{nil, []string{"ghost"}},
		{[]Change{{Name: "n", Data: []byte("x")}}, []string{"n"}},
	}
	for i, c := range cases {
		if _, err := m.Publish(c.adds, c.removes); err == nil {
			t.Fatalf("case %d: expected validation error", i)
		}
	}
	if m.Version() != 1 {
		t.Fatalf("failed publishes must not advance the version, got %d", m.Version())
	}
}

func TestManyVersionsChain(t *testing.T) {
	dir := t.TempDir()
	m, err := Open(OSFS{}, dir)
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 25; i++ {
		name := fmt.Sprintf("seg-%02d", i)
		publish(t, m, []Change{{Name: name, Data: []byte(fmt.Sprintf("content-%d", i))}}, nil)
	}
	m2, err := Open(OSFS{}, dir)
	if err != nil {
		t.Fatal(err)
	}
	if m2.Version() != 25 || len(m2.Segments()) != 25 || len(m2.Versions()) != 25 {
		t.Fatalf("version=%d segments=%d records=%d",
			m2.Version(), len(m2.Segments()), len(m2.Versions()))
	}
}
