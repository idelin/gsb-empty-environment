package netsec

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

var testKey = []byte("captable-test-hmac-key-0123456789")

const (
	permRead Perms = 1 << iota
	permWrite
	permExec
)

func mustTable(t *testing.T, slots int) *Table {
	t.Helper()
	tbl, err := NewTable(slots, testKey)
	if err != nil {
		t.Fatalf("NewTable: %v", err)
	}
	return tbl
}

func mustAllocate(t *testing.T, tbl *Table, obj any, objType ObjType, perms Perms) Ticket {
	t.Helper()
	tk, err := tbl.Allocate(obj, objType, perms)
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	return tk
}

func TestAllocateUseIssueRevoke(t *testing.T) {
	tbl := mustTable(t, 2)
	tk := mustAllocate(t, tbl, "obj-A", 7, permRead|permWrite)

	obj, err := tbl.Use(tk, permRead)
	if err != nil || obj != "obj-A" {
		t.Fatalf("Use: obj=%v err=%v", obj, err)
	}

	sub, err := tbl.Issue(tk, permRead)
	if err != nil {
		t.Fatalf("Issue subset: %v", err)
	}
	if _, err := tbl.Use(sub, permRead); err != nil {
		t.Fatalf("Use subset ticket: %v", err)
	}
	if _, err := tbl.Use(sub, permWrite); !errors.Is(err, ErrNotSubset) {
		t.Fatalf("Use subset ticket with write: err=%v", err)
	}
	if _, err := tbl.Issue(tk, permRead|permExec); !errors.Is(err, ErrNotSubset) {
		t.Fatalf("Issue superset: err=%v", err)
	}
	if _, err := tbl.Issue(tk, 0); !errors.Is(err, ErrEmptyPerms) {
		t.Fatalf("Issue empty perms: err=%v", err)
	}

	if err := tbl.Revoke(tk); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if _, err := tbl.Use(tk, permRead); !errors.Is(err, ErrStale) {
		t.Fatalf("Use after revoke: err=%v", err)
	}
	if _, err := tbl.Issue(tk, permRead); !errors.Is(err, ErrStale) {
		t.Fatalf("Issue after revoke: err=%v", err)
	}
	if err := tbl.Revoke(tk); !errors.Is(err, ErrStale) {
		t.Fatalf("double revoke: err=%v", err)
	}

	tk2 := mustAllocate(t, tbl, "obj-B", 7, permRead)
	if tk2.Slot != tk.Slot || tk2.Generation != tk.Generation+1 {
		t.Fatalf("slot reuse: old=%v new=%v", tk, tk2)
	}
	if _, err := tbl.Use(tk, permRead); !errors.Is(err, ErrStale) {
		t.Fatalf("old ticket after reuse: err=%v", err)
	}
}

func TestMACCoversEveryField(t *testing.T) {
	tbl := mustTable(t, 2)
	tk := mustAllocate(t, tbl, "obj-A", 7, permRead)

	permsSwapped := tk
	permsSwapped.Perms = permWrite // attacker upgrades permissions
	genBumped := tk
	genBumped.Generation++
	typeSwapped := tk
	typeSwapped.ObjType = 8
	slotSwapped := tk
	slotSwapped.Slot = 1
	tableSwapped := tk
	tableSwapped.TableID[0] ^= 0xff
	macFlipped := tk
	macFlipped.MAC[0] ^= 0x01

	cases := map[string]Ticket{
		"perms swapped": permsSwapped,
		"generation":    genBumped,
		"object type":   typeSwapped,
		"slot":          slotSwapped,
		"table ID":      tableSwapped,
		"mac flipped":   macFlipped,
	}
	for name, bad := range cases {
		if _, err := tbl.Use(bad, permRead); !errors.Is(err, ErrBadMAC) {
			t.Errorf("%s: err=%v, want ErrBadMAC", name, err)
		}
	}
}

func TestSlotOutOfRange(t *testing.T) {
	tbl := mustTable(t, 1)
	tk := mustAllocate(t, tbl, "obj-A", 7, permRead)

	// Attacker-corrupted slot: rejected at the MAC stage.
	bad := tk
	bad.Slot = 5
	if _, err := tbl.Use(bad, permRead); !errors.Is(err, ErrBadMAC) {
		t.Fatalf("corrupted slot: err=%v", err)
	}

	// Host-side ticket minted for a slot beyond the table (e.g. after a
	// shrink): must hit the bounds check, never index the slice.
	forged := Ticket{TableID: tbl.id, Slot: 5, Generation: 0, Perms: permRead, ObjType: 7}
	forged.MAC = tbl.mac(5, 0, permRead, 7)
	if _, err := tbl.Use(forged, permRead); !errors.Is(err, ErrSlotRange) {
		t.Fatalf("out-of-range slot: err=%v", err)
	}
}

func TestSameTypeDifferentObject(t *testing.T) {
	tbl := mustTable(t, 1)
	tkA := mustAllocate(t, tbl, "obj-A", 9, permRead)
	if err := tbl.Revoke(tkA); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	// Same object type, different object reuses the slot.
	tkB := mustAllocate(t, tbl, "obj-B", 9, permRead)
	if tkB.ObjType != tkA.ObjType {
		t.Fatalf("types differ: %v vs %v", tkA, tkB)
	}
	if _, err := tbl.Use(tkA, permRead); !errors.Is(err, ErrStale) {
		t.Fatalf("stale ticket reached new object: err=%v", err)
	}
	obj, err := tbl.Use(tkB, permRead)
	if err != nil || obj != "obj-B" {
		t.Fatalf("new ticket: obj=%v err=%v", obj, err)
	}
}

func TestClearTableBurnsAllTickets(t *testing.T) {
	tbl := mustTable(t, 2)
	tk1 := mustAllocate(t, tbl, "obj-A", 7, permRead)
	tk2 := mustAllocate(t, tbl, "obj-B", 8, permWrite)
	oldID := tbl.TableID()

	if err := tbl.ClearTable(); err != nil {
		t.Fatalf("ClearTable: %v", err)
	}
	if tbl.TableID() == oldID {
		t.Fatal("table ID not rotated")
	}
	for i, tk := range []Ticket{tk1, tk2} {
		if _, err := tbl.Use(tk, permRead); !errors.Is(err, ErrBadMAC) {
			t.Fatalf("ticket %d survived ClearTable: err=%v", i, err)
		}
	}
	// Table stays usable under the new ID.
	tk3 := mustAllocate(t, tbl, "obj-C", 7, permRead)
	if obj, err := tbl.Use(tk3, permRead); err != nil || obj != "obj-C" {
		t.Fatalf("Use after ClearTable: obj=%v err=%v", obj, err)
	}
}

func TestGenerationBurnsAtMaxNoWrap(t *testing.T) {
	tbl := mustTable(t, 1)
	tbl.slots[0].generation = MaxGeneration - 1

	tk := mustAllocate(t, tbl, "obj-A", 7, permRead)
	if tk.Generation != MaxGeneration-1 {
		t.Fatalf("generation: got %d", tk.Generation)
	}
	if err := tbl.Revoke(tk); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	tk2 := mustAllocate(t, tbl, "obj-B", 7, permRead)
	if tk2.Generation != MaxGeneration {
		t.Fatalf("generation: got %d", tk2.Generation)
	}
	if err := tbl.Revoke(tk2); err != nil {
		t.Fatalf("Revoke at max: %v", err)
	}
	if !tbl.slots[0].burned {
		t.Fatal("slot not burned at max generation")
	}
	if tbl.slots[0].generation != MaxGeneration {
		t.Fatalf("generation wrapped to %d", tbl.slots[0].generation)
	}
	if _, err := tbl.Allocate("obj-C", 7, permRead); !errors.Is(err, ErrTableFull) {
		t.Fatalf("burned slot reused: err=%v", err)
	}
	if _, err := tbl.Use(tk2, permRead); !errors.Is(err, ErrStale) {
		t.Fatalf("ticket on burned slot: err=%v", err)
	}
}

func TestTxnAtomicCommitAndAbort(t *testing.T) {
	tbl := mustTable(t, 2)
	tkA := mustAllocate(t, tbl, "obj-A", 1, permRead)
	tkB := mustAllocate(t, tbl, "obj-B", 1, permRead)

	// Failing txn: one valid revoke, one forged revoke, one allocate.
	// Nothing may be published; no slot may be partially reused.
	forged := tkB
	forged.Perms = permWrite
	txn := tbl.BeginTxn()
	txn.Revoke(tkA)
	txn.Revoke(forged)
	txn.Allocate("obj-C", 1, permRead)
	if _, err := txn.Commit(); err == nil {
		t.Fatal("txn with forged revoke committed")
	}
	for i, tk := range []Ticket{tkA, tkB} {
		if _, err := tbl.Use(tk, permRead); err != nil {
			t.Fatalf("ticket %d lost after aborted txn: %v", i, err)
		}
	}
	if _, err := tbl.Allocate("obj-C", 1, permRead); !errors.Is(err, ErrTableFull) {
		t.Fatalf("aborted txn freed a slot: err=%v", err)
	}

	// Duplicate revoke of one slot in a txn is rejected wholesale.
	dup := tbl.BeginTxn()
	dup.Revoke(tkA)
	dup.Revoke(tkA)
	if _, err := dup.Commit(); err == nil {
		t.Fatal("txn with duplicate revoke committed")
	}
	if _, err := tbl.Use(tkA, permRead); err != nil {
		t.Fatalf("duplicate-revoke txn mutated state: %v", err)
	}

	// Over-capacity txn is rejected wholesale.
	over := tbl.BeginTxn()
	over.Allocate("x", 1, permRead)
	over.Allocate("y", 1, permRead)
	over.Allocate("z", 1, permRead)
	if _, err := over.Commit(); !errors.Is(err, ErrTableFull) {
		t.Fatalf("over-capacity txn: err=%v", err)
	}

	// Valid txn: revoke A, allocate C reusing the freed slot atomically.
	ok := tbl.BeginTxn()
	ok.Revoke(tkA)
	ok.Allocate("obj-C", 2, permRead|permWrite)
	issued, err := ok.Commit()
	if err != nil {
		t.Fatalf("valid txn: %v", err)
	}
	if len(issued) != 1 || issued[0].Slot != tkA.Slot || issued[0].Generation != tkA.Generation+1 {
		t.Fatalf("txn allocate: %v", issued)
	}
	if _, err := tbl.Use(tkA, permRead); !errors.Is(err, ErrStale) {
		t.Fatalf("revoked ticket after txn: err=%v", err)
	}
	if obj, err := tbl.Use(issued[0], permWrite); err != nil || obj != "obj-C" {
		t.Fatalf("txn-issued ticket: obj=%v err=%v", obj, err)
	}
	if obj, err := tbl.Use(tkB, permRead); err != nil || obj != "obj-B" {
		t.Fatalf("untouched ticket: obj=%v err=%v", obj, err)
	}
}

// TestUseRevokeLinearization hammers Use and Revoke concurrently. Because
// both linearize on the table lock (Use reads the entry and checks the
// capability under one critical section), every successful Use must return
// the original object, and no Use may succeed once Revoke has linearized.
func TestUseRevokeLinearization(t *testing.T) {
	tbl := mustTable(t, 1)
	tk := mustAllocate(t, tbl, "obj-A", 7, permRead)

	const users = 16
	start := make(chan struct{})
	revoked := make(chan struct{})
	errs := make(chan error, users*8)
	var wg sync.WaitGroup

	// Group 1: Use racing Revoke. Successes must see obj-A; failures must
	// be ErrStale (the ticket itself is well-formed).
	for i := 0; i < users; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for j := 0; j < 4; j++ {
				obj, err := tbl.Use(tk, permRead)
				switch {
				case err == nil && obj != "obj-A":
					errs <- fmt.Errorf("Use returned %v, want obj-A", obj)
				case err != nil && !errors.Is(err, ErrStale):
					errs <- fmt.Errorf("Use failed with %v, want ErrStale", err)
				}
			}
		}()
	}

	// Group 2: Use strictly after Revoke has linearized must fail.
	for i := 0; i < users; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			<-revoked
			if _, err := tbl.Use(tk, permRead); err == nil {
				errs <- errors.New("Use succeeded after Revoke linearized")
			}
		}()
	}

	close(start)
	if err := tbl.Revoke(tk); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	close(revoked)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}

	// The linearization order is well defined: exactly one Revoke wins.
	if err := tbl.Revoke(tk); !errors.Is(err, ErrStale) {
		t.Fatalf("second Revoke: err=%v", err)
	}
	if _, err := tbl.Use(tk, permRead); !errors.Is(err, ErrStale) {
		t.Fatalf("final Use: err=%v", err)
	}
}
