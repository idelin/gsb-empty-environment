package bootpolicy

import "sort"

func buildGenClasses(entries []Entry) []IntervalClass {
	boundSet := map[uint32]struct{}{}
	boundSet[0] = struct{}{}
	for i := range entries {
		boundSet[entries[i].GenLo] = struct{}{}
		if entries[i].GenHi != maxUint32 {
			boundSet[entries[i].GenHi+1] = struct{}{}
		}
	}
	bounds := sortedU32(boundSet)
	classes := make([]IntervalClass, 0, len(bounds))
	for idx, lo := range bounds {
		hi := uint32(maxUint32)
		tail := idx == len(bounds)-1
		if !tail {
			hi = bounds[idx+1] - 1
		}
		classes = append(classes, IntervalClass{Representative: lo, Lo: lo, Hi: hi, Tail: tail})
	}
	return classes
}

func sortedU32(set map[uint32]struct{}) []uint32 {
	out := make([]uint32, 0, len(set))
	for v := range set {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func buildTimeClasses(entries []Entry) []TimeClass {
	boundSet := map[uint64]struct{}{}
	for i := range entries {
		if entries[i].Kind == KindExempt && entries[i].NotAfter != 0 && entries[i].NotAfter != maxUint64 {
			boundSet[entries[i].NotAfter+1] = struct{}{}
		}
	}
	bounds := make([]uint64, 0, len(boundSet))
	for v := range boundSet {
		bounds = append(bounds, v)
	}
	sort.Slice(bounds, func(i, j int) bool { return bounds[i] < bounds[j] })

	classes := make([]TimeClass, 0, len(bounds)+1)
	if len(bounds) == 0 {
		classes = append(classes, TimeClass{Representative: 0, Lo: 0, Hi: maxUint64, Tail: true})
		return classes
	}
	classes = append(classes, TimeClass{Representative: 0, Lo: 0, Hi: bounds[0] - 1})
	for idx, lo := range bounds {
		hi := uint64(maxUint64)
		tail := idx == len(bounds)-1
		if !tail {
			hi = bounds[idx+1] - 1
		}
		classes = append(classes, TimeClass{Representative: lo, Lo: lo, Hi: hi, Tail: tail})
	}
	return classes
}

func buildParentClasses(entries []Entry) []ParentClass {
	type key struct {
		signer string
		label  string
	}
	scopes := map[key]*ParentScope{}
	unscoped := make([]int, 0)
	for i := range entries {
		if entries[i].Parent == nil {
			unscoped = append(unscoped, i)
			continue
		}
		k := key{entries[i].Parent.SignerID, entries[i].Parent.Label}
		scopes[k] = entries[i].Parent
	}
	keys := make([]key, 0, len(scopes))
	for k := range scopes {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].signer != keys[j].signer {
			return keys[i].signer < keys[j].signer
		}
		return keys[i].label < keys[j].label
	})

	classes := make([]ParentClass, 0, len(keys)+2)
	classes = append(classes, ParentClass{NilParent: true, UnscopedIndexes: append([]int(nil), unscoped...)})
	for _, k := range keys {
		scope := scopes[k]
		matched := make([]int, 0)
		for i := range entries {
			if entries[i].Parent != nil && entries[i].Parent.equal(scope) {
				matched = append(matched, i)
			}
		}
		classes = append(classes, ParentClass{
			Scope:           scope,
			MatchedIndexes:  matched,
			UnscopedIndexes: append([]int(nil), unscoped...),
		})
	}
	classes = append(classes, ParentClass{Wildcard: true, UnscopedIndexes: append([]int(nil), unscoped...)})
	return classes
}

func (s *ParentScope) equal(other *ParentScope) bool {
	if s == nil || other == nil {
		return s == other
	}
	return s.SignerID == other.SignerID && s.Label == other.Label
}

// buildPrefixClasses constructs the trie over the distinct label prefixes and
// emits, per trie node, the node subtree class and one class per outgoing
// edge region. An empty-prefix node is always present.
func buildPrefixClasses(entries []Entry) ([]string, []PrefixClass) {
	prefixSet := map[string]struct{}{"": {}}
	for i := range entries {
		prefixSet[entries[i].LabelPrefix] = struct{}{}
	}
	prefixes := make([]string, 0, len(prefixSet))
	for p := range prefixSet {
		prefixes = append(prefixes, p)
	}
	sort.Slice(prefixes, func(i, j int) bool {
		if len(prefixes[i]) != len(prefixes[j]) {
			return len(prefixes[i]) < len(prefixes[j])
		}
		return prefixes[i] < prefixes[j]
	})
	prefixIndex := map[string]int{}
	for i, p := range prefixes {
		prefixIndex[p] = i
	}

	nodes := map[string]*prefixNode{}
	for _, p := range prefixes {
		nodes[p] = &prefixNode{prefix: p}
	}
	for _, p := range prefixes {
		if p == "" {
			continue
		}
		parent := longestProperPrefixIn(p, prefixSet)
		nodes[parent].children = append(nodes[parent].children, nodes[p])
	}
	sortedNodes := make([]*prefixNode, 0, len(prefixes))
	for _, p := range prefixes {
		sortedNodes = append(sortedNodes, nodes[p])
	}

	classes := make([]PrefixClass, 0)
	for _, node := range sortedNodes {
		matched := matchingPrefixIndexes(prefixes, node.prefix, prefixIndex)
		classes = append(classes, PrefixClass{
			Representative:  node.prefix,
			IsNode:          true,
			Prefix:          node.prefix,
			MatchedPrefixes: matched,
		})
		if len(node.children) == 0 {
			continue
		}
		edgeBytes := map[byte]struct{}{}
		for _, ch := range node.children {
			edgeBytes[ch.prefix[len(node.prefix)]] = struct{}{}
		}
		otherByte, ok := unusedByte(edgeBytes)
		if !ok {
			// All 256 continuation bytes lead into child edges; the node has no
			// residual region, so no extra class is required.
			continue
		}
		classes = append(classes, PrefixClass{
			Representative:  node.prefix + string([]byte{otherByte}),
			IsNode:          false,
			Prefix:          node.prefix,
			MatchedPrefixes: matched,
		})
	}
	return prefixes, classes
}

func matchingPrefixIndexes(prefixes []string, label string, index map[string]int) []int {
	out := make([]int, 0)
	for _, p := range prefixes {
		if hasPrefix(label, p) {
			out = append(out, index[p])
		}
	}
	return out
}

func hasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

func longestProperPrefixIn(p string, set map[string]struct{}) string {
	for i := len(p) - 1; i >= 0; i-- {
		cand := p[:i]
		if _, ok := set[cand]; ok {
			return cand
		}
	}
	return ""
}

func unusedByte(used map[byte]struct{}) (byte, bool) {
	for b := 0; b < 256; b++ {
		if _, ok := used[byte(b)]; !ok {
			return byte(b), true
		}
	}
	return 0, false
}
