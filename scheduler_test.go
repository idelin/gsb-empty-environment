package cdcsched

import (
	"fmt"
	"math/rand"
	"strings"
	"sync/atomic"
	"testing"
)

func testSchema() *Schema {
	return NewSchema().
		AddTable("users", "id").
		AddTable("orders", "id",
			ForeignKey{ChildTable: "orders", ChildColumn: "user_id", ParentTable: "users", ParentColumn: "id"}).
		AddTable("order_items", "id",
			ForeignKey{ChildTable: "order_items", ChildColumn: "order_id", ParentTable: "orders", ParentColumn: "id"})
}

func indexOf(t *testing.T, log []string, want string) int {
	t.Helper()
	for i, line := range log {
		if line == want {
			return i
		}
	}
	t.Fatalf("%q not found in apply log %v", want, log)
	return -1
}

func TestTopoSortParentsFirst(t *testing.T) {
	order, err := testSchema().TopoSort()
	if err != nil {
		t.Fatalf("TopoSort: %v", err)
	}
	want := []string{"users", "orders", "order_items"}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Fatalf("got %v, want %v", order, want)
	}

	del, err := testSchema().DeleteOrder()
	if err != nil {
		t.Fatalf("DeleteOrder: %v", err)
	}
	wantDel := []string{"order_items", "orders", "users"}
	if strings.Join(del, ",") != strings.Join(wantDel, ",") {
		t.Fatalf("got %v, want %v", del, wantDel)
	}
}

func TestTopoSortDetectsCycle(t *testing.T) {
	s := NewSchema().
		AddTable("a", "id", ForeignKey{ChildTable: "a", ChildColumn: "b_id", ParentTable: "b", ParentColumn: "id"}).
		AddTable("b", "id", ForeignKey{ChildTable: "b", ChildColumn: "a_id", ParentTable: "a", ParentColumn: "id"})
	if _, err := s.TopoSort(); err == nil {
		t.Fatal("expected cycle error, got nil")
	}
}

// Acceptance scenario: the batch contains the OrderItem insert BEFORE the User
// insert. The scheduler must suspend the OrderItem (and the Order) until the
// User row has been applied, so the target never raises an FK error.
func TestOutOfOrderInsertsAreReordered(t *testing.T) {
	schema := testSchema()
	db := NewTargetDB(schema)
	sched := NewScheduler(schema, 4)

	events := []Event{
		{ID: 1, Table: "order_items", Op: OpInsert, Key: "item-1", Row: map[string]string{"order_id": "order-1"}},
		{ID: 2, Table: "orders", Op: OpInsert, Key: "order-1", Row: map[string]string{"user_id": "user-1"}},
		{ID: 3, Table: "users", Op: OpInsert, Key: "user-1", Row: map[string]string{"name": "alice"}},
	}
	results := sched.Replay(events, db.Apply)
	for _, r := range results {
		if r.Err != nil {
			t.Fatalf("event #%d failed: %v", r.Event.ID, r.Err)
		}
	}

	log := db.Log()
	iUser := indexOf(t, log, "INSERT users/user-1")
	iOrder := indexOf(t, log, "INSERT orders/order-1")
	iItem := indexOf(t, log, "INSERT order_items/item-1")
	if !(iUser < iOrder && iOrder < iItem) {
		t.Fatalf("parent rows not applied before children: %v", log)
	}
}

func TestDeletesRunChildrenFirst(t *testing.T) {
	schema := testSchema()
	db := NewTargetDB(schema)
	sched := NewScheduler(schema, 4)

	seed := []Event{
		{ID: 1, Table: "users", Op: OpInsert, Key: "user-1", Row: map[string]string{}},
		{ID: 2, Table: "orders", Op: OpInsert, Key: "order-1", Row: map[string]string{"user_id": "user-1"}},
		{ID: 3, Table: "order_items", Op: OpInsert, Key: "item-1", Row: map[string]string{"order_id": "order-1"}},
	}
	for _, r := range sched.Replay(seed, db.Apply) {
		if r.Err != nil {
			t.Fatalf("seed failed: %v", r.Err)
		}
	}

	// Parent delete arrives first in the batch.
	deletes := []Event{
		{ID: 11, Table: "users", Op: OpDelete, Key: "user-1", Row: map[string]string{"id": "user-1"}},
		{ID: 12, Table: "order_items", Op: OpDelete, Key: "item-1", Row: map[string]string{"order_id": "order-1"}},
		{ID: 13, Table: "orders", Op: OpDelete, Key: "order-1", Row: map[string]string{"user_id": "user-1"}},
	}
	results := sched.Replay(deletes, db.Apply)
	for _, r := range results {
		if r.Err != nil {
			t.Fatalf("delete event #%d failed: %v", r.Event.ID, r.Err)
		}
	}

	log := db.Log()
	dItem := indexOf(t, log, "DELETE order_items/item-1")
	dOrder := indexOf(t, log, "DELETE orders/order-1")
	dUser := indexOf(t, log, "DELETE users/user-1")
	if !(dItem < dOrder && dOrder < dUser) {
		t.Fatalf("children not deleted before parents: %v", log)
	}
	if db.RowCount("users") != 0 || db.RowCount("orders") != 0 || db.RowCount("order_items") != 0 {
		t.Fatalf("expected empty db, got users=%d orders=%d order_items=%d",
			db.RowCount("users"), db.RowCount("orders"), db.RowCount("order_items"))
	}
}

func TestFailedParentSkipsChildren(t *testing.T) {
	schema := testSchema()
	sched := NewScheduler(schema, 4)

	var applied int32
	apply := func(ev Event) error {
		atomic.AddInt32(&applied, 1)
		if ev.Table == "users" {
			return fmt.Errorf("simulated write failure")
		}
		return nil
	}

	events := []Event{
		{ID: 1, Table: "users", Op: OpInsert, Key: "user-1", Row: map[string]string{}},
		{ID: 2, Table: "orders", Op: OpInsert, Key: "order-1", Row: map[string]string{"user_id": "user-1"}},
		{ID: 3, Table: "order_items", Op: OpInsert, Key: "item-1", Row: map[string]string{"order_id": "order-1"}},
	}
	results := sched.Replay(events, apply)

	if results[0].Err == nil {
		t.Fatal("expected user insert to fail")
	}
	for _, r := range results[1:] {
		if r.Err == nil {
			t.Fatalf("expected event #%d to be skipped, got nil error", r.Event.ID)
		}
		if !strings.Contains(r.Err.Error(), "skipped") {
			t.Fatalf("event #%d: expected skip error, got %v", r.Event.ID, r.Err)
		}
	}
	if got := atomic.LoadInt32(&applied); got != 1 {
		t.Fatalf("apply called %d times, want 1 (children must never be attempted)", got)
	}
}

func TestConcurrentReplayLargeShuffledBatch(t *testing.T) {
	schema := testSchema()
	db := NewTargetDB(schema)
	sched := NewScheduler(schema, 8)

	var events []Event
	id := 0
	for u := 0; u < 20; u++ {
		user := fmt.Sprintf("user-%d", u)
		id++
		events = append(events, Event{ID: id, Table: "users", Op: OpInsert, Key: user, Row: map[string]string{}})
		for o := 0; o < 5; o++ {
			order := fmt.Sprintf("order-%d-%d", u, o)
			id++
			events = append(events, Event{ID: id, Table: "orders", Op: OpInsert, Key: order, Row: map[string]string{"user_id": user}})
			for i := 0; i < 4; i++ {
				item := fmt.Sprintf("item-%d-%d-%d", u, o, i)
				id++
				events = append(events, Event{ID: id, Table: "order_items", Op: OpInsert, Key: item, Row: map[string]string{"order_id": order}})
			}
		}
	}
	rand.New(rand.NewSource(7)).Shuffle(len(events), func(i, j int) { events[i], events[j] = events[j], events[i] })

	results := sched.Replay(events, db.Apply)
	for _, r := range results {
		if r.Err != nil {
			t.Fatalf("event #%d (%s %s/%s) failed: %v", r.Event.ID, r.Event.Op, r.Event.Table, r.Event.Key, r.Err)
		}
	}
	if got := db.RowCount("users"); got != 20 {
		t.Fatalf("users=%d, want 20", got)
	}
	if got := db.RowCount("orders"); got != 100 {
		t.Fatalf("orders=%d, want 100", got)
	}
	if got := db.RowCount("order_items"); got != 400 {
		t.Fatalf("order_items=%d, want 400", got)
	}

	// Every parent insert must appear before its children in the apply log.
	pos := make(map[string]int, len(db.Log()))
	for i, line := range db.Log() {
		parts := strings.SplitN(line, " ", 2)
		pos[parts[1]] = i
	}
	for _, ev := range events {
		child := ev.Table + "/" + ev.Key
		for _, parentKey := range ev.Row {
			var parentTable string
			switch ev.Table {
			case "orders":
				parentTable = "users"
			case "order_items":
				parentTable = "orders"
			default:
				continue
			}
			if pos[parentTable+"/"+parentKey] >= pos[child] {
				t.Fatalf("parent %s/%s applied after child %s", parentTable, parentKey, child)
			}
		}
	}
}
