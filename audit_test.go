package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func genStore(t *testing.T, segs, keys int) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "store")
	if err := Gen(dir, segs, keys); err != nil {
		t.Fatal(err)
	}
	return dir
}

func snapshotTree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		if info.IsDir() {
			out[rel+"/"] = "dir"
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		out[rel] = hex.EncodeToString(sum[:]) + "|" + info.ModTime().UTC().String()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func actionTypes(rep *Report) []string {
	var out []string
	for _, a := range rep.Plan.Actions {
		out = append(out, a.Type)
	}
	sort.Strings(out)
	return out
}

func hasIssue(rep *Report, code string) bool {
	for _, is := range rep.Issues {
		if is.Code == code {
			return true
		}
	}
	return false
}

func TestAuditCleanStore(t *testing.T) {
	dir := genStore(t, 3, 8)
	rep, err := Audit(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Clean() {
		t.Fatalf("clean store reported issues: %+v", rep.Issues)
	}
	if len(rep.Plan.Actions) != 0 {
		t.Fatalf("clean store produced actions: %+v", rep.Plan.Actions)
	}
}

func TestAuditIsReadOnly(t *testing.T) {
	dir := genStore(t, 3, 8)
	os.Remove(indexPath(dir))
	os.WriteFile(segmentPath(dir, "seg-999999.dat"), []byte("orphan"), 0o644)
	before := snapshotTree(t, dir)
	if _, err := Audit(dir); err != nil {
		t.Fatal(err)
	}
	after := snapshotTree(t, dir)
	if len(before) != len(after) {
		t.Fatalf("audit changed file set: before %d entries, after %d", len(before), len(after))
	}
	for k, v := range before {
		if after[k] != v {
			t.Fatalf("audit modified %s", k)
		}
	}
}

func TestAuditMissingIndexPlansRebuild(t *testing.T) {
	dir := genStore(t, 2, 8)
	if err := os.Remove(indexPath(dir)); err != nil {
		t.Fatal(err)
	}
	rep, err := Audit(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !hasIssue(rep, "index-missing") {
		t.Fatalf("missing index not reported: %+v", rep.Issues)
	}
	got := actionTypes(rep)
	want := []string{ActionRebuildIndex, ActionSyncManifest}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("actions = %v, want %v", got, want)
	}
}

func TestAuditCorruptSegmentIsUnrecoverable(t *testing.T) {
	dir := genStore(t, 2, 8)
	f, err := os.OpenFile(segmentPath(dir, "seg-000001.dat"), os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteAt([]byte{0xAA}, 5); err != nil {
		t.Fatal(err)
	}
	f.Close()
	rep, err := Audit(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !hasIssue(rep, "segment-checksum-mismatch") {
		t.Fatalf("checksum mismatch not reported: %+v", rep.Issues)
	}
	for _, a := range rep.Plan.Actions {
		if a.Type == ActionRebuildIndex || a.Type == ActionSyncManifest {
			t.Fatalf("plan must not rebuild around corrupt segments: %+v", rep.Plan.Actions)
		}
	}
	found := false
	for _, a := range rep.Plan.Actions {
		if a.Type == ActionUnrecoverable && a.Segment == "seg-000001.dat" {
			found = true
		}
	}
	if !found {
		t.Fatalf("no unrecoverable action for corrupt segment: %+v", rep.Plan.Actions)
	}
}

func TestAuditDetectsOrphan(t *testing.T) {
	dir := genStore(t, 2, 4)
	if err := os.WriteFile(segmentPath(dir, "seg-000007.dat"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	rep, err := Audit(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !hasIssue(rep, "segment-orphan") {
		t.Fatalf("orphan not reported: %+v", rep.Issues)
	}
	found := false
	for _, a := range rep.Plan.Actions {
		if a.Type == ActionRemoveOrphan && a.Segment == "seg-000007.dat" {
			found = true
		}
	}
	if !found {
		t.Fatalf("no remove-orphan action: %+v", rep.Plan.Actions)
	}
}

func TestAuditDetectsStaleIndex(t *testing.T) {
	dir := genStore(t, 2, 4)
	data, err := os.ReadFile(indexPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	var idx Index
	if err := json.Unmarshal(data, &idx); err != nil {
		t.Fatal(err)
	}
	delete(idx.Entries, "key-00000000")
	out, _ := json.MarshalIndent(&idx, "", "  ")
	if err := os.WriteFile(indexPath(dir), out, 0o644); err != nil {
		t.Fatal(err)
	}
	rep, err := Audit(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !hasIssue(rep, "index-hash-mismatch") {
		t.Fatalf("stale index not reported: %+v", rep.Issues)
	}
	got := actionTypes(rep)
	if len(got) != 2 || got[0] != ActionRebuildIndex || got[1] != ActionSyncManifest {
		t.Fatalf("actions = %v, want rebuild+sync", got)
	}
}

func TestAuditRejectsPlanOutInsideStore(t *testing.T) {
	dir := genStore(t, 1, 2)
	code := run([]string{"audit", "-dir", dir, "-plan-out", filepath.Join(dir, "plan.json")})
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if _, err := os.Stat(filepath.Join(dir, "plan.json")); !os.IsNotExist(err) {
		t.Fatal("plan file was written inside the store")
	}
}
