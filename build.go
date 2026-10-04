package snapdelta

import (
	"fmt"
	"sort"
)

// FullRoot is the reference implementation: it recomputes the root of a
// complete snapshot map from scratch. Tests cross-check every verified
// delta against a full rebuild.
func FullRoot(m map[uint16][]byte) Hash {
	keys := sortedKeys(m)
	return subtreeHash(m, keys, 0, 0)
}

func sortedKeys(m map[uint16][]byte) []uint16 {
	keys := make([]uint16, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	return keys
}

// subtreeHash hashes the subtree rooted at (depth, prefix) using the
// present keys (sorted slice restricted to that subtree).
func subtreeHash(m map[uint16][]byte, keys []uint16, depth uint, prefix uint32) Hash {
	if len(keys) == 0 {
		return defaultHash[Depth-depth]
	}
	if depth == Depth {
		return leafHash(keys[0], m[keys[0]])
	}
	split := (prefix<<1 | 1) << (Depth - depth - 1)
	i := sort.Search(len(keys), func(i int) bool { return uint32(keys[i]) >= split })
	left := subtreeHash(m, keys[:i], depth+1, prefix<<1)
	right := subtreeHash(m, keys[i:], depth+1, prefix<<1|1)
	return nodeHash(left, right)
}

// BuildDelta applies ops to the snapshot m (limited to keys in
// [rangeStart, rangeEnd]) and produces the delta plus the proof a
// receiver needs to verify the transition, along with the updated map.
// It is the reference prover: it owns the full map and computes proof
// nodes by full traversal.
func BuildDelta(m map[uint16][]byte, oldVersion uint64, ops []Op, rangeStart, rangeEnd uint16) (*Delta, *Proof, map[uint16][]byte, error) {
	if rangeStart > rangeEnd {
		return nil, nil, nil, fmt.Errorf("snapdelta: invalid range [%d, %d]", rangeStart, rangeEnd)
	}
	touched := make(map[uint16]bool)
	for i, op := range ops {
		if op.Type != OpPut && op.Type != OpDelete {
			return nil, nil, nil, fmt.Errorf("snapdelta: op %d has unknown type %d", i, op.Type)
		}
		if op.Key < rangeStart || op.Key > rangeEnd {
			return nil, nil, nil, fmt.Errorf("snapdelta: op key %d outside range [%d, %d]", op.Key, rangeStart, rangeEnd)
		}
		touched[op.Key] = true
	}

	// Apply ops to a copy of the map.
	newMap := make(map[uint16][]byte, len(m))
	for k, v := range m {
		newMap[k] = v
	}
	for _, op := range ops {
		if op.Type == OpPut {
			newMap[op.Key] = op.Value
		} else {
			delete(newMap, op.Key)
		}
	}

	// Old entries for touched keys (present ones only), sorted.
	touchedSorted := make([]uint16, 0, len(touched))
	for k := range touched {
		touchedSorted = append(touchedSorted, k)
	}
	sort.Slice(touchedSorted, func(i, j int) bool { return touchedSorted[i] < touchedSorted[j] })
	var oldEntries []Entry
	for _, k := range touchedSorted {
		if v, ok := m[k]; ok {
			oldEntries = append(oldEntries, Entry{Key: k, Value: v})
		}
	}

	// Inherited blocks: for every touched key, walk its root path and
	// pin each sibling subtree that contains no touched key and is not
	// canonically empty.
	allKeys := sortedKeys(m)
	nodeSet := make(map[nodeID]Hash)
	for _, k := range touchedSorted {
		for d := uint(1); d <= Depth; d++ {
			sibIdx := (uint32(k) >> (Depth - d)) ^ 1
			lo := sibIdx << (Depth - d)
			hi := lo + (uint32(1) << (Depth - d))
			if anyTouchedInRange(touchedSorted, lo, hi) {
				continue
			}
			sub := keysInRange(allKeys, lo, hi)
			h := subtreeHash(m, sub, d, sibIdx)
			if h != defaultHash[Depth-d] {
				nodeSet[nodeID{uint8(d), sibIdx}] = h
			}
		}
	}
	if len(touched) == 0 {
		// Empty delta: pin the whole tree with a single root node.
		if r := FullRoot(m); r != defaultHash[Depth] {
			nodeSet[nodeID{0, 0}] = r
		}
	}
	nodes := make([]ProofNode, 0, len(nodeSet))
	for id, h := range nodeSet {
		nodes = append(nodes, ProofNode{Depth: id.depth, Index: id.index, Hash: h})
	}
	sort.Slice(nodes, func(i, j int) bool {
		if nodes[i].Depth != nodes[j].Depth {
			return nodes[i].Depth < nodes[j].Depth
		}
		return nodes[i].Index < nodes[j].Index
	})

	proof := &Proof{
		OldVersion: oldVersion,
		NewVersion: oldVersion + 1,
		RangeStart: rangeStart,
		RangeEnd:   rangeEnd,
		OldEntries: oldEntries,
		Nodes:      nodes,
	}
	delta := &Delta{NewRoot: FullRoot(newMap), Ops: append([]Op(nil), ops...)}
	return delta, proof, newMap, nil
}

func anyTouchedInRange(touchedSorted []uint16, lo, hi uint32) bool {
	i := sort.Search(len(touchedSorted), func(i int) bool { return uint32(touchedSorted[i]) >= lo })
	return i < len(touchedSorted) && uint32(touchedSorted[i]) < hi
}

func keysInRange(sortedAll []uint16, lo, hi uint32) []uint16 {
	from := sort.Search(len(sortedAll), func(i int) bool { return uint32(sortedAll[i]) >= lo })
	to := sort.Search(len(sortedAll), func(i int) bool { return uint32(sortedAll[i]) >= hi })
	return sortedAll[from:to]
}
