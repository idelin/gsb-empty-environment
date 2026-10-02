package leaserouter

import "fmt"

// KeyRange is a half-open key interval [Start, End).
// An empty End means the range is unbounded on the right, which covers
// the top of the key space ("...Z" and anything beyond).
type KeyRange struct {
	Start string
	End   string
}

// Contains reports whether key falls inside [Start, End).
func (r KeyRange) Contains(key string) bool {
	if key < r.Start {
		return false
	}
	return r.End == "" || key < r.End
}

// overlaps reports whether two half-open ranges share any key.
func overlaps(a, b KeyRange) bool {
	if b.End != "" && a.Start >= b.End {
		return false
	}
	if a.End != "" && b.Start >= a.End {
		return false
	}
	return true
}

func (r KeyRange) String() string {
	end := r.End
	if end == "" {
		end = "∞"
	}
	return fmt.Sprintf("[%s, %s)", r.Start, end)
}
