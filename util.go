package netsec

import "sort"

// sortBoxes orders boxes by their canonical bound sequence so that digest
// computation is independent of the caller's slice order.
func sortBoxes(boxes []Box) {
	sort.Slice(boxes, func(i, j int) bool {
		return boxLess(boxes[i], boxes[j])
	})
}

func boxLess(a, b Box) bool {
	for k := 0; k < len(a.Lo) && k < len(b.Lo); k++ {
		if a.Lo[k] != b.Lo[k] {
			return a.Lo[k] < b.Lo[k]
		}
		if a.Hi[k] != b.Hi[k] {
			return a.Hi[k] < b.Hi[k]
		}
	}
	return len(a.Lo) < len(b.Lo)
}
