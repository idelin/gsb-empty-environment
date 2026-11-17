package netsec

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
)

func TestThreadsShareOneImmutableChain(t *testing.T) {
	h := NewHost(10, 20, 30)
	if err := h.Install(
		Filter{Rules: []Rule{DenyRule(1, 13)}},
		Filter{Rules: []Rule{AllowRule(1)}},
	); err != nil {
		t.Fatal(err)
	}

	shared := h.Chain()
	for _, id := range h.ThreadIDs() {
		threadChain, err := h.ThreadChain(id)
		if err != nil {
			t.Fatal(err)
		}
		if threadChain != shared {
			t.Fatalf("thread %d does not share the chain pointer", id)
		}
	}
	if got := shared.Evaluate(1, [6]uint64{}); got != (Decision{Action: ActionDeny, Data: 13}) {
		t.Fatalf("shared chain decision = %s", got)
	}
}

func TestNewThreadInheritsCurrentChain(t *testing.T) {
	h := NewHost(1)
	if err := h.AddThread(2); err != nil {
		t.Fatal(err)
	}
	if err := h.Install(Filter{Rules: []Rule{DenyRule(1, 13)}}); err != nil {
		t.Fatal(err)
	}
	if err := h.AddThread(3); err != nil {
		t.Fatal(err)
	}
	shared := h.Chain()
	for _, id := range []ThreadID{1, 2, 3} {
		chain, err := h.ThreadChain(id)
		if err != nil {
			t.Fatal(err)
		}
		if chain != shared {
			t.Fatalf("thread %d inherited a different chain", id)
		}
	}
}

func TestSynchronizedInstallFailureLeavesNoPartialChain(t *testing.T) {
	ids := []ThreadID{10, 20, 30, 40, 50, 60}
	syscalls := []uint32{0, 1, 2, 3, 4, 5}
	domains := [6][]uint64{
		{0, 1, 2, 7},
		{0, 1, 2},
		{0, 1},
		{0, 1},
		{0, 1},
		{0, 1},
	}
	initial := Filter{
		Rules: []Rule{
			DenyRule(1, 13),
			NotifyRule(2, 1),
		},
		Default: ActionAllow,
	}
	addition := Filter{
		Rules: []Rule{
			AllowRule(1),
			DenyRule(2, 22),
			TerminateRule(3, 9),
		},
		Default: ActionAllow,
	}

	for failAt := 0; failAt <= len(ids); failAt++ {
		t.Run(fmt.Sprintf("fail-at-%d", failAt), func(t *testing.T) {
			h := NewHost(ids...)
			if err := h.Install(initial); err != nil {
				t.Fatal(err)
			}
			before := h.Chain()

			var applied []ThreadID
			var rolledBack []ThreadID
			h.SetInstallFunc(func(id ThreadID, previous, next *Chain) error {
				if previous != before {
					t.Fatalf("thread %d hook saw wrong previous chain", id)
				}
				if next.Len() != before.Len()+1 {
					t.Fatalf("thread %d hook saw wrong next chain", id)
				}
				if len(applied) == failAt {
					return errors.New("injected install failure")
				}
				applied = append(applied, id)
				return nil
			})
			h.SetRollbackFunc(func(id ThreadID, previous, next *Chain) error {
				rolledBack = append(rolledBack, id)
				return nil
			})

			err := h.Install(addition)
			if failAt == len(ids) {
				if err != nil {
					t.Fatalf("expected success: %v", err)
				}
			} else {
				if !errors.Is(err, ErrInstallFailed) {
					t.Fatalf("expected ErrInstallFailed, got %v", err)
				}
				if len(applied) != failAt {
					t.Fatalf("applied %d threads, want %d", len(applied), failAt)
				}
				if len(rolledBack) != failAt {
					t.Fatalf("rolled back %d threads, want %d", len(rolledBack), failAt)
				}
				for i := range rolledBack {
					want := applied[len(applied)-1-i]
					if rolledBack[i] != want {
						t.Fatalf("rollback order = %v, want reverse of %v", rolledBack, applied)
					}
				}
			}

			expectedChain := h.Chain()
			if failAt == len(ids) {
				if expectedChain == before {
					t.Fatal("successful install did not publish a new chain")
				}
			} else if expectedChain != before {
				t.Fatal("failed install published a partial chain")
			}

			for _, id := range ids {
				chain, err := h.ThreadChain(id)
				if err != nil {
					t.Fatal(err)
				}
				if chain != expectedChain {
					t.Fatalf("thread %d has a different chain pointer", id)
				}
			}

			// Enumerate the complete finite domain to prove that no thread
			// sees partially installed behavior after the transaction.
			for _, id := range ids {
				chain, err := h.ThreadChain(id)
				if err != nil {
					t.Fatal(err)
				}
				Enumerate(syscalls, domains, func(point Point) bool {
					got := chain.EvaluatePoint(point)
					want := before.EvaluatePoint(point)
					if failAt == len(ids) {
						want = expectedChain.EvaluatePoint(point)
					}
					if got != want {
						t.Errorf(
							"thread %d syscall=%d args=%v after failed install: got %s, want %s",
							id, point.Syscall, point.Args, got, want,
						)
						return false
					}
					return true
				})
			}

			if failAt != len(ids) {
				// A thread created after the failed transaction must inherit
				// the old complete chain.
				const newID ThreadID = 999
				if err := h.AddThread(newID); err != nil {
					t.Fatal(err)
				}
				chain, err := h.ThreadChain(newID)
				if err != nil {
					t.Fatal(err)
				}
				if chain != before {
					t.Fatal("new thread inherited a partial chain")
				}

				// The same transaction must succeed once the failing thread
				// is healthy, and then reach every thread.
				h.SetInstallFunc(nil)
				h.SetRollbackFunc(nil)
				if err := h.Install(addition); err != nil {
					t.Fatalf("recovery install failed: %v", err)
				}
				finalChain := h.Chain()
				if finalChain == before {
					t.Fatal("recovery install did not publish a new chain")
				}
				allIDs := append(append([]ThreadID(nil), ids...), newID)
				for _, id := range allIDs {
					chain, err := h.ThreadChain(id)
					if err != nil {
						t.Fatal(err)
					}
					if chain != finalChain {
						t.Fatalf("thread %d not updated by recovery install", id)
					}
				}
			}
		})
	}
}

func TestInvalidFilterFailsBeforeTouchingThreads(t *testing.T) {
	h := NewHost(1, 2, 3)
	if err := h.Install(Filter{Rules: []Rule{DenyRule(1, 13)}}); err != nil {
		t.Fatal(err)
	}
	before := h.Chain()
	calls := 0
	h.SetInstallFunc(func(id ThreadID, previous, next *Chain) error {
		calls++
		return nil
	})

	err := h.Install(Filter{Rules: []Rule{{Syscall: 1, Action: Action(99)}}})
	if !errors.Is(err, ErrInvalidFilter) || !errors.Is(err, ErrInvalidAction) {
		t.Fatalf("error = %v", err)
	}
	if calls != 0 {
		t.Fatalf("install hook ran %d times for invalid filter", calls)
	}
	if h.Chain() != before {
		t.Fatal("invalid filter changed host chain")
	}
}

func TestRollbackFailureIsReported(t *testing.T) {
	h := NewHost(1, 2, 3)
	if err := h.Install(Filter{Rules: []Rule{DenyRule(1, 1)}}); err != nil {
		t.Fatal(err)
	}
	before := h.Chain()

	h.SetInstallFunc(func(id ThreadID, previous, next *Chain) error {
		if id == 3 {
			return errors.New("install failed")
		}
		return nil
	})
	h.SetRollbackFunc(func(id ThreadID, previous, next *Chain) error {
		return errors.New("rollback failed")
	})

	err := h.Install(Filter{Rules: []Rule{DenyRule(1, 2)}})
	if !errors.Is(err, ErrInstallFailed) {
		t.Fatalf("error = %v", err)
	}
	var installErr *InstallError
	if !errors.As(err, &installErr) {
		t.Fatalf("error type = %T", err)
	}
	if installErr.RollbackErr == nil {
		t.Fatal("rollback failure was not reported")
	}
	if h.Chain() != before {
		t.Fatal("host state changed despite failure")
	}
}

func TestHostZeroValueAndAddThreadErrors(t *testing.T) {
	var h Host
	if err := h.Install(Filter{Rules: []Rule{DenyRule(1, 13)}}); err != nil {
		t.Fatalf("zero-value host install: %v", err)
	}
	if err := h.AddThread(1); err != nil {
		t.Fatal(err)
	}
	if err := h.AddThread(1); !errors.Is(err, ErrThreadExists) {
		t.Fatalf("duplicate add error = %v", err)
	}
	if _, err := h.ThreadChain(2); !errors.Is(err, ErrUnknownThread) {
		t.Fatalf("unknown thread error = %v", err)
	}
}

func TestThreadsSnapshotAndIDs(t *testing.T) {
	h := NewHost(3, 1, 2)
	threads := h.Threads()
	ids := h.ThreadIDs()
	if !reflect.DeepEqual(ids, []ThreadID{1, 2, 3}) {
		t.Fatalf("ThreadIDs = %v", ids)
	}
	if len(threads) != 3 {
		t.Fatalf("Threads length = %d", len(threads))
	}
	for i, thread := range threads {
		if thread.ID != ids[i] {
			t.Fatalf("thread order mismatch at %d", i)
		}
	}
}
