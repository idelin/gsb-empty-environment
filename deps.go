package netsec

import "slices"

// depIndex is a read-only view of the producer/consumer edges of a trace.
type depIndex struct {
	// messages is the indexed trace, kept for edge traversal.
	messages []Message

	// producers maps each symbol to every message that produces it, in trace
	// order. Repeated producers are kept as separate entries.
	producers map[SymID][]int

	// consumers maps each symbol to every message that consumes it, in trace
	// order. Repeated consumption by one message appears once per occurrence.
	consumers map[SymID][]int

	// byID maps a message id to its position in the trace.
	byID map[int]int
}

func buildIndex(msgs []Message) depIndex {
	idx := depIndex{
		messages:  msgs,
		producers: make(map[SymID][]int),
		consumers: make(map[SymID][]int),
		byID:      make(map[int]int, len(msgs)),
	}
	for pos, msg := range msgs {
		idx.byID[msg.ID] = pos
		for _, sym := range msg.Produces {
			idx.producers[sym] = append(idx.producers[sym], msg.ID)
		}
		for _, sym := range msg.Consumes {
			idx.consumers[sym] = append(idx.consumers[sym], msg.ID)
		}
	}
	return idx
}

// closure returns the set of message ids that must be deleted together with
// roots so that no surviving message consumes a symbol without a surviving
// producer. Deletions cascade across connection boundaries.
//
// Repeated producers matter: a consumer is only forced into the closure once
// *all* producers of a consumed symbol are removed.
func (idx depIndex) closure(alive map[int]bool, roots []int) map[int]bool {
	removed := make(map[int]bool, len(roots))
	queue := append([]int(nil), roots...)
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if removed[id] || !alive[id] {
			continue
		}
		removed[id] = true

		for _, sym := range idx.messages[idx.byID[id]].Produces {
			for _, consumer := range idx.consumers[sym] {
				if removed[consumer] || !alive[consumer] {
					continue
				}
				if !idx.hasLivingProducer(alive, removed, sym, consumer) {
					queue = append(queue, consumer)
				}
			}
		}
	}
	return removed
}

// hasLivingProducer reports whether consumer can still obtain sym after the
// pending deletions. Only producers preceding the consumer can satisfy it,
// matching execution order in a live server.
func (idx depIndex) hasLivingProducer(alive, removed map[int]bool, sym SymID, consumer int) bool {
	consumerPos := idx.byID[consumer]
	for _, producer := range idx.producers[sym] {
		if idx.byID[producer] >= consumerPos {
			continue
		}
		if alive[producer] && !removed[producer] {
			return true
		}
	}
	return false
}

// danglingConsumers returns, in trace order, the (message id, consumed symbol)
// pairs that lose every preceding producer because of removed.
func (idx depIndex) danglingConsumers(alive, removed map[int]bool) []danglingRef {
	var refs []danglingRef
	for _, msg := range idx.messages {
		id := msg.ID
		if removed[id] {
			continue
		}
		pos := idx.byID[id]
		seen := map[SymID]bool{}
		for _, sym := range msg.Consumes {
			if seen[sym] {
				continue
			}
			seen[sym] = true
			living := false
			for _, producer := range idx.producers[sym] {
				if idx.byID[producer] < pos && alive[producer] && !removed[producer] {
					living = true
					break
				}
			}
			if !living {
				refs = append(refs, danglingRef{Message: id, Symbol: sym})
			}
		}
	}
	slices.SortFunc(refs, func(a, b danglingRef) int {
		if d := idx.byID[a.Message] - idx.byID[b.Message]; d != 0 {
			return d
		}
		return int(a.Symbol) - int(b.Symbol)
	})
	return refs
}

type danglingRef struct {
	Message int
	Symbol  SymID
}

// aliveSet returns the set of message ids present in msgs.
func aliveSet(msgs []Message) map[int]bool {
	alive := make(map[int]bool, len(msgs))
	for _, msg := range msgs {
		alive[msg.ID] = true
	}
	return alive
}

// removeMessages returns the messages outside removed, keeping trace order.
func removeMessages(msgs []Message, removed map[int]bool) []Message {
	out := make([]Message, 0, len(msgs)-len(removed))
	for _, msg := range msgs {
		if !removed[msg.ID] {
			out = append(out, msg)
		}
	}
	return out
}

// RepairFunc rewrites a surviving message whose consumes list contains symbols
// that lost every producer under a Repair policy deletion. dangling lists the
// affected symbols (each once). The function receives a private copy and must
// return the rewritten message; returning ok=false rejects the whole deletion
// candidate, which is the way callers express "this dependency cannot be
// repaired, delete the consumer instead".
type RepairFunc func(msg Message, dangling []SymID) (repaired Message, ok bool)

// DropConsuming is a RepairFunc that removes dangling symbols from Consumes.
// It models a caller that can satisfy the message by dropping the dependency
// (for example re-issuing a ticket instead of forwarding the original).
func DropConsuming(msg Message, dangling []SymID) (Message, bool) {
	drop := make(map[SymID]bool, len(dangling))
	for _, sym := range dangling {
		drop[sym] = true
	}
	fixed := msg.Clone()
	kept := fixed.Consumes[:0]
	for _, sym := range fixed.Consumes {
		if !drop[sym] {
			kept = append(kept, sym)
		}
	}
	fixed.Consumes = kept
	return fixed, true
}

// buildCascadeCandidate deletes the closure rooted at roots, so every surviving
// message still has a producer for each consumed symbol.
func buildCascadeCandidate(msgs []Message, idx depIndex, alive map[int]bool, roots []int) []Message {
	removed := idx.closure(alive, roots)
	return removeMessages(msgs, removed)
}

// buildRepairCandidate deletes exactly roots and asks repair to fix every
// message left with a dangling consumption. It returns ok=false if repair
// rejects any affected message. Originals are never mutated.
func buildRepairCandidate(msgs []Message, idx depIndex, alive map[int]bool, roots []int, repair RepairFunc) ([]Message, bool) {
	if repair == nil {
		return nil, false
	}
	removed := make(map[int]bool, len(roots))
	for _, id := range roots {
		removed[id] = true
	}
	refs := idx.danglingConsumers(alive, removed)
	if len(refs) == 0 {
		return removeMessages(msgs, removed), true
	}

	byMessage := make(map[int][]SymID)
	for _, ref := range refs {
		byMessage[ref.Message] = append(byMessage[ref.Message], ref.Symbol)
	}

	out := make([]Message, 0, len(msgs)-len(removed))
	for _, msg := range msgs {
		if removed[msg.ID] {
			continue
		}
		if dangling, ok := byMessage[msg.ID]; ok {
			rewritten, good := repair(msg.Clone(), dangling)
			if !good {
				return nil, false
			}
			out = append(out, rewritten)
			continue
		}
		out = append(out, msg)
	}
	return out, true
}

// closureOfOne is a convenience wrapper returning the sorted closure rooted at
// a single still-alive message.
func closureOfOne(idx depIndex, alive map[int]bool, root int) map[int]bool {
	return idx.closure(alive, []int{root})
}

// sortedIDs returns the ids of a set in trace order given the index.
func sortedIDs(idx depIndex, set map[int]bool) []int {
	ids := make([]int, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	slices.SortFunc(ids, func(a, b int) int { return idx.byID[a] - idx.byID[b] })
	return ids
}
