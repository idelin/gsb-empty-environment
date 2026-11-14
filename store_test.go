package netsec_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"example.org/netsec/q0396"
)

func TestDirStoreWithAccess(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "jobs")
	store, err := netsec.NewDirStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	access := netsec.NewWithStore(store)
	ctx := context.Background()
	info, err := access.CreateJob(ctx, netsec.JobSpec{Subject: fixedSubject(), Image: testJPEG(t)})
	if err != nil {
		t.Fatal(err)
	}
	scan, err := access.Read(ctx, fixedSubject(), info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(scan.Data, []byte("GPSLatitude")) {
		t.Fatal("disk-backed scan leaked location metadata")
	}

	fileInfo, err := os.Stat(filepath.Join(dir, info.ID+".scan"))
	if err != nil {
		t.Fatal(err)
	}
	if fileInfo.Mode().Perm() != 0o600 {
		t.Fatalf("scan file mode = %o, want 600", fileInfo.Mode().Perm())
	}

	if _, err := store.Get("../../etc/passwd"); !errors.Is(err, netsec.ErrStoreNotFound) {
		t.Fatalf("path traversal Get error = %v", err)
	}
	if err := store.Put("../../etc/passwd", []byte("x")); !errors.Is(err, netsec.ErrStoreNotFound) {
		t.Fatalf("path traversal Put error = %v", err)
	}
}
