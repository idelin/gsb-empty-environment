package batch_test

import (
	"context"
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"

	"batchrun/batch"
)

// The same freeze/resume guarantees hold on a real directory.
func TestOSFSResume(t *testing.T) {
	root, err := ioutil.TempDir("", "batchrun")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	fs := batch.OSFS{Root: root}
	for p, c := range map[string]string{"a": "aa", "b": "bb"} {
		if err := fs.WriteFile(filepath.ToSlash(filepath.Join("src", p)), []byte(c)); err != nil {
			t.Fatal(err)
		}
	}
	var log []string
	o := opts(&log, map[string][]string{"b": {"a"}})
	o.AfterPersist = func(seq int) error {
		if seq == 2 {
			return errCrash
		}
		return nil
	}
	r, err := batch.Open(fs, o)
	if err != nil {
		t.Fatal(err)
	}
	r.Run(context.Background())

	o.AfterPersist = nil
	r, err = batch.Open(fs, o)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	data, err := fs.ReadFile("out/b")
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "BB|a=AA" {
		t.Fatalf("out/b: %q", data)
	}
	// Checkpoint must be the only file in the state dir (atomic tmp+rename).
	stateFiles, _ := fs.List("state")
	if len(stateFiles) != 1 || stateFiles[0] != "checkpoint.json" {
		t.Fatalf("state dir: %v", stateFiles)
	}
}
