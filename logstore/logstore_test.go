package logstore_test

import (
	"context"
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"

	"inbox"
	"inbox/logstore"
	"inbox/storetest"
)

func TestCrashRecoverySuite(t *testing.T) {
	storetest.Run(t, func(dir string) (inbox.Store, error) {
		return logstore.Open(dir)
	})
}

// A crash mid-append leaves a torn tail record; recovery must truncate it
// and keep all previously committed records intact.
func TestTornTailTruncatedOnRecovery(t *testing.T) {
	dir, err := ioutil.TempDir("", "inbox-log-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	s, err := logstore.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	p := &inbox.Processor{Store: s}
	ev := inbox.Event{ID: "evt-1"}
	if err := p.Handle(context.Background(), ev, func(tx inbox.Tx) error {
		if err := tx.PutState("k", []byte("committed")); err != nil {
			return err
		}
		return tx.EnqueueOutbox(inbox.OutboxMessage{
			ID:      inbox.OutputID(ev.ID, 0),
			EventID: ev.ID,
			Topic:   "t",
		})
	}); err != nil {
		t.Fatal(err)
	}
	s.Close()

	// Simulate a crash halfway through appending the next commit record.
	wal := filepath.Join(dir, "inbox.wal")
	f, err := os.OpenFile(wal, os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte{0, 0, 0, 200, 1, 2, 3}); err != nil { // claims 200 bytes, writes 3
		t.Fatal(err)
	}
	f.Close()

	s, err = logstore.Open(dir)
	if err != nil {
		t.Fatalf("reopen with torn tail: %v", err)
	}
	defer s.Close()
	err = s.View(context.Background(), func(tx inbox.Tx) error {
		v, ok, err := tx.GetState("k")
		if err != nil {
			return err
		}
		if !ok || string(v) != "committed" {
			t.Fatalf("state = %q, want committed value", v)
		}
		msgs, err := tx.PendingOutbox()
		if err != nil {
			return err
		}
		if len(msgs) != 1 {
			t.Fatalf("pending = %d, want 1", len(msgs))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// The log must still accept new commits after truncation.
	p = &inbox.Processor{Store: s}
	ev2 := inbox.Event{ID: "evt-2"}
	if err := p.Handle(context.Background(), ev2, func(tx inbox.Tx) error {
		return tx.PutState("k2", []byte("after-recovery"))
	}); err != nil {
		t.Fatalf("commit after torn-tail recovery: %v", err)
	}
	s.Close()
	s, err = logstore.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	err = s.View(context.Background(), func(tx inbox.Tx) error {
		v, ok, _ := tx.GetState("k2")
		if !ok || string(v) != "after-recovery" {
			t.Fatalf("post-recovery commit lost: %q", v)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
