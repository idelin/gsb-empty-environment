package inbox_test

import (
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"inbox"
	"inbox/snapshot"
	"inbox/wal"
)

// storeFactory opens a fresh store instance over dir. Reopening with the
// same dir simulates a restart after a crash.
type storeFactory struct {
	name string
	open func(dir string) (inbox.Store, error)
}

// factories are the two independent persistence models under verification.
func factories() []storeFactory {
	return []storeFactory{
		{"wal", func(dir string) (inbox.Store, error) {
			return wal.Open(filepath.Join(dir, "inbox.wal"))
		}},
		{"snapshot", func(dir string) (inbox.Store, error) {
			return snapshot.Open(filepath.Join(dir, "inbox.json"))
		}},
	}
}

func tempDir(t *testing.T) (string, func()) {
	dir, err := ioutil.TempDir("", "inbox-test")
	if err != nil {
		t.Fatal(err)
	}
	return dir, func() { os.RemoveAll(dir) }
}

// countingHandler is the business effect: increment a counter and emit one
// output. If it runs twice for the same event, the counter shows it.
func countingHandler(tx inbox.Tx, ev inbox.Event) ([]inbox.Output, error) {
	raw, _ := tx.GetState("count")
	n, _ := strconv.Atoi(string(raw))
	tx.SetState("count", []byte(strconv.Itoa(n+1)))
	return []inbox.Output{{Payload: []byte("effect:" + string(ev.Payload))}}, nil
}

func readState(t *testing.T, s inbox.Store, key string) string {
	tx, err := s.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	v, _ := tx.GetState(key)
	return string(v)
}

type recordingTransport struct{ sent []inbox.Output }

func (rt *recordingTransport) Send(o inbox.Output) error {
	rt.sent = append(rt.sent, o)
	return nil
}

// 1. Duplicate delivery: the effect is applied once, exactly one output is
// enqueued, and its id is stable/deterministic.
func TestDuplicateDelivery(t *testing.T) {
	for _, f := range factories() {
		t.Run(f.name, func(t *testing.T) {
			dir, cleanup := tempDir(t)
			defer cleanup()
			s, err := f.open(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			p := inbox.NewProcessor(s)
			ev := inbox.Event{ID: "ev-1", Payload: []byte("hello")}

			applied, err := p.Process(ev, countingHandler)
			if err != nil || !applied {
				t.Fatalf("first delivery: applied=%v err=%v", applied, err)
			}
			for i := 0; i < 3; i++ {
				applied, err = p.Process(ev, countingHandler)
				if err != nil || applied {
					t.Fatalf("redelivery %d: applied=%v err=%v", i, applied, err)
				}
			}
			if got := readState(t, s, "count"); got != "1" {
				t.Fatalf("business effect count = %q, want 1", got)
			}
			pending, err := s.Pending()
			if err != nil {
				t.Fatal(err)
			}
			if len(pending) != 1 {
				t.Fatalf("pending outputs = %d, want 1", len(pending))
			}
			if want := inbox.StableOutputID("ev-1", 0); pending[0].ID != want {
				t.Fatalf("output id = %q, want stable id %q", pending[0].ID, want)
			}
		})
	}
}

// failCommitStore injects one commit failure, simulating a crash before the
// commit record became durable.
type failCommitStore struct {
	inner inbox.Store
	fail  bool
}

func (s *failCommitStore) Begin() (inbox.Tx, error) {
	tx, err := s.inner.Begin()
	if err != nil {
		return nil, err
	}
	return &failCommitTx{Tx: tx, fail: &s.fail}, nil
}
func (s *failCommitStore) Pending() ([]inbox.Output, error) { return s.inner.Pending() }
func (s *failCommitStore) MarkSent(id string) error         { return s.inner.MarkSent(id) }
func (s *failCommitStore) Close() error                     { return s.inner.Close() }

type failCommitTx struct {
	inbox.Tx
	fail *bool
}

func (t *failCommitTx) Commit() error {
	if *t.fail {
		*t.fail = false
		t.Tx.Rollback()
		return errors.New("injected commit failure")
	}
	return t.Tx.Commit()
}

// 2. Crash before commit: nothing is applied; the retried delivery commits
// the effect exactly once.
func TestCommitFailureThenRetry(t *testing.T) {
	for _, f := range factories() {
		t.Run(f.name, func(t *testing.T) {
			dir, cleanup := tempDir(t)
			defer cleanup()
			inner, err := f.open(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer inner.Close()
			s := &failCommitStore{inner: inner, fail: true}
			p := inbox.NewProcessor(s)
			ev := inbox.Event{ID: "ev-1", Payload: []byte("x")}

			if _, err := p.Process(ev, countingHandler); err == nil {
				t.Fatal("expected injected commit failure")
			}
			if got := readState(t, inner, "count"); got != "" && got != "0" {
				t.Fatalf("failed commit leaked state: count = %q", got)
			}
			applied, err := p.Process(ev, countingHandler)
			if err != nil || !applied {
				t.Fatalf("retry: applied=%v err=%v", applied, err)
			}
			if got := readState(t, inner, "count"); got != "1" {
				t.Fatalf("count = %q, want 1", got)
			}
			pending, _ := inner.Pending()
			if len(pending) != 1 {
				t.Fatalf("pending = %d, want 1", len(pending))
			}
		})
	}
}

// 3. Crash after commit but before dispatch: the committed output survives
// the restart and is dispatched exactly once.
func TestCrashAfterCommitBeforeDispatch(t *testing.T) {
	for _, f := range factories() {
		t.Run(f.name, func(t *testing.T) {
			dir, cleanup := tempDir(t)
			defer cleanup()

			s1, err := f.open(dir)
			if err != nil {
				t.Fatal(err)
			}
			p := inbox.NewProcessor(s1)
			ev := inbox.Event{ID: "ev-1", Payload: []byte("a")}
			if applied, err := p.Process(ev, countingHandler); err != nil || !applied {
				t.Fatalf("process: applied=%v err=%v", applied, err)
			}
			// Crash: abandon s1 without Close.

			s2, err := f.open(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer s2.Close()
			pending, err := s2.Pending()
			if err != nil {
				t.Fatal(err)
			}
			if len(pending) != 1 || pending[0].ID != "ev-1#0" {
				t.Fatalf("pending after restart = %+v", pending)
			}
			tr := &recordingTransport{}
			n, err := inbox.NewDispatcher(s2, tr).DispatchPending()
			if err != nil || n != 1 {
				t.Fatalf("dispatch: n=%d err=%v", n, err)
			}
			if len(tr.sent) != 1 || tr.sent[0].ID != "ev-1#0" {
				t.Fatalf("sent = %+v", tr.sent)
			}
			if pending, _ := s2.Pending(); len(pending) != 0 {
				t.Fatalf("pending after dispatch = %d", len(pending))
			}
		})
	}
}

var errSimulatedCrash = errors.New("simulated crash after send")

// 4. Ack lost after a successful send: the output is resent with the SAME
// stable id; the receiver dedups it, so the business effect happens once
// even though the network send happened twice.
func TestAckLostResendWithStableID(t *testing.T) {
	for _, f := range factories() {
		t.Run(f.name, func(t *testing.T) {
			dir, cleanup := tempDir(t)
			defer cleanup()

			s1, err := f.open(dir)
			if err != nil {
				t.Fatal(err)
			}
			p := inbox.NewProcessor(s1)
			ev := inbox.Event{ID: "ev-1", Payload: []byte("b")}
			if applied, err := p.Process(ev, countingHandler); err != nil || !applied {
				t.Fatalf("process: applied=%v err=%v", applied, err)
			}

			tr := &recordingTransport{}
			d1 := inbox.NewDispatcher(s1, tr)
			d1.AfterSend = func(o inbox.Output) error { return errSimulatedCrash }
			if _, err := d1.DispatchPending(); err != errSimulatedCrash {
				t.Fatalf("dispatch err = %v, want simulated crash", err)
			}
			if len(tr.sent) != 1 {
				t.Fatalf("sent before crash = %d, want 1", len(tr.sent))
			}
			// Crash between Send and MarkSent: abandon s1 without Close.

			s2, err := f.open(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer s2.Close()
			n, err := inbox.NewDispatcher(s2, tr).DispatchPending()
			if err != nil || n != 1 {
				t.Fatalf("redispatch: n=%d err=%v", n, err)
			}
			if len(tr.sent) != 2 {
				t.Fatalf("total sends = %d, want 2 (at-least-once)", len(tr.sent))
			}
			if tr.sent[0].ID != tr.sent[1].ID {
				t.Fatalf("resent id %q != original id %q: id not stable",
					tr.sent[1].ID, tr.sent[0].ID)
			}

			recv := inbox.NewReceiver()
			for _, o := range tr.sent {
				recv.Receive(o)
			}
			if recv.Applied != 1 {
				t.Fatalf("receiver applied %d effects, want 1", recv.Applied)
			}
			if got := readState(t, s2, "count"); got != "1" {
				t.Fatalf("business effect count = %q, want 1", got)
			}
		})
	}
}

// 5. Redelivery storm: many duplicates of many events; each effect and each
// output id appears exactly once.
func TestRedeliveryStorm(t *testing.T) {
	for _, f := range factories() {
		t.Run(f.name, func(t *testing.T) {
			dir, cleanup := tempDir(t)
			defer cleanup()
			s, err := f.open(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			p := inbox.NewProcessor(s)

			const unique = 20
			const copies = 5
			appliedCount := 0
			for round := 0; round < copies; round++ {
				for i := 0; i < unique; i++ {
					ev := inbox.Event{ID: fmt.Sprintf("ev-%d", i), Payload: []byte{byte(i)}}
					applied, err := p.Process(ev, countingHandler)
					if err != nil {
						t.Fatal(err)
					}
					if applied {
						appliedCount++
					}
				}
			}
			if appliedCount != unique {
				t.Fatalf("applied = %d, want %d", appliedCount, unique)
			}
			if got := readState(t, s, "count"); got != strconv.Itoa(unique) {
				t.Fatalf("count = %q, want %d", got, unique)
			}
			pending, _ := s.Pending()
			if len(pending) != unique {
				t.Fatalf("pending = %d, want %d", len(pending), unique)
			}
			ids := make(map[string]bool)
			for _, o := range pending {
				if ids[o.ID] {
					t.Fatalf("duplicate output id %q", o.ID)
				}
				ids[o.ID] = true
			}

			tr := &recordingTransport{}
			if _, err := inbox.NewDispatcher(s, tr).DispatchPending(); err != nil {
				t.Fatal(err)
			}
			recv := inbox.NewReceiver()
			for _, o := range tr.sent {
				recv.Receive(o)
			}
			if recv.Applied != unique {
				t.Fatalf("receiver applied %d, want %d", recv.Applied, unique)
			}
		})
	}
}

// 6. Handler failure: nothing is recorded, so the event can be retried.
func TestHandlerErrorRollsBack(t *testing.T) {
	for _, f := range factories() {
		t.Run(f.name, func(t *testing.T) {
			dir, cleanup := tempDir(t)
			defer cleanup()
			s, err := f.open(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			p := inbox.NewProcessor(s)
			ev := inbox.Event{ID: "ev-1", Payload: []byte("z")}

			boom := errors.New("boom")
			failing := func(tx inbox.Tx, ev inbox.Event) ([]inbox.Output, error) {
				tx.SetState("count", []byte("99"))
				return nil, boom
			}
			if _, err := p.Process(ev, failing); err != boom {
				t.Fatalf("err = %v, want boom", err)
			}
			if got := readState(t, s, "count"); got != "" {
				t.Fatalf("rolled-back state leaked: count = %q", got)
			}
			applied, err := p.Process(ev, countingHandler)
			if err != nil || !applied {
				t.Fatalf("retry after handler error: applied=%v err=%v", applied, err)
			}
			if got := readState(t, s, "count"); got != "1" {
				t.Fatalf("count = %q, want 1", got)
			}
		})
	}
}
