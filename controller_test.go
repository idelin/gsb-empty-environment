package netsec

import (
	"errors"
	"fmt"
	"net/netip"
	"sync"
	"testing"
)

const (
	fieldDst  = "ipv4.dst"
	tableMain = "main"
)

func ipv4(t *testing.T, s string) []byte {
	t.Helper()
	addr, err := netip.ParseAddr(s)
	if err != nil {
		t.Fatalf("parse address %q: %v", s, err)
	}
	if !addr.Is4() {
		t.Fatalf("%q is not IPv4", s)
	}
	b := addr.As4()
	return append([]byte(nil), b[:]...)
}

func prefixPattern(t *testing.T, cidr string) Pattern {
	t.Helper()
	prefix, err := netip.ParsePrefix(cidr)
	if err != nil {
		t.Fatalf("parse prefix %q: %v", cidr, err)
	}
	addr := prefix.Masked().Addr().As4()
	p, err := NewPrefixPattern(addr[:], prefix.Bits())
	if err != nil {
		t.Fatalf("prefix pattern %q: %v", cidr, err)
	}
	return p
}

func lpmMatch(t *testing.T, field, cidr string) FieldMatch {
	t.Helper()
	prefix, err := netip.ParsePrefix(cidr)
	if err != nil {
		t.Fatalf("parse prefix %q: %v", cidr, err)
	}
	addr := prefix.Masked().Addr().As4()
	m, err := NewPrefixMatch(field, addr[:], prefix.Bits())
	if err != nil {
		t.Fatalf("LPM match %q: %v", cidr, err)
	}
	return m
}

func forward(port uint32) Action {
	return Action{Kind: "forward", Ports: []uint32{port}}
}

func drop() Action {
	return Action{Kind: "drop"}
}

func testController(t *testing.T) *Controller {
	t.Helper()
	ctrl, err := NewController([]Tenant{
		{
			ID: "tenant-a",
			HeaderSpace: HeaderSpace{
				fieldDst: {prefixPattern(t, "10.20.0.0/16")},
			},
			Ports: []uint32{10, 11},
		},
		{
			ID: "tenant-b",
			HeaderSpace: HeaderSpace{
				fieldDst: {prefixPattern(t, "10.21.0.0/16")},
			},
			Ports: []uint32{20, 21},
		},
	})
	if err != nil {
		t.Fatalf("NewController: %v", err)
	}
	return ctrl
}

func entry(id uint64, priority int32, action Action, matches ...FieldMatch) TableEntry {
	return TableEntry{
		Tenant:   "tenant-a",
		Table:    tableMain,
		ID:       id,
		Priority: priority,
		Matches:  matches,
		Action:   action,
	}
}

func TestMaskOverreachIsRejected(t *testing.T) {
	ctrl := testController(t)

	lpmTooWide := entry(1, 100, forward(10), lpmMatch(t, fieldDst, "10.20.0.0/15"))
	if _, err := ctrl.Insert(lpmTooWide, 0); !errors.Is(err, ErrUnauthorizedMatch) {
		t.Fatalf("LPM /15 insert error = %v, want ErrUnauthorizedMatch", err)
	}

	tooWideVerified, err := NewPrefixMatch(fieldDst, ipv4(t, "10.20.0.0"), 15)
	if err != nil {
		t.Fatal(err)
	}
	ternaryTooWide := entry(2, 100, forward(10),
		FieldMatch{
			Field: fieldDst,
			Kind:  MatchTernary,
			Value: ipv4(t, "10.20.0.0"),
			Mask:  []byte{0xff, 0xfe, 0x00, 0x00},
		},
	)
	if _, err := ctrl.Insert(ternaryTooWide, 0); !errors.Is(err, ErrUnauthorizedMatch) {
		t.Fatalf("ternary overreach error = %v, want ErrUnauthorizedMatch", err)
	}
	if err := ctrl.ValidateEntry(entry(3, 100, forward(10), tooWideVerified)); !errors.Is(err, ErrUnauthorizedMatch) {
		t.Fatalf("ValidateEntry overreach error = %v, want ErrUnauthorizedMatch", err)
	}

	exactOutside := entry(4, 100, forward(10),
		NewExactMatch(fieldDst, ipv4(t, "10.21.1.2")),
	)
	if _, err := ctrl.Insert(exactOutside, 0); !errors.Is(err, ErrUnauthorizedMatch) {
		t.Fatalf("outside exact match error = %v, want ErrUnauthorizedMatch", err)
	}

	if ctrl.Version() != 0 || len(ctrl.Entries(tableMain)) != 0 {
		t.Fatalf("rejected writes changed state: version=%d entries=%d",
			ctrl.Version(), len(ctrl.Entries(tableMain)))
	}

	inside := entry(5, 100, forward(10),
		FieldMatch{
			Field: fieldDst,
			Kind:  MatchTernary,
			Value: ipv4(t, "10.20.0.0"),
			Mask:  []byte{0xff, 0xff, 0x00, 0x00},
		},
	)
	if _, err := ctrl.Insert(inside, 0); err != nil {
		t.Fatalf("in-range ternary insert: %v", err)
	}
}

func TestAuthorizedUnionAndLPMIntersection(t *testing.T) {
	left := prefixPattern(t, "10.30.0.0/25")
	right := prefixPattern(t, "10.30.0.128/25")
	ctrl, err := NewController([]Tenant{{
		ID:          "union-tenant",
		HeaderSpace: HeaderSpace{fieldDst: {left, right}},
		Ports:       []uint32{7},
	}})
	if err != nil {
		t.Fatal(err)
	}

	whole := TableEntry{
		Tenant:   "union-tenant",
		Table:    tableMain,
		ID:       1,
		Priority: 10,
		Matches:  []FieldMatch{lpmMatch(t, fieldDst, "10.30.0.0/24")},
		Action:   forward(7),
	}
	if _, err := ctrl.Insert(whole, 0); err != nil {
		t.Fatalf("entry covered by the union of two /25 patterns: %v", err)
	}

	tooWide := TableEntry{
		Tenant:   "union-tenant",
		Table:    tableMain,
		ID:       2,
		Priority: 10,
		Matches:  []FieldMatch{lpmMatch(t, fieldDst, "10.30.0.0/23")},
		Action:   forward(7),
	}
	if _, err := ctrl.Insert(tooWide, 1); !errors.Is(err, ErrUnauthorizedMatch) {
		t.Fatalf("/23 beyond union error = %v, want ErrUnauthorizedMatch", err)
	}

	nonContiguous := TableEntry{
		Tenant:   "union-tenant",
		Table:    tableMain,
		ID:       3,
		Priority: 10,
		Matches: []FieldMatch{{
			Field: fieldDst,
			Kind:  MatchLPM,
			Value: ipv4(t, "10.30.0.0"),
			Mask:  []byte{0xff, 0x00, 0xff, 0x00},
		}},
		Action: forward(7),
	}
	if _, err := ctrl.Insert(nonContiguous, 1); !errors.Is(err, ErrInvalidMatch) {
		t.Fatalf("non-contiguous LPM mask error = %v, want ErrInvalidMatch", err)
	}
}

func TestSamePriorityAmbiguity(t *testing.T) {
	ctrl := testController(t)

	base := entry(1, 100, forward(10), lpmMatch(t, fieldDst, "10.20.0.0/24"))
	if _, err := ctrl.Insert(base, 0); err != nil {
		t.Fatal(err)
	}

	overlapDifferentAction := entry(2, 100, drop(), lpmMatch(t, fieldDst, "10.20.0.128/25"))
	if _, err := ctrl.Insert(overlapDifferentAction, 1); !errors.Is(err, ErrPriorityConflict) {
		t.Fatalf("same-priority overlap error = %v, want ErrPriorityConflict", err)
	}
	if ctrl.Version() != 1 {
		t.Fatalf("failed insert bumped version to %d", ctrl.Version())
	}

	overlapSameAction := entry(3, 100, forward(10), lpmMatch(t, fieldDst, "10.20.0.128/25"))
	if _, err := ctrl.Insert(overlapSameAction, 1); err != nil {
		t.Fatalf("same action overlap should be allowed: %v", err)
	}

	overlapDifferentPriority := entry(4, 99, drop(), lpmMatch(t, fieldDst, "10.20.0.128/25"))
	if _, err := ctrl.Insert(overlapDifferentPriority, 2); err != nil {
		t.Fatalf("different priority overlap should be allowed: %v", err)
	}

	disjointSamePriority := entry(5, 100, drop(), lpmMatch(t, fieldDst, "10.20.64.0/24"))
	if _, err := ctrl.Insert(disjointSamePriority, 3); err != nil {
		t.Fatalf("disjoint same-priority entry should be allowed: %v", err)
	}

	if got := len(ctrl.Entries(tableMain)); got != 4 {
		t.Fatalf("entry count = %d, want 4", got)
	}
}

func TestConflictUsesFullHeaderIntersection(t *testing.T) {
	fieldSrc := "ipv4.src"
	ctrl, err := NewController([]Tenant{{
		ID: "multi-field",
		HeaderSpace: HeaderSpace{
			fieldDst: {prefixPattern(t, "10.20.0.0/16")},
			fieldSrc: {prefixPattern(t, "10.40.0.0/16")},
		},
		Ports: []uint32{10},
	}})
	if err != nil {
		t.Fatal(err)
	}

	first := TableEntry{
		Tenant: "multi-field", Table: tableMain, ID: 1, Priority: 5,
		Matches: []FieldMatch{
			lpmMatch(t, fieldSrc, "10.40.0.0/24"),
			lpmMatch(t, fieldDst, "10.20.1.0/24"),
		},
		Action: forward(10),
	}
	if _, err := ctrl.Insert(first, 0); err != nil {
		t.Fatal(err)
	}

	// Source overlaps, destination is disjoint: full header intersection is
	// empty, so this is not an ambiguity.
	srcOnlyOverlap := first
	srcOnlyOverlap.ID = 2
	srcOnlyOverlap.Action = drop()
	srcOnlyOverlap.Matches = []FieldMatch{
		lpmMatch(t, fieldSrc, "10.40.0.0/25"),
		lpmMatch(t, fieldDst, "10.20.2.0/24"),
	}
	if _, err := ctrl.Insert(srcOnlyOverlap, 1); err != nil {
		t.Fatalf("disjoint destination should avoid conflict: %v", err)
	}

	bothOverlap := srcOnlyOverlap
	bothOverlap.ID = 3
	bothOverlap.Matches = []FieldMatch{
		lpmMatch(t, fieldSrc, "10.40.0.0/25"),
		lpmMatch(t, fieldDst, "10.20.1.0/25"),
	}
	if _, err := ctrl.Insert(bothOverlap, 2); !errors.Is(err, ErrPriorityConflict) {
		t.Fatalf("both fields overlap error = %v, want ErrPriorityConflict", err)
	}
}

func TestEgressPortAuthorization(t *testing.T) {
	ctrl := testController(t)

	unauthorized := entry(1, 10, forward(99), lpmMatch(t, fieldDst, "10.20.0.0/24"))
	if _, err := ctrl.Insert(unauthorized, 0); !errors.Is(err, ErrUnauthorizedPort) {
		t.Fatalf("unauthorized port error = %v, want ErrUnauthorizedPort", err)
	}
	if err := ctrl.ValidateEntry(unauthorized); !errors.Is(err, ErrUnauthorizedPort) {
		t.Fatalf("ValidateEntry unauthorized port error = %v, want ErrUnauthorizedPort", err)
	}

	otherTenantsPort := entry(2, 10, forward(20), lpmMatch(t, fieldDst, "10.20.0.0/24"))
	if _, err := ctrl.Insert(otherTenantsPort, 0); !errors.Is(err, ErrUnauthorizedPort) {
		t.Fatalf("other tenant's port error = %v, want ErrUnauthorizedPort", err)
	}

	authorized := entry(3, 10, forward(11), lpmMatch(t, fieldDst, "10.20.0.0/24"))
	if _, err := ctrl.Insert(authorized, 0); err != nil {
		t.Fatalf("authorized port insert: %v", err)
	}
}

func TestModifyAndDeleteUseVersionAtomically(t *testing.T) {
	ctrl := testController(t)
	e1 := entry(1, 10, forward(10), lpmMatch(t, fieldDst, "10.20.0.0/24"))
	e2 := entry(2, 20, forward(11), lpmMatch(t, fieldDst, "10.20.1.0/24"))

	if v, err := ctrl.Insert(e1, 0); err != nil || v != 1 {
		t.Fatalf("insert e1: version=%d err=%v", v, err)
	}
	if v, err := ctrl.Insert(e2, 1); err != nil || v != 2 {
		t.Fatalf("insert e2: version=%d err=%v", v, err)
	}

	modified := e1
	modified.Priority = 50
	modified.Action = forward(11)
	if v, err := ctrl.Apply(WriteRequest{
		ExpectedVersion: 2,
		Operations: []Operation{
			ModifyOperation(modified),
			DeleteOperation(tableMain, e2.ID),
		},
	}); err != nil || v != 3 {
		t.Fatalf("atomic modify+delete: version=%d err=%v", v, err)
	}
	if got, ok := ctrl.Entry(tableMain, 1); !ok || got.Priority != 50 || !got.Action.Equal(forward(11)) {
		t.Fatalf("modified entry = %+v, found=%v", got, ok)
	}
	if _, ok := ctrl.Entry(tableMain, 2); ok {
		t.Fatal("deleted entry is still present")
	}

	staleDelete := WriteRequest{
		ExpectedVersion: 2,
		Operations:      []Operation{DeleteOperation(tableMain, 1)},
	}
	if _, err := ctrl.Apply(staleDelete); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale batch error = %v, want ErrVersionConflict", err)
	}
	if got, ok := ctrl.Entry(tableMain, 1); !ok || !got.Action.Equal(forward(11)) {
		t.Fatal("stale delete changed committed state")
	}
	if ctrl.Version() != 3 {
		t.Fatalf("stale batch changed version to %d", ctrl.Version())
	}

	if v, err := ctrl.Modify(modified, 3); err != nil || v != 4 {
		t.Fatalf("modify at current version: version=%d err=%v", v, err)
	}
	if v, err := ctrl.Delete(tableMain, 1, 4); err != nil || v != 5 {
		t.Fatalf("delete at current version: version=%d err=%v", v, err)
	}
	if len(ctrl.Entries(tableMain)) != 0 {
		t.Fatal("table is not empty after delete")
	}
}

func TestBatchRollbackOnAnyFailure(t *testing.T) {
	ctrl := testController(t)
	base := entry(1, 10, forward(10), lpmMatch(t, fieldDst, "10.20.0.0/24"))
	if _, err := ctrl.Insert(base, 0); err != nil {
		t.Fatal(err)
	}

	valid := entry(2, 20, forward(11), lpmMatch(t, fieldDst, "10.20.1.0/24"))
	invalidPort := entry(3, 30, forward(99), lpmMatch(t, fieldDst, "10.20.2.0/24"))
	if _, err := ctrl.Apply(WriteRequest{
		ExpectedVersion: 1,
		Operations: []Operation{
			InsertOperation(valid),
			InsertOperation(invalidPort),
		},
	}); !errors.Is(err, ErrUnauthorizedPort) {
		t.Fatalf("batch error = %v, want ErrUnauthorizedPort", err)
	}
	if ctrl.Version() != 1 || len(ctrl.Entries(tableMain)) != 1 {
		t.Fatalf("failed batch was not rolled back: version=%d entries=%d",
			ctrl.Version(), len(ctrl.Entries(tableMain)))
	}
	if _, ok := ctrl.Entry(tableMain, valid.ID); ok {
		t.Fatal("partially applied insert is visible")
	}

	if _, err := ctrl.Apply(WriteRequest{
		ExpectedVersion: 1,
		Operations: []Operation{
			ModifyOperation(entry(1, 10, forward(11), lpmMatch(t, fieldDst, "10.20.0.0/24"))),
			DeleteOperation(tableMain, 999),
		},
	}); !errors.Is(err, ErrEntryNotFound) {
		t.Fatalf("batch with missing delete error = %v, want ErrEntryNotFound", err)
	}
	if got, ok := ctrl.Entry(tableMain, 1); !ok || !got.Action.Equal(forward(10)) {
		t.Fatal("failed batch partially applied modify")
	}
}

func TestConcurrentInsertsHaveOneWinner(t *testing.T) {
	ctrl := testController(t)

	const writers = 32
	start := make(chan struct{})
	errs := make(chan error, writers)
	var wg sync.WaitGroup
	wg.Add(writers)
	for i := 0; i < writers; i++ {
		i := i
		go func() {
			defer wg.Done()
			<-start
			addr := ipv4(t, fmt.Sprintf("10.20.0.%d", i+1))
			_, err := ctrl.Insert(entry(uint64(i+1), 10, forward(10), NewExactMatch(fieldDst, addr)), 0)
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)

	successes, conflicts := 0, 0
	for err := range errs {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrVersionConflict):
			conflicts++
		default:
			t.Fatalf("unexpected concurrent insert error: %v", err)
		}
	}
	if successes != 1 || conflicts != writers-1 {
		t.Fatalf("successes=%d conflicts=%d, want 1/%d", successes, conflicts, writers-1)
	}
	if ctrl.Version() != 1 || len(ctrl.Entries(tableMain)) != 1 {
		t.Fatalf("winner not committed exactly once: version=%d entries=%d",
			ctrl.Version(), len(ctrl.Entries(tableMain)))
	}
}

func TestConcurrentModifyCAS(t *testing.T) {
	ctrl := testController(t)
	base := entry(1, 10, forward(10), lpmMatch(t, fieldDst, "10.20.0.0/24"))
	if _, err := ctrl.Insert(base, 0); err != nil {
		t.Fatal(err)
	}

	const writers = 16
	type result struct {
		kind string
		err  error
	}
	start := make(chan struct{})
	results := make(chan result, writers)
	var wg sync.WaitGroup
	wg.Add(writers)
	for i := 0; i < writers; i++ {
		i := i
		go func() {
			defer wg.Done()
			<-start
			kind := fmt.Sprintf("action-%d", i)
			candidate := base
			candidate.Action = Action{Kind: kind, Ports: []uint32{10}}
			_, err := ctrl.Modify(candidate, 1)
			results <- result{kind: kind, err: err}
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	var winner string
	successes, conflicts := 0, 0
	for res := range results {
		switch {
		case res.err == nil:
			successes++
			winner = res.kind
		case errors.Is(res.err, ErrVersionConflict):
			conflicts++
		default:
			t.Fatalf("unexpected concurrent modify error: %v", res.err)
		}
	}
	if successes != 1 || conflicts != writers-1 {
		t.Fatalf("successes=%d conflicts=%d, want 1/%d", successes, conflicts, writers-1)
	}
	if ctrl.Version() != 2 {
		t.Fatalf("version = %d, want 2", ctrl.Version())
	}
	got, ok := ctrl.Entry(tableMain, 1)
	if !ok || got.Action.Kind != winner {
		t.Fatalf("committed action = %q, want winner %q", got.Action.Kind, winner)
	}
}
