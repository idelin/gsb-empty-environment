package register

// Client issues read and write operations against the cluster. Client
// IDs are unique per Sim, which makes every version tag unique.
type Client struct {
	id  int
	sim *Sim
}

// ID returns the cluster-wide unique client ID.
func (c *Client) ID() int { return c.id }

// Op is one in-flight register operation (the ABD two-phase protocol):
//
//	write: phase 1 reads the max tag from a majority, phase 2 stores
//	       (maxVer+1, clientID, value) on a majority.
//	read:  phase 1 reads the max (tag, value) from a majority, phase 2
//	       writes that pair back to a majority (read repair) so that no
//	       later quorum can observe an older value.
//
// An operation reports OK only after a majority has acknowledged; when
// no majority is reachable it finishes with OK=false ("not completed")
// instead of pretending success.
type Op struct {
	client *Client
	write  bool
	value  string

	phase   int
	pending int
	oks     int

	maxTag Tag
	maxVal string
	found  bool

	// Done is set when the operation has terminated.
	Done bool
	// OK reports whether a majority was reached and the operation
	// completed. !OK means "not completed", never a false success.
	OK bool
	// Found reports (reads only) whether any value was ever written.
	Found bool
	// Result is the value returned by a completed read.
	Result string
	// Tag is the version tag written or read back.
	Tag Tag
}

// WriteAsync starts an asynchronous write of v.
func (c *Client) WriteAsync(v string) *Op {
	op := &Op{client: c, write: true, value: v}
	op.startPhase1()
	return op
}

// ReadAsync starts an asynchronous read.
func (c *Client) ReadAsync() *Op {
	op := &Op{client: c}
	op.startPhase1()
	return op
}

// Write runs a write to completion on the simulator clock.
func (c *Client) Write(v string) *Op {
	op := c.WriteAsync(v)
	c.sim.RunUntil(func() bool { return op.Done })
	return op
}

// Read runs a read to completion on the simulator clock.
func (c *Client) Read() *Op {
	op := c.ReadAsync()
	c.sim.RunUntil(func() bool { return op.Done })
	return op
}

func (op *Op) sim() *Sim { return op.client.sim }

// startPhase1 broadcasts a get to all replicas. Every request is
// eventually answered positively or negatively, so the phase always
// terminates.
func (op *Op) startPhase1() {
	op.phase = 1
	op.pending = op.sim().NumNodes()
	op.oks = 0
	for _, n := range op.sim().Nodes() {
		op.sim().sendRequest(n, request{kind: kindGet, reply: op.onPhase1Reply})
	}
}

func (op *Op) onPhase1Reply(tag Tag, val string, stored bool, ok bool) {
	if op.Done || op.phase != 1 {
		return
	}
	op.pending--
	if ok {
		op.oks++
		if stored && (!op.found || op.maxTag.Less(tag)) {
			op.maxTag, op.maxVal, op.found = tag, val, true
		}
	}
	if op.pending > 0 {
		return
	}
	if op.oks < op.sim().Quorum() {
		op.finish(false)
		return
	}
	if op.write {
		// Unique tag: bump the highest version seen and break ties
		// with this client's unique ID.
		op.maxTag = Tag{Ver: op.maxTag.Ver + 1, Writer: op.client.id}
		op.maxVal = op.value
		op.found = true
		op.startPhase2()
		return
	}
	if !op.found {
		// Nothing was ever written; no write-back needed.
		op.finish(true)
		return
	}
	// Read repair: write the highest (tag, value) back to a majority
	// before returning, so a stale minority can never make a later
	// read regress.
	op.startPhase2()
}

// startPhase2 broadcasts a put of (maxTag, maxVal) to all replicas.
func (op *Op) startPhase2() {
	op.phase = 2
	op.pending = op.sim().NumNodes()
	op.oks = 0
	tag, val := op.maxTag, op.maxVal
	for _, n := range op.sim().Nodes() {
		op.sim().sendRequest(n, request{kind: kindPut, tag: tag, val: val, reply: op.onPhase2Reply})
	}
}

func (op *Op) onPhase2Reply(_ Tag, _ string, _ bool, ok bool) {
	if op.Done || op.phase != 2 {
		return
	}
	op.pending--
	if ok {
		op.oks++
	}
	if op.oks >= op.sim().Quorum() {
		op.finish(true)
		return
	}
	if op.pending == 0 {
		op.finish(false)
	}
}

func (op *Op) finish(ok bool) {
	op.Done = true
	op.OK = ok
	if ok {
		op.Tag = op.maxTag
		if !op.write {
			op.Found = op.found
			op.Result = op.maxVal
		}
	}
}
