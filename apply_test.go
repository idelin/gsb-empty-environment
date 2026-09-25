package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writePlan(t *testing.T, rep *Report) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "plan.json")
	data, err := jsonMarshal(rep.Plan)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func jsonMarshal(v any) ([]byte, error) {
	return json.MarshalIndent(v, "", "  ")
}

func TestApplyRebuildsMissingIndex(t *testing.T) {
	dir := genStore(t, 3, 8)
	origIdx, err := os.ReadFile(indexPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(indexPath(dir)); err != nil {
		t.Fatal(err)
	}
	rep, err := Audit(dir)
	if err != nil {
		t.Fatal(err)
	}
	planPath := writePlan(t, rep)
	var out bytes.Buffer
	if err := Apply(dir, planPath, &out); err != nil {
		t.Fatalf("apply failed: %v\n%s", err, out.String())
	}
	newIdx, err := os.ReadFile(indexPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(origIdx, newIdx) {
		t.Fatal("rebuilt index differs from the original")
	}
	rep2, err := Audit(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !rep2.Clean() {
		t.Fatalf("store not clean after apply: %+v", rep2.Issues)
	}
	if _, err := os.Stat(journalPath(dir)); !os.IsNotExist(err) {
		t.Fatal("journal left behind after successful apply")
	}
}

func TestApplyAbortsOnStateMismatch(t *testing.T) {
	dir := genStore(t, 2, 4)
	if err := os.Remove(indexPath(dir)); err != nil {
		t.Fatal(err)
	}
	rep, err := Audit(dir)
	if err != nil {
		t.Fatal(err)
	}
	planPath := writePlan(t, rep)
	f, err := os.OpenFile(segmentPath(dir, "seg-000001.dat"), os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteAt([]byte{0x01}, 0); err != nil {
		t.Fatal(err)
	}
	f.Close()
	err = Apply(dir, planPath, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "state summary mismatch") {
		t.Fatalf("expected state summary mismatch, got %v", err)
	}
	if _, err := os.Stat(indexPath(dir)); !os.IsNotExist(err) {
		t.Fatal("apply touched the store despite summary mismatch")
	}
	if _, err := os.Stat(journalPath(dir)); !os.IsNotExist(err) {
		t.Fatal("journal created despite aborted apply")
	}
}

func TestApplyCrashAndResume(t *testing.T) {
	dir := genStore(t, 2, 8)
	if err := os.Remove(indexPath(dir)); err != nil {
		t.Fatal(err)
	}
	rep, err := Audit(dir)
	if err != nil {
		t.Fatal(err)
	}
	planPath := writePlan(t, rep)

	oldCrash := crashFunc
	defer func() { crashFunc = oldCrash }()
	crashFunc = func() { panic("injected crash") }
	t.Setenv("STORETOOL_FAILPOINT", "action-1:post-rename")

	panicked := false
	func() {
		defer func() {
			if r := recover(); r != nil {
				panicked = true
			}
		}()
		_ = Apply(dir, planPath, io.Discard)
	}()
	if !panicked {
		t.Fatal("fault injection did not fire")
	}
	if _, err := os.Stat(indexPath(dir)); err != nil {
		t.Fatal("index should exist: crash was after rename")
	}
	jdata, err := os.ReadFile(journalPath(dir))
	if err != nil {
		t.Fatal("journal missing after crash")
	}
	if strings.Contains(string(jdata), "action-1") {
		t.Fatal("journal recorded a step that crashed before completion")
	}

	t.Setenv("STORETOOL_FAILPOINT", "")
	var out bytes.Buffer
	if err := Apply(dir, planPath, &out); err != nil {
		t.Fatalf("resume failed: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "resuming") {
		t.Fatalf("resume not detected in output:\n%s", out.String())
	}
	rep2, err := Audit(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !rep2.Clean() {
		t.Fatalf("store not clean after resume: %+v", rep2.Issues)
	}
}

func TestApplyCrashBeforeRenameLeavesNoIndex(t *testing.T) {
	dir := genStore(t, 2, 4)
	if err := os.Remove(indexPath(dir)); err != nil {
		t.Fatal(err)
	}
	rep, err := Audit(dir)
	if err != nil {
		t.Fatal(err)
	}
	planPath := writePlan(t, rep)

	oldCrash := crashFunc
	defer func() { crashFunc = oldCrash }()
	crashFunc = func() { panic("injected crash") }
	t.Setenv("STORETOOL_FAILPOINT", "action-1:pre-rename")

	func() {
		defer func() { recover() }()
		_ = Apply(dir, planPath, io.Discard)
	}()
	if _, err := os.Stat(indexPath(dir)); !os.IsNotExist(err) {
		t.Fatal("index must not exist when crash happens before rename")
	}
	t.Setenv("STORETOOL_FAILPOINT", "")
	if err := Apply(dir, planPath, io.Discard); err != nil {
		t.Fatalf("resume after pre-rename crash failed: %v", err)
	}
	rep2, err := Audit(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !rep2.Clean() {
		t.Fatalf("store not clean after resume: %+v", rep2.Issues)
	}
}

func TestApplyNeverFabricatesSegmentContent(t *testing.T) {
	dir := genStore(t, 2, 4)
	segPath := segmentPath(dir, "seg-000002.dat")
	f, err := os.OpenFile(segPath, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteAt([]byte{0xEE}, 3); err != nil {
		t.Fatal(err)
	}
	f.Close()
	corrupted, err := os.ReadFile(segPath)
	if err != nil {
		t.Fatal(err)
	}
	rep, err := Audit(dir)
	if err != nil {
		t.Fatal(err)
	}
	planPath := writePlan(t, rep)
	err = Apply(dir, planPath, io.Discard)
	if !errors.Is(err, errUnrecoverable) {
		t.Fatalf("expected errUnrecoverable, got %v", err)
	}
	after, err := os.ReadFile(segPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(corrupted, after) {
		t.Fatal("apply modified a corrupt segment instead of reporting it")
	}
}

func TestApplyRemovesOrphan(t *testing.T) {
	dir := genStore(t, 2, 4)
	orphan := segmentPath(dir, "seg-000009.dat")
	if err := os.WriteFile(orphan, []byte("orphan"), 0o644); err != nil {
		t.Fatal(err)
	}
	rep, err := Audit(dir)
	if err != nil {
		t.Fatal(err)
	}
	planPath := writePlan(t, rep)
	if err := Apply(dir, planPath, io.Discard); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatal("orphan segment still present after apply")
	}
	rep2, err := Audit(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !rep2.Clean() {
		t.Fatalf("store not clean after orphan removal: %+v", rep2.Issues)
	}
}

func TestApplyRejectsForeignJournal(t *testing.T) {
	dir := genStore(t, 2, 4)
	if err := os.Remove(indexPath(dir)); err != nil {
		t.Fatal(err)
	}
	rep, err := Audit(dir)
	if err != nil {
		t.Fatal(err)
	}
	planPath := writePlan(t, rep)
	journal := Journal{PlanSummary: "deadbeef", Completed: []string{"action-1"}}
	data, _ := jsonMarshal(journal)
	if err := os.WriteFile(journalPath(dir), data, 0o644); err != nil {
		t.Fatal(err)
	}
	err = Apply(dir, planPath, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "different plan") {
		t.Fatalf("expected foreign-journal error, got %v", err)
	}
}
