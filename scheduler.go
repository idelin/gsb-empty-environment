package cdcsched

import (
	"fmt"
	"sync"
)

// Scheduler replays a batch of CDC events against a target database while
// preserving foreign key ordering:
//
//   - INSERT/UPDATE of a child row runs only after the INSERT/UPDATE of every
//     parent row it references (when that parent change is in the same batch).
//   - DELETE of a parent row runs only after the DELETE of every child row in
//     the batch that references it.
//
// Events whose dependencies are not yet satisfied are suspended; independent
// events are applied concurrently by a worker pool.
type Scheduler struct {
	schema  *Schema
	workers int
}

// NewScheduler creates a Scheduler. workers <= 0 defaults to 4.
func NewScheduler(schema *Schema, workers int) *Scheduler {
	if workers <= 0 {
		workers = 4
	}
	return &Scheduler{schema: schema, workers: workers}
}

// Result pairs an event with the error returned while applying it, if any.
// An event skipped because one of its dependencies failed carries a
// non-nil Err even though apply was never invoked for it.
type Result struct {
	Event Event
	Err   error
}

// Replay applies all events and returns one Result per event, in input order.
// It never violates FK ordering regardless of the order events arrive in.
func (s *Scheduler) Replay(events []Event, apply func(Event) error) []Result {
	n := len(events)
	results := make([]Result, n)
	for i, ev := range events {
		results[i].Event = ev
	}
	if n == 0 {
		return results
	}

	indeg, dependents := s.buildGraph(events)
	if hasCycle(indeg, dependents) {
		for i := range results {
			results[i].Err = fmt.Errorf("dependency cycle detected among events")
		}
		return results
	}

	var (
		mu        sync.Mutex
		cond      = sync.NewCond(&mu)
		ready     []int
		settled   = make([]bool, n)
		remaining = n
	)
	for i := 0; i < n; i++ {
		if indeg[i] == 0 {
			ready = append(ready, i)
		}
	}

	var wg sync.WaitGroup
	for w := 0; w < s.workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				mu.Lock()
				for len(ready) == 0 && remaining > 0 {
					cond.Wait()
				}
				if remaining == 0 {
					mu.Unlock()
					return
				}
				idx := ready[0]
				ready = ready[1:]
				mu.Unlock()

				err := apply(events[idx])

				mu.Lock()
				settled[idx] = true
				results[idx].Err = err
				remaining--
				if err == nil {
					// Release dependents whose last dependency just completed.
					for _, d := range dependents[idx] {
						indeg[d]--
						if indeg[d] == 0 {
							ready = append(ready, d)
						}
					}
				} else {
					// Transitively skip everything that (in)directly depends on
					// the failed event instead of replaying it into an FK error.
					queue := append([]int(nil), dependents[idx]...)
					for len(queue) > 0 {
						d := queue[0]
						queue = queue[1:]
						if settled[d] {
							continue
						}
						settled[d] = true
						results[d].Err = fmt.Errorf("skipped: dependency event #%d (%s %s/%s) failed: %v",
							events[idx].ID, events[idx].Op, events[idx].Table, events[idx].Key, err)
						remaining--
						queue = append(queue, dependents[d]...)
					}
				}
				cond.Broadcast()
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	return results
}

// buildGraph derives the event-level dependency DAG from the schema's FK
// metadata. It returns in-degrees and adjacency (dependency -> dependents).
func (s *Scheduler) buildGraph(events []Event) ([]int, [][]int) {
	type rowID struct {
		table, key string
	}
	upserts := make(map[rowID]int, len(events))
	deletes := make(map[rowID]int, len(events))
	for i, ev := range events {
		id := rowID{ev.Table, ev.Key}
		switch ev.Op {
		case OpInsert, OpUpdate:
			upserts[id] = i
		case OpDelete:
			deletes[id] = i
		}
	}

	n := len(events)
	deps := make([]map[int]bool, n)
	for i := range deps {
		deps[i] = make(map[int]bool)
	}

	addDep := func(event, dependsOn int) {
		if event != dependsOn {
			deps[event][dependsOn] = true
		}
	}

	for i, ev := range events {
		tbl, ok := s.schema.Table(ev.Table)
		if !ok {
			continue
		}
		switch ev.Op {
		case OpInsert, OpUpdate:
			// Parent rows referenced by this row must be written first.
			for _, fk := range tbl.FKs {
				parentKey, ok := ev.Row[fk.ChildColumn]
				if !ok {
					continue
				}
				if j, ok := upserts[rowID{fk.ParentTable, parentKey}]; ok {
					addDep(i, j)
				}
			}
		case OpDelete:
			// If the same row is re-written in this batch, delete after write.
			if j, ok := upserts[rowID{ev.Table, ev.Key}]; ok {
				addDep(i, j)
			}
		}
	}

	// Child-side pass for deletes: a parent delete waits for the deletes of
	// child rows whose before-image references the parent key.
	for i, ev := range events {
		if ev.Op != OpDelete {
			continue
		}
		tbl, ok := s.schema.Table(ev.Table)
		if !ok {
			continue
		}
		for _, fk := range tbl.FKs {
			parentKey, ok := ev.Row[fk.ChildColumn]
			if !ok {
				continue
			}
			if j, ok := deletes[rowID{fk.ParentTable, parentKey}]; ok {
				addDep(j, i) // parent delete j depends on child delete i
			}
		}
	}

	indeg := make([]int, n)
	dependents := make([][]int, n)
	for i, set := range deps {
		indeg[i] = len(set)
		for j := range set {
			dependents[j] = append(dependents[j], i)
		}
	}
	return indeg, dependents
}

// hasCycle reports whether the graph cannot be fully topo-sorted.
func hasCycle(indeg []int, dependents [][]int) bool {
	in := append([]int(nil), indeg...)
	var ready []int
	for i, d := range in {
		if d == 0 {
			ready = append(ready, i)
		}
	}
	count := 0
	for len(ready) > 0 {
		x := ready[0]
		ready = ready[1:]
		count++
		for _, d := range dependents[x] {
			in[d]--
			if in[d] == 0 {
				ready = append(ready, d)
			}
		}
	}
	return count != len(in)
}
