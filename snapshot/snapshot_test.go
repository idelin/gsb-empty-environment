package snapshot_test

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"

	"inbox"
	"inbox/snapshot"
)

// A crash mid-commit leaves a leftover temp file; recovery must ignore it
// and keep the last complete snapshot.
func TestLeftoverTempFileIsIgnored(t *testing.T) {
	dir, err := ioutil.TempDir("", "snapshot-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "inbox.json")

	s1, err := snapshot.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	p := inbox.NewProcessor(s1)
	ev := inbox.Event{ID: "ev-1", Payload: []byte("a")}
	if applied, err := p.Process(ev, func(tx inbox.Tx, ev inbox.Event) ([]inbox.Output, error) {
		return []inbox.Output{{Payload: []byte("x")}}, nil
	}); err != nil || !applied {
		t.Fatalf("process: applied=%v err=%v", applied, err)
	}
	s1.Close()

	// Simulate a crashed commit: partial temp file never renamed.
	if err := ioutil.WriteFile(path+".tmp", []byte(`{"Seen":{`), 0600); err != nil {
		t.Fatal(err)
	}

	s2, err := snapshot.Open(path)
	if err != nil {
		t.Fatalf("reopen with leftover tmp: %v", err)
	}
	defer s2.Close()
	pending, err := s2.Pending()
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].ID != "ev-1#0" {
		t.Fatalf("pending after recovery = %+v", pending)
	}
}
