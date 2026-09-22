package sim

import "sort"

// Cluster coordinates writes and reads across replica nodes. Writes are
// sequenced by a single counter so versions are totally ordered.
type Cluster struct {
	sim   *Sim
	net   *Network
	Nodes []*Node
	byID  map[string]*Node
	ver   uint64

	// Sched owns all repairs; repairs are detached from the read lifecycle.
	Sched *RepairScheduler

	// ReadTimeout is how long a read waits for replica responses.
	ReadTimeout int64
}

func NewCluster(s *Sim, n *Network, nodes ...*Node) *Cluster {
	c := &Cluster{
		sim:         s,
		net:         n,
		Nodes:       nodes,
		byID:        map[string]*Node{},
		ReadTimeout: 50,
	}
	for _, nd := range nodes {
		c.byID[nd.ID] = nd
	}
	c.Sched = NewRepairScheduler(s, n, c.byID)
	return c
}

// Write assigns the next version and propagates the value to all replicas.
// Delivery (or loss) is up to the network.
func (c *Cluster) Write(key, val string) uint64 {
	c.ver++
	v := VersionedValue{Val: val, Ver: c.ver}
	for _, nd := range c.Nodes {
		node := nd
		c.net.Send("client", node.ID, "write", func() {
			node.Apply(key, v)
		})
	}
	return v.Ver
}

// ReadHandle represents an in-flight read. Cancelling it only suppresses the
// completion callback; repairs already committed to the scheduler are owned
// by the scheduler and are never revoked by a cancel.
type ReadHandle struct {
	cancelled bool
}

// Cancel aborts the read's callback. Committed repairs are unaffected.
func (h *ReadHandle) Cancel() { h.cancelled = true }

// ReadAsync reads key from every replica. When enough responses arrive (or
// the read times out) it reports the newest value and commits repairs for
// every replica that is stale or failed to respond.
func (c *Cluster) ReadAsync(key string, onDone func(v VersionedValue, ok bool)) *ReadHandle {
	h := &ReadHandle{}
	responded := map[string]VersionedValue{}
	haveValue := map[string]bool{}
	finished := false

	finish := func() {
		if finished {
			return
		}
		finished = true
		var best VersionedValue
		haveBest := false
		for id, v := range responded {
			if haveValue[id] && (!haveBest || v.Ver > best.Ver) {
				best, haveBest = v, true
			}
		}
		if haveBest {
			var targets []string
			for _, nd := range c.Nodes {
				v, seen := responded[nd.ID]
				switch {
				case !seen:
					// No response: replica is down or the message was
					// lost; it may be stale, so repair it too.
					targets = append(targets, nd.ID)
				case !haveValue[nd.ID] || v.Ver < best.Ver:
					targets = append(targets, nd.ID)
				}
			}
			if len(targets) > 0 {
				sort.Strings(targets)
				c.sim.Logf("read: key=%q newest=v%d stale=%v -> commit repair", key, best.Ver, targets)
				c.Sched.Commit(key, best, targets)
			}
		}
		if !h.cancelled && onDone != nil {
			onDone(best, haveBest)
		}
	}

	for _, nd := range c.Nodes {
		node := nd
		c.net.Send("client", node.ID, "read", func() {
			if node.Down {
				return
			}
			v, ok := node.Get(key)
			c.net.Send(node.ID, "client", "read-resp", func() {
				if finished {
					return
				}
				responded[node.ID] = v
				haveValue[node.ID] = ok
				if len(responded) == len(c.Nodes) {
					finish()
				}
			})
		})
	}
	c.sim.After(c.ReadTimeout, finish)
	return h
}
