package register

// Node is a replica. Its only state is the (tag, value) pair kept on a
// simulated disk. Writes are write-through, so a crash loses nothing and
// a restart recovers exactly the state visible before the crash.
type Node struct {
	id   int
	sim  *Sim
	up   bool
	disk diskState
}

// diskState survives crashes and restarts.
type diskState struct {
	stored bool
	tag    Tag
	val    string
}

// ID returns the replica index.
func (n *Node) ID() int { return n.id }

// Up reports whether the replica is currently running.
func (n *Node) Up() bool { return n.up }

// Crash halts the replica. In-flight and future requests to it are
// answered negatively until Restart is called.
func (n *Node) Crash() { n.up = false }

// Restart brings the replica back with its disk contents intact.
func (n *Node) Restart() { n.up = true }

// Stored returns the durable (tag, value) pair, for tests and demos.
func (n *Node) Stored() (Tag, string, bool) {
	return n.disk.tag, n.disk.val, n.disk.stored
}

type requestKind int

const (
	kindGet requestKind = iota
	kindPut
)

// request is a client-to-replica message. reply is invoked exactly once,
// with ok=false if the replica is down.
type request struct {
	kind  requestKind
	tag   Tag
	val   string
	reply func(tag Tag, val string, stored bool, ok bool)
}

// deliver handles an incoming request. A put carrying an older tag than
// the one on disk is acknowledged but ignored, which makes the replica
// immune to message reordering and delayed duplicates.
func (n *Node) deliver(r request) {
	if !n.up {
		n.sim.sendReply(func() { r.reply(Tag{}, "", false, false) })
		return
	}
	switch r.kind {
	case kindGet:
		tag, val, stored := n.disk.tag, n.disk.val, n.disk.stored
		n.sim.sendReply(func() { r.reply(tag, val, stored, true) })
	case kindPut:
		if !n.disk.stored || n.disk.tag.Less(r.tag) {
			n.disk = diskState{stored: true, tag: r.tag, val: r.val}
		}
		n.sim.sendReply(func() { r.reply(Tag{}, "", false, true) })
	}
}
