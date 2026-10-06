package snapshotstore_test

import (
	"context"
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"

	"inbox"
	"inbox/snapshotstore"
	"inbox/storetest"
)

func TestCrashRecoverySuite(t *testing.T) {
	storetest.Run(t, func(dir string) (inbox.Store, error) {
		return snapshotstore.Open(dir)
	})
}

// A leftover .tmp file from a crash mid-commit must be ignored: the last
// fully renamed snapshot is the durable state.
func TestStaleTempFileIgnoredOnRecovery(t *testing.T) {
	dir, err := ioutil.TempDir("", "inbox-snapshot-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	s, err := snapshotstore.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	p := &inbox.Processor{Store: s}
	ev := inbox.Event{ID: "evt-1"}
	if err := p.Handle(context.Background(), ev, func(tx inbox.Tx) error {
		return tx.PutState("k", []byte("committed"))
	}); err != nil {
		t.Fatal(err)
	}
	s.Close()

	// Simulate a crash that left a half-written tmp file behind.
	if err := ioutil.WriteFile(filepath.Join(dir, "state.json.tmp"), []byte(`{"Dedup`), 0600); err != nil {
		t.Fatal(err)
	}

	s, err = snapshotstore.Open(dir)
	if err != nil {
		t.Fatalf("reopen with stale tmp: %v", err)
	}
	defer s.Close()
	err = s.View(context.Background(), func(tx inbox.Tx) error {
		v, ok, err := tx.GetState("k")
		if err != nil {
			return err
		}
		if !ok || string(v) != "committed" {
			t.Fatalf("state = %q, want committed snapshot", v)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
