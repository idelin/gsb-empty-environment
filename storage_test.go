package netsec

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFileStorageIsolatesScopes(t *testing.T) {
	store, err := NewFileStorage(filepath.Join(t.TempDir(), "root"))
	if err != nil {
		t.Fatalf("NewFileStorage: %v", err)
	}
	alice := Scope{Queue: "queue-a", Submitter: "alice", JobID: "job-a"}
	bob := Scope{Queue: "queue-a", Submitter: "bob", JobID: "job-b"}
	if err := store.WriteJob(context.Background(), alice, "doc.txt", []byte("alice")); err != nil {
		t.Fatalf("WriteJob: %v", err)
	}
	got, err := store.ReadJob(context.Background(), alice, "doc.txt")
	if err != nil || string(got) != "alice" {
		t.Fatalf("ReadJob = %q, %v", got, err)
	}
	if _, err := store.ReadJob(context.Background(), bob, "doc.txt"); err == nil {
		t.Fatal("read through another job scope succeeded")
	}
	if err := store.WriteJob(context.Background(), alice, "doc.txt", []byte("overwrite")); !errors.Is(err, ErrJobExists) {
		t.Fatalf("overwrite error = %v, want ErrJobExists", err)
	}

	dir, err := store.JobDirectory(alice)
	if err != nil {
		t.Fatalf("JobDirectory: %v", err)
	}
	rel, err := filepath.Rel(store.Root(), dir)
	if err != nil || rel == "." || filepath.IsAbs(rel) {
		t.Fatalf("job dir = %q, rel = %q, %v", dir, rel, err)
	}
	if strings.Contains(dir, "alice") {
		t.Fatalf("physical job dir leaks or aliases the logical scope: %q", dir)
	}
	if !strings.HasPrefix(filepath.Base(filepath.Dir(dir)), storageComponent("alice")) {
		t.Fatalf("submitter directory is not the hashed scope: %q", dir)
	}
	if err := store.DeleteJob(context.Background(), alice); err != nil {
		t.Fatalf("DeleteJob: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("job directory still exists: %v", err)
	}
}

func TestStorageRejectsUnsafeDocumentName(t *testing.T) {
	store, err := NewFileStorage(filepath.Join(t.TempDir(), "root"))
	if err != nil {
		t.Fatalf("NewFileStorage: %v", err)
	}
	scope := Scope{Queue: "q", Submitter: "u", JobID: "j"}
	if err := store.WriteJob(context.Background(), scope, "../victim.txt", []byte("x")); err == nil {
		t.Fatal("unsafe document name was written")
	}
	if _, err := store.ReadJob(context.Background(), scope, "../victim.txt"); err == nil {
		t.Fatal("unsafe document name was read")
	}
}

func TestMemoryStorageIsolatesScopes(t *testing.T) {
	store := NewMemoryStorage()
	alice := Scope{Queue: "queue-a", Submitter: "alice", JobID: "job-a"}
	bob := Scope{Queue: "queue-a", Submitter: "bob", JobID: "job-b"}
	if err := store.WriteJob(context.Background(), alice, "doc.txt", []byte("alice")); err != nil {
		t.Fatalf("WriteJob: %v", err)
	}
	got, err := store.ReadJob(context.Background(), alice, "doc.txt")
	if err != nil || string(got) != "alice" {
		t.Fatalf("ReadJob = %q, %v", got, err)
	}
	if _, err := store.ReadJob(context.Background(), bob, "doc.txt"); err == nil {
		t.Fatal("read through another job scope succeeded")
	}
}

func TestStorageScopeTraversalRejected(t *testing.T) {
	store, err := NewFileStorage(filepath.Join(t.TempDir(), "root"))
	if err != nil {
		t.Fatalf("NewFileStorage: %v", err)
	}
	badScopes := []Scope{
		{Queue: "..", Submitter: "alice", JobID: "job-a"},
		{Queue: "q", Submitter: "../bob", JobID: "job-a"},
		{Queue: "q", Submitter: "alice", JobID: "a/b"},
	}
	for _, scope := range badScopes {
		if _, err := store.JobDirectory(scope); err == nil {
			t.Errorf("JobDirectory accepted %+v", scope)
		}
	}
}
