package store

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func newStore(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := Gen(dir); err != nil {
		t.Fatal(err)
	}
	return dir
}

// snapshot hashes every file in dir so tests can prove the audit
// (or a failed apply) changed nothing.
func snapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		sum, _, err := sha256File(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		out[e.Name()] = sum
	}
	return out
}

func mustAudit(t *testing.T, dir string) (*Report, *Plan) {
	t.Helper()
	rep, plan, err := Audit(dir)
	if err != nil {
		t.Fatal(err)
	}
	return rep, plan
}

func TestAuditConsistentStoreIsNoopAndReadOnly(t *testing.T) {
	dir := newStore(t)
	before := snapshot(t, dir)

	rep, plan := mustAudit(t, dir)
	if !rep.ManifestOK || !rep.IndexConsistent {
		t.Fatalf("expected consistent store, got %+v", rep)
	}
	if !plan.Repairable || len(plan.Steps) != 1 || plan.Steps[0].Op != OpNoop {
		t.Fatalf("expected single noop step, got %+v", plan.Steps)
	}

	after := snapshot(t, dir)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("audit modified the store:\nbefore=%v\nafter=%v", before, after)
	}
}

func TestMissingIndexIsRebuiltByteIdentical(t *testing.T) {
	dir := newStore(t)
	orig, err := os.ReadFile(filepath.Join(dir, IndexName))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, IndexName)); err != nil {
		t.Fatal(err)
	}

	rep, plan := mustAudit(t, dir)
	if rep.IndexConsistent || !plan.Repairable {
		t.Fatalf("expected repairable plan for missing index")
	}
	if len(plan.Steps) != 1 || plan.Steps[0].Op != OpRebuildIndex {
		t.Fatalf("expected rebuild_index step, got %+v", plan.Steps)
	}
	if err := Apply(dir, plan); err != nil {
		t.Fatal(err)
	}
	rebuilt, err := os.ReadFile(filepath.Join(dir, IndexName))
	if err != nil {
		t.Fatal(err)
	}
	if string(rebuilt) != string(orig) {
		t.Fatal("rebuilt index differs from original")
	}
	// Journal must be cleaned up after a complete apply.
	if _, err := os.Stat(filepath.Join(dir, JournalName)); !os.IsNotExist(err) {
		t.Fatal("journal left behind after successful apply")
	}
}

func TestCorruptSegmentIsUnrecoverableAndUntouched(t *testing.T) {
	dir := newStore(t)
	seg := filepath.Join(dir, "seg-0001.dat")
	data, _ := os.ReadFile(seg)
	orig := append([]byte(nil), data...)
	data[len(data)/2] ^= 0xFF
	if err := os.WriteFile(seg, data, 0o644); err != nil {
		t.Fatal(err)
	}

	rep, plan := mustAudit(t, dir)
	if plan.Repairable {
		t.Fatal("corrupt segment must not be repairable")
	}
	if plan.Steps[0].Op != OpUnrecoverable {
		t.Fatalf("expected unrecoverable step, got %+v", plan.Steps)
	}
	for _, s := range rep.Segments {
		if s.Name == "seg-0001.dat" && s.DigestOK {
			t.Fatal("corruption not detected")
		}
	}
	if err := Apply(dir, plan); err == nil {
		t.Fatal("apply must refuse an unrecoverable plan")
	}
	// The corrupt bytes must be left exactly as found: never fabricated.
	now, _ := os.ReadFile(seg)
	if !reflect.DeepEqual(now, data) || reflect.DeepEqual(now, orig) {
		t.Fatal("segment content was modified")
	}
}

func TestApplyAbortsWhenStateChangedSinceAudit(t *testing.T) {
	dir := newStore(t)
	os.Remove(filepath.Join(dir, IndexName))
	_, plan := mustAudit(t, dir)

	// Store changes after the audit: a third segment appears.
	extra := encodeRecord([]byte("late"), []byte("x"))
	if err := os.WriteFile(filepath.Join(dir, "seg-0003.dat"), extra, 0o644); err != nil {
		t.Fatal(err)
	}
	err := Apply(dir, plan)
	if err == nil {
		t.Fatal("apply must abort when the live state no longer matches the audited summary")
	}
	if _, serr := os.Stat(filepath.Join(dir, IndexName)); !os.IsNotExist(serr) {
		t.Fatal("apply wrote files despite summary mismatch")
	}
}

func TestResumeAfterCrashAtEveryFaultPoint(t *testing.T) {
	points := []FaultPoint{FaultBeforeStep, FaultBeforeRename, FaultAfterRename, FaultAfterJournal}
	for _, point := range points {
		t.Run(string(point), func(t *testing.T) {
			dir := newStore(t)
			orig, _ := os.ReadFile(filepath.Join(dir, IndexName))
			os.Remove(filepath.Join(dir, IndexName))
			_, plan := mustAudit(t, dir)

			fired := false
			FaultHook = func(p FaultPoint, stepID string) error {
				if !fired && p == point {
					fired = true
					return &InjectedFault{Point: p, StepID: stepID}
				}
				return nil
			}
			err := Apply(dir, plan)
			FaultHook = nil
			var injected *InjectedFault
			if !errors.As(err, &injected) {
				t.Fatalf("expected injected fault, got %v", err)
			}

			// "Reboot": resume with the same plan.
			if err := Apply(dir, plan); err != nil {
				t.Fatalf("resume failed: %v", err)
			}
			rebuilt, err := os.ReadFile(filepath.Join(dir, IndexName))
			if err != nil {
				t.Fatal(err)
			}
			if string(rebuilt) != string(orig) {
				t.Fatal("resumed apply produced wrong index")
			}
			// No temp files or journals may survive a finished apply.
			ents, _ := os.ReadDir(dir)
			for _, e := range ents {
				if e.Name() == JournalName || e.Name()[:1] == "." && e.Name() != JournalName {
					t.Fatalf("leftover file after resume: %s", e.Name())
				}
			}
		})
	}
}

func TestResumeRejectsForeignPlan(t *testing.T) {
	dir := newStore(t)
	os.Remove(filepath.Join(dir, IndexName))
	_, plan := mustAudit(t, dir)

	// Crash after the journal's first write, then try to resume with a
	// different plan (e.g. produced by a later audit of another state).
	FaultHook = func(p FaultPoint, stepID string) error {
		if p == FaultAfterJournal {
			return &InjectedFault{Point: p, StepID: stepID}
		}
		return nil
	}
	_ = Apply(dir, plan)
	FaultHook = nil

	other := *plan
	other.ID = "deadbeefcafe0000"
	if err := Apply(dir, &other); err == nil {
		t.Fatal("resume with a foreign plan must be rejected")
	}
	if err := Apply(dir, plan); err != nil {
		t.Fatalf("resume with the original plan must succeed: %v", err)
	}
}

func TestTamperedIndexEntryDetectedAndRepaired(t *testing.T) {
	dir := newStore(t)
	idxPath := filepath.Join(dir, IndexName)
	data, _ := os.ReadFile(idxPath)
	var idx Index
	if err := jsonUnmarshal(data, &idx); err != nil {
		t.Fatal(err)
	}
	e := idx.Entries["alpha"]
	e.Offset = e.Offset + 1 // now points mid-record
	idx.Entries["alpha"] = e
	tampered, _ := marshalJSON(&idx)
	if err := os.WriteFile(idxPath, tampered, 0o644); err != nil {
		t.Fatal(err)
	}

	rep, plan := mustAudit(t, dir)
	if rep.IndexConsistent {
		t.Fatal("tampered index not detected")
	}
	if !plan.Repairable || plan.Steps[0].Op != OpRebuildIndex {
		t.Fatalf("expected rebuild plan, got %+v", plan.Steps)
	}
	if err := Apply(dir, plan); err != nil {
		t.Fatal(err)
	}
	_, plan2 := mustAudit(t, dir)
	if plan2.Steps[0].Op != OpNoop {
		t.Fatal("store still inconsistent after repair")
	}
}

func TestOrphanSegmentReportedButNotDeleted(t *testing.T) {
	dir := newStore(t)
	orphan := filepath.Join(dir, "seg-9999.dat")
	if err := os.WriteFile(orphan, encodeRecord([]byte("x"), []byte("y")), 0o644); err != nil {
		t.Fatal(err)
	}
	rep, plan := mustAudit(t, dir)
	if len(rep.OrphanSegments) != 1 || rep.OrphanSegments[0] != "seg-9999.dat" {
		t.Fatalf("orphan not reported: %+v", rep.OrphanSegments)
	}
	if plan.Steps[0].Op != OpNoop {
		t.Fatal("orphans are report-only; plan should stay noop")
	}
	if _, err := os.Stat(orphan); err != nil {
		t.Fatal("audit must never delete files")
	}
}

func TestMissingManifestIsUnrecoverable(t *testing.T) {
	dir := newStore(t)
	if err := os.Remove(filepath.Join(dir, ManifestName)); err != nil {
		t.Fatal(err)
	}
	_, plan := mustAudit(t, dir)
	if plan.Repairable || plan.Steps[0].Op != OpUnrecoverable {
		t.Fatalf("missing manifest must be unrecoverable, got %+v", plan.Steps)
	}
	if err := Apply(dir, plan); err == nil {
		t.Fatal("apply must refuse")
	}
}
