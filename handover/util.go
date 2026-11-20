package handover

import (
	"fmt"
	"sort"
)

// errf builds a descriptive validation error.
func errf(format string, args ...any) error {
	return fmt.Errorf("handover: "+format, args...)
}

func sortSlice(s []Interval) {
	sort.Slice(s, func(i, j int) bool {
		if s[i].Start != s[j].Start {
			return s[i].Start < s[j].Start
		}
		return s[i].End < s[j].End
	})
}

// isPow2 reports whether v is a positive power of two.
func isPow2(v uint64) bool { return v&(v-1) == 0 && v != 0 }

// log2 returns the base-2 logarithm of a positive power of two.
func log2(v uint64) uint {
	var k uint
	for v > 1 {
		v >>= 1
		k++
	}
	return k
}
