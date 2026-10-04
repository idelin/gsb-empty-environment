package readrepair

// VClock is a vector clock mapping node IDs to logical counters.
type VClock map[string]uint64

// Copy returns a deep copy of the clock.
func (c VClock) Copy() VClock {
	out := make(VClock, len(c))
	for k, v := range c {
		out[k] = v
	}
	return out
}

// Tick increments the entry for node and returns the updated clock.
func (c VClock) Tick(node string) VClock {
	out := c.Copy()
	out[node]++
	return out
}

// Merge returns the element-wise maximum of both clocks.
func (c VClock) Merge(o VClock) VClock {
	out := c.Copy()
	for k, v := range o {
		if out[k] < v {
			out[k] = v
		}
	}
	return out
}

// Compare returns -1 if c strictly descends from o (c < o),
// 1 if c strictly dominates o (c > o), 0 if equal, and 2 if concurrent.
func (c VClock) Compare(o VClock) int {
	less, greater := false, false
	for k, v := range c {
		if ov, ok := o[k]; !ok || ov < v {
			greater = true
		} else if ov > v {
			less = true
		}
	}
	for k, v := range o {
		if _, ok := c[k]; !ok && v > 0 {
			less = true
		}
	}
	switch {
	case less && greater:
		return 2
	case less:
		return -1
	case greater:
		return 1
	default:
		return 0
	}
}

// Dominates reports whether c strictly descends from o... i.e. c > o.
func (c VClock) Dominates(o VClock) bool { return c.Compare(o) == 1 }

// Equal reports whether both clocks are identical.
func (c VClock) Equal(o VClock) bool { return c.Compare(o) == 0 }

// Concurrent reports whether neither clock descends from the other.
func (c VClock) Concurrent(o VClock) bool { return c.Compare(o) == 2 }
