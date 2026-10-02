// Command demo simulates a CDC replay: a batch of events arrives out of
// order (child rows before their parents) and the scheduler reorders them
// so the target database never sees a foreign key violation.
package main

import (
	"fmt"
	"math/rand"

	"cdcsched"
)

func main() {
	// users <- orders <- order_items
	schema := cdcsched.NewSchema().
		AddTable("users", "id").
		AddTable("orders", "id",
			cdcsched.ForeignKey{ChildTable: "orders", ChildColumn: "user_id", ParentTable: "users", ParentColumn: "id"}).
		AddTable("order_items", "id",
			cdcsched.ForeignKey{ChildTable: "order_items", ChildColumn: "order_id", ParentTable: "orders", ParentColumn: "id"})

	insertOrder, err := schema.TopoSort()
	if err != nil {
		panic(err)
	}
	deleteOrder, err := schema.DeleteOrder()
	if err != nil {
		panic(err)
	}
	fmt.Println("table insert order (parents first):", insertOrder)
	fmt.Println("table delete order (children first):", deleteOrder)
	fmt.Println()

	// A shuffled CDC batch: children deliberately arrive before parents.
	events := []cdcsched.Event{
		{ID: 1, Table: "order_items", Op: cdcsched.OpInsert, Key: "item-1", Row: map[string]string{"order_id": "order-1", "sku": "book"}},
		{ID: 2, Table: "order_items", Op: cdcsched.OpInsert, Key: "item-2", Row: map[string]string{"order_id": "order-1", "sku": "pen"}},
		{ID: 3, Table: "orders", Op: cdcsched.OpInsert, Key: "order-1", Row: map[string]string{"user_id": "user-1"}},
		{ID: 4, Table: "users", Op: cdcsched.OpInsert, Key: "user-1", Row: map[string]string{"name": "alice"}},
		{ID: 5, Table: "order_items", Op: cdcsched.OpInsert, Key: "item-3", Row: map[string]string{"order_id": "order-2", "sku": "mug"}},
		{ID: 6, Table: "orders", Op: cdcsched.OpInsert, Key: "order-2", Row: map[string]string{"user_id": "user-2"}},
		{ID: 7, Table: "users", Op: cdcsched.OpInsert, Key: "user-2", Row: map[string]string{"name": "bob"}},
	}
	rand.New(rand.NewSource(42)).Shuffle(len(events), func(i, j int) { events[i], events[j] = events[j], events[i] })

	fmt.Println("arrival order of the CDC batch:")
	for _, ev := range events {
		fmt.Printf("  #%d %s %s/%s\n", ev.ID, ev.Op, ev.Table, ev.Key)
	}
	fmt.Println()

	db := cdcsched.NewTargetDB(schema)
	sched := cdcsched.NewScheduler(schema, 4)

	results := sched.Replay(events, db.Apply)
	failed := 0
	for _, r := range results {
		if r.Err != nil {
			failed++
			fmt.Printf("event #%d FAILED: %v\n", r.Event.ID, r.Err)
		}
	}
	fmt.Printf("insert replay finished: %d/%d events applied, %d failed\n", len(events)-failed, len(events), failed)
	fmt.Println("actual apply order on the target DB:")
	for _, line := range db.Log() {
		fmt.Println(" ", line)
	}
	fmt.Println()

	// Now delete everything, again with a shuffled batch.
	deletes := []cdcsched.Event{
		{ID: 101, Table: "users", Op: cdcsched.OpDelete, Key: "user-1", Row: map[string]string{"id": "user-1"}},
		{ID: 102, Table: "orders", Op: cdcsched.OpDelete, Key: "order-1", Row: map[string]string{"id": "order-1", "user_id": "user-1"}},
		{ID: 103, Table: "order_items", Op: cdcsched.OpDelete, Key: "item-1", Row: map[string]string{"id": "item-1", "order_id": "order-1"}},
		{ID: 104, Table: "order_items", Op: cdcsched.OpDelete, Key: "item-2", Row: map[string]string{"id": "item-2", "order_id": "order-1"}},
		{ID: 105, Table: "users", Op: cdcsched.OpDelete, Key: "user-2", Row: map[string]string{"id": "user-2"}},
		{ID: 106, Table: "orders", Op: cdcsched.OpDelete, Key: "order-2", Row: map[string]string{"id": "order-2", "user_id": "user-2"}},
		{ID: 107, Table: "order_items", Op: cdcsched.OpDelete, Key: "item-3", Row: map[string]string{"id": "item-3", "order_id": "order-2"}},
	}
	results = sched.Replay(deletes, db.Apply)
	failed = 0
	for _, r := range results {
		if r.Err != nil {
			failed++
			fmt.Printf("event #%d FAILED: %v\n", r.Event.ID, r.Err)
		}
	}
	fmt.Printf("delete replay finished: %d/%d events applied, %d failed\n", len(deletes)-failed, len(deletes), failed)
	fmt.Println("actual apply order on the target DB:")
	for _, line := range db.Log()[len(events):] {
		fmt.Println(" ", line)
	}
	fmt.Printf("rows left: users=%d orders=%d order_items=%d\n",
		db.RowCount("users"), db.RowCount("orders"), db.RowCount("order_items"))
}
