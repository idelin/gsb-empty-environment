package main

import (
	"fmt"
	"time"
)

// maxBound is the exclusive upper bound of the key space. "[" is the ASCII
// character immediately after 'Z', so the range [A, maxBound) covers every
// key beginning with an uppercase letter A-Z. It is rendered as "Z" when
// printing, so ranges appear exactly as [A-Z), [M-Z), etc.
const maxBound = "["

// KeyRange is a half-open lexicographic key range [Start, End).
type KeyRange struct {
	Start string // inclusive
	End   string // exclusive
}

// Contains reports whether key falls inside the range.
func (r KeyRange) Contains(key string) bool {
	if key < r.Start {
		return false
	}
	if r.End != "" && key >= r.End {
		return false
	}
	return true
}

func (r KeyRange) String() string {
	end := r.End
	if end == maxBound {
		end = "Z"
	}
	return fmt.Sprintf("[%s-%s)", r.Start, end)
}

// overlaps reports whether two ranges intersect.
func overlaps(a, b KeyRange) bool {
	return a.Start < b.End && b.Start < a.End
}

// Lease grants a node the exclusive right to serve a key range.
type Lease struct {
	Range   KeyRange
	NodeID  string
	Epoch   uint64
	Granted time.Time
	TTL     time.Duration
	valid   bool
}

// Valid reports whether the lease is still active (not revoked by a split
// and not expired).
func (l *Lease) Valid(now time.Time) bool {
	if !l.valid {
		return false
	}
	return l.TTL <= 0 || !now.After(l.Granted.Add(l.TTL))
}

func (l *Lease) String() string {
	state := "active"
	if !l.valid {
		state = "revoked"
	}
	return fmt.Sprintf("range=%s holder=%s epoch=%d %s", l.Range, l.NodeID, l.Epoch, state)
}
