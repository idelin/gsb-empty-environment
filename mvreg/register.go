package mvreg

import "sort"

// Item is one concurrent value with the exact version that created it.
type Item[T any] struct {
	Value  T
	Origin string
	Nonce  int64
	Vector VersionVector
}

// Register is a multi-value register replicated at ReplicaID.
type Register[T any] struct {
	ReplicaID string
	clock     int64
	context   VersionVector
	items     []Item[T]
}

func New[T any](replicaID string) *Register[T] {
	return &Register[T]{
		ReplicaID: replicaID,
		clock:     0,
		context:   VersionVector{},
	}
}

// Read returns causally maximal values in deterministic origin/nonce order.
func (r *Register[T]) Read() []Item[T] {
	r.gc()
	return cloneItems(r.items)
}

// Write replaces every value this replica has observed and creates a new version.
func (r *Register[T]) Write(value T) Item[T] {
	r.gc()
	r.clock++
	vector := cloneVector(r.context)
	vector[r.ReplicaID] = r.clock
	item := Item[T]{
		Value:  value,
		Origin: r.ReplicaID,
		Nonce:  r.clock,
		Vector: vector,
	}
	r.context = cloneVector(vector)
	r.items = []Item[T]{cloneItem(item)}
	return item
}

// Resolve explicitly merges observed values and records the merge in the clock.
func (r *Register[T]) Resolve(value T) Item[T] {
	return r.Write(value)
}

// Merge incorporates a remote state without advancing the local clock.
func (r *Register[T]) Merge(other *Register[T]) {
	if other == nil {
		return
	}
	r.MergeSnapshot(other.Snapshot())
}

// MergeSnapshot incorporates a point-in-time copy from another replica.
func (r *Register[T]) MergeSnapshot(snapshot Snapshot[T]) {
	mergedContext := mergeVectors(r.context, cloneVector(snapshot.Context))
	mergedItems := append(cloneItems(r.items), cloneItems(snapshot.Items)...)

	candidate := &Register[T]{
		ReplicaID: r.ReplicaID,
		clock:     r.clock,
		context:   mergedContext,
		items:     mergedItems,
	}
	candidate.gc()

	r.context = mergedContext
	r.items = candidate.items
}

// Snapshot returns a deep copy suitable for asynchronous network delivery.
func (r *Register[T]) Snapshot() Snapshot[T] {
	r.gc()
	return Snapshot[T]{
		ReplicaID: r.ReplicaID,
		Clock:     r.clock,
		Context:   cloneVector(r.context),
		Items:     cloneItems(r.items),
	}
}

// Snapshot is an immutable, marshal-friendly representation of register state.
type Snapshot[T any] struct {
	ReplicaID string
	Clock     int64
	Context   VersionVector
	Items     []Item[T]
}

func (r *Register[T]) gc() {
	type itemKey struct {
		origin string
		nonce  int64
	}
	seen := make(map[itemKey]bool, len(r.items))
	items := make([]Item[T], 0, len(r.items))
	for _, candidate := range r.items {
		key := itemKey{candidate.Origin, candidate.Nonce}
		if seen[key] {
			continue
		}
		seen[key] = true

		dominated := false
		for _, other := range r.items {
			if sameItem(candidate, other) {
				continue
			}
			if compareVectors(candidate.Vector, other.Vector) == before {
				dominated = true
				break
			}
		}
		if !dominated {
			items = append(items, cloneItem(candidate))
		}
	}
	sortItems(items)
	r.items = items
}

func sameItem[T any](a, b Item[T]) bool {
	return a.Origin == b.Origin && a.Nonce == b.Nonce
}

func cloneItem[T any](item Item[T]) Item[T] {
	item.Vector = cloneVector(item.Vector)
	return item
}

func cloneItems[T any](items []Item[T]) []Item[T] {
	out := make([]Item[T], len(items))
	for i, item := range items {
		out[i] = cloneItem(item)
	}
	return out
}

func sortItems[T any](items []Item[T]) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].Origin != items[j].Origin {
			return items[i].Origin < items[j].Origin
		}
		return items[i].Nonce < items[j].Nonce
	})
}
