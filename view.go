package walrestore

import "iter"

// All returns a go1.23 range-over-func iterator over the live key/value pairs
// of the view in lexicographic key order. Every yielded value is an independent
// copy, so mutating it cannot affect the immutable store.
//
//	for key, value := range view.All() {
//	    ...
//	}
func (v *View) All() iter.Seq2[[]byte, []byte] {
	return func(yield func([]byte, []byte) bool) {
		for idx, key := range v.store.keys {
			ver, ok := v.store.versionAt(idx, v.seq)
			if !ok || ver.deleted {
				continue
			}
			value := append([]byte(nil), ver.value...)
			if !yield(key, value) {
				return
			}
		}
	}
}

// Count returns the number of live (non-deleted) keys visible in the view.
func (v *View) Count() int {
	count := 0
	for range v.All() {
		count++
	}
	return count
}
