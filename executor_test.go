package logtrunc

import (
	"errors"
	"io/ioutil"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func tempStorePath(t *testing.T) (string, func()) {
	t.Helper()
	dir, err := ioutil.TempDir("", "logtrunc")
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, "requests.jsonl"), func() { os.RemoveAll(dir) }
}

// A failing deletion leaves the request pending; later retries finish it.
func TestDeletionFailureIsRetried(t *testing.T) {
	path, cleanup := tempStorePath(t)
	defer cleanup()
	store, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}

	failures := 2
	var deleted []string
	ex := NewExecutor(store, func(id string) error {
		if failures > 0 {
			failures--
			return errors.New("injected deletion failure")
		}
		deleted = append(deleted, id)
		return nil
	})

	req := Request{ID: "r1", SegmentIDs: []string{"s0", "s1"}}
	if err := ex.Submit(req); err != nil {
		t.Fatal(err)
	}
	// The immediate attempt consumed one injected failure.
	if got := store.Pending(); len(got) != 1 {
		t.Fatalf("pending = %v, want request still pending after failure", got)
	}
	ex.ProcessPending() // consumes the second failure
	if got := store.Pending(); len(got) != 1 {
		t.Fatalf("pending = %v, want request still pending after second failure", got)
	}
	ex.ProcessPending() // now succeeds
	if got := store.Pending(); len(got) != 0 {
		t.Fatalf("pending = %v, want empty after successful retry", got)
	}
	if !reflect.DeepEqual(deleted, []string{"s0", "s1"}) {
		t.Fatalf("deleted = %v, want [s0 s1]", deleted)
	}
}

// A crash must not forget unfinished requests: reopening the journal
// replays them and a fresh executor completes the work.
func TestRecoveryRemembersPendingRequests(t *testing.T) {
	path, cleanup := tempStorePath(t)
	defer cleanup()

	store1, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	crash := NewExecutor(store1, func(id string) error {
		return errors.New("deleter unavailable")
	})
	if err := crash.Submit(Request{ID: "r1", SegmentIDs: []string{"s0"}}); err != nil {
		t.Fatal(err)
	}
	if err := crash.Submit(Request{ID: "r2", SegmentIDs: []string{"s1", "s2"}}); err != nil {
		t.Fatal(err)
	}

	// Simulate crash + restart: new store and executor over the same file.
	store2, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	pending := store2.Pending()
	if len(pending) != 2 || pending[0].ID != "r1" || pending[1].ID != "r2" {
		t.Fatalf("pending after recovery = %v, want r1 and r2", pending)
	}

	var deleted []string
	recovered := NewExecutor(store2, func(id string) error {
		deleted = append(deleted, id)
		return nil
	})
	recovered.ProcessPending()
	if !reflect.DeepEqual(deleted, []string{"s0", "s1", "s2"}) {
		t.Fatalf("deleted after recovery = %v, want [s0 s1 s2]", deleted)
	}

	// The completions are durable too: a second restart sees no work left.
	store3, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := store3.Pending(); len(got) != 0 {
		t.Fatalf("pending after second restart = %v, want empty", got)
	}
}

// Submit persists before attempting, so even a permanently failing
// deleter cannot lose the request.
func TestSubmitPersistsBeforeAttempt(t *testing.T) {
	path, cleanup := tempStorePath(t)
	defer cleanup()
	store, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	ex := NewExecutor(store, func(id string) error { return errors.New("always fails") })
	if err := ex.Submit(Request{ID: "r1", SegmentIDs: []string{"s0"}}); err != nil {
		t.Fatal(err)
	}

	reopened, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := reopened.Pending(); len(got) != 1 || got[0].ID != "r1" {
		t.Fatalf("pending = %v, want r1 preserved despite failed attempt", got)
	}
}
