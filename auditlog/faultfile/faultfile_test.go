package faultfile_test

import (
	"os"
	"path/filepath"
	"testing"

	"example.org/netsec/uid1279/auditlog/faultfile"
)

func TestTornSyncExposesOnlyDurablePrefix(t *testing.T) {
	dir := t.TempDir()
	controller, opener := faultfile.New()

	path := filepath.Join(dir, "segment.part")
	file, err := opener.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	fault := controller.Last()

	first := []byte("durable-prefix")
	if _, err := file.Write(first); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := file.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	second := []byte("-torn-suffix")
	if _, err := file.Write(second); err != nil {
		t.Fatalf("Write: %v", err)
	}
	controller.ArmSyncLimit(int64(len(first)) + 5)
	if err := file.Sync(); err == nil {
		t.Fatal("expected torn sync error")
	}
	if err := fault.Crash(); err != nil {
		t.Fatalf("Crash: %v", err)
	}

	persisted, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	want := string(first) + "-torn"
	if string(persisted) != want {
		t.Fatalf("persisted = %q, want %q", persisted, want)
	}
}

func TestCrashDiscardsUnsyncedBytes(t *testing.T) {
	dir := t.TempDir()
	controller, opener := faultfile.New()

	path := filepath.Join(dir, "segment.part")
	file, err := opener.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	fault := controller.Last()
	if _, err := file.Write([]byte("saved")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := file.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if _, err := file.Write([]byte("lost")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := fault.Crash(); err != nil {
		t.Fatalf("Crash: %v", err)
	}

	persisted, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(persisted) != "saved" {
		t.Fatalf("persisted = %q, want %q", persisted, "saved")
	}
}
