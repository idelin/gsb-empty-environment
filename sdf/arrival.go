package sdf

// arrivalCursor indexes the periodic run-length input stream of one edge.
//
// The cursor stores the next token's ordinal within the infinite stream. All
// quantities are derived arithmetically from the run descriptor, so gaps up to
// 2^45 cost O(1) instead of being walked tick by tick.
type arrivalCursor struct {
	in     Input
	next   int64 // ordinal of next token within the current period (0..Count-1)
	period int64 // number of elapsed periods
}

func newCursor(in Input) arrivalCursor {
	return arrivalCursor{in: in}
}

// has reports whether another token exists (the stream is infinite).
func (c *arrivalCursor) has() bool { return true }

// nextTime returns the absolute arrival time of the next token without
// consuming it.
func (c *arrivalCursor) nextTime() int64 {
	return c.timeOf(c.period, c.next)
}

// advance consumes one token and returns its arrival time.
func (c *arrivalCursor) advance() int64 {
	t := c.nextTime()
	c.next++
	if c.next >= c.in.Count {
		c.next = 0
		c.period++
	}
	return t

}

// timeOf maps (period, index-within-period) to an absolute time. index is
// split into run number and position inside the run.
func (c *arrivalCursor) timeOf(period, idx int64) int64 {
	in := c.in
	r := idx
	// Tokens are packed per run: run k (0-based) starts at FirstTime + k*Gap.
	// With Runs runs and Count tokens, each run holds floor/ceil Count/Runs;
	// the canonical packing is ceil for the first remainder runs.
	base, rem := divmod(in.Count, in.Runs)
	runNo := int64(0)
	pos := r
	if rem != 0 {
		head := base + 1
		if r < head*rem {
			runNo = r / head
			pos = r - runNo*head
		} else {
			r2 := r - head*rem
			runNo = rem + r2/base
			pos = r2 - (runNo-rem)*base
		}
	} else {
		runNo = r / base
		pos = r - runNo*base
	}
	return absArrival(in, period, runNo, pos)
}

// absArrival computes FirstTime + period*Period + runNo*Gap + pos with int64
// overflow protection, saturating at the maximum representable time.
func absArrival(in Input, period, runNo, pos int64) int64 {
	t, ok := add64(in.FirstTime, period*in.Period)
	if !ok {
		return 1<<63 - 1
	}
	if t, ok = add64(t, runNo*in.Gap); !ok {
		return 1<<63 - 1
	}
	if t, ok = add64(t, pos); !ok {
		return 1<<63 - 1
	}
	return t
}

// runOf returns the run number and in-run position of an index in [0,Count).
func runPosition(in Input, idx int64) (runNo, pos int64) {
	base, rem := divmod(in.Count, in.Runs)
	if rem != 0 {
		head := base + 1
		if idx < head*rem {
			runNo = idx / head
			return runNo, idx - runNo*head
		}
		idx -= head * rem
		runNo = rem + idx/base
		return runNo, idx - (runNo-rem)*base
	}
	runNo = idx / base
	return runNo, idx - runNo*base
}

// phase returns time modulo the input period (for snapshot keys).
func (c *arrivalCursor) phase(now int64) int64 {
	r := now - c.in.FirstTime
	if r < 0 {
		return r
	}
	return r % c.in.Period
}

func divmod(a, b int64) (q, r int64) {
	q = a / b
	r = a - q*b
	return q, r
}
