package tbsim

// Packet is a unit of traffic; Size is in bytes (1 token == 1 byte).
type Packet struct {
	ID   int
	Flow string
	Size int64
}

// Arrival injects a packet into the model at a given tick.
type Arrival struct {
	Time int64
	Pkt  Packet
}

// Node is a tree node carrying an optional shared token bucket.
type Node struct {
	Name   string
	Bucket *Bucket
	Parent *Node
}

// Flow is a traffic flow attached to a leaf node. Its packets must satisfy
// every bucket on the path from the leaf up to the root.
type Flow struct {
	Name     string
	Leaf     *Node
	Priority int   // larger wins
	Weight   int   // smooth-WRR weight among same-priority ready flows
	BufCap   int64 // shaper queue capacity in bytes (shaping may delay, never exceed this)

	path   []*Bucket // live ancestor buckets, leaf-first
	queue  []queued
	qbytes int64
	cw     int64 // smooth-WRR current weight
	idx    int   // stable tie-break
}

type queued struct {
	pkt      Packet
	arrival  int64
	decision int64            // tick of the last wait evaluation
	waits    map[string]int64 // per-ancestor-bucket wait at decision
	readyAt  int64            // decision + max(waits)
}

// NewFlow wires a flow to a leaf and snapshots its ancestor bucket path.
func NewFlow(name string, leaf *Node, prio, weight int, bufCap int64, idx int) *Flow {
	f := &Flow{Name: name, Leaf: leaf, Priority: prio, Weight: weight, BufCap: bufCap, idx: idx}
	for n := leaf; n != nil; n = n.Parent {
		if n.Bucket != nil {
			f.path = append(f.path, n.Bucket)
		}
	}
	return f
}

// Path returns the live ancestor buckets (leaf-first). Every bucket on the
// path constrains the packet; the shaper wait is the max over all of them.
func (f *Flow) Path() []*Bucket { return f.path }

func (f *Flow) push(pkt Packet, now int64) {
	f.queue = append(f.queue, queued{pkt: pkt, arrival: now})
	f.qbytes += pkt.Size
}

func (f *Flow) pop() queued {
	head := f.queue[0]
	f.queue = f.queue[1:]
	f.qbytes -= head.pkt.Size
	return head
}

// cancel removes a queued (not yet emitted) packet. Tokens are only consumed
// at emit time, so cancelling touches no bucket and cannot refill anything.
func (f *Flow) cancel(id int) bool {
	for i, q := range f.queue {
		if q.pkt.ID == id {
			f.queue = append(f.queue[:i], f.queue[i+1:]...)
			f.qbytes -= q.pkt.Size
			return true
		}
	}
	return false
}

// admit checks static admission rules shared by shaper and reference.
func admit(f *Flow, size int64) string {
	for _, b := range f.path {
		if b.Cap < size {
			return "oversize" // can never pass: burst is bounded by capacity
		}
	}
	if f.qbytes+size > f.BufCap {
		return "buffer" // shaping may delay but never beyond the cache
	}
	return ""
}

// DropRecord logs a dropped packet.
type DropRecord struct {
	Pkt    Packet
	Time   int64
	Reason string
}

// EmitRecord logs a shaped packet with its full wait decomposition.
type EmitRecord struct {
	Pkt      Packet
	Arrival  int64
	Emit     int64
	Decision int64            // tick of the final wait evaluation
	ReadyAt  int64            // earliest tick all ancestor buckets allowed
	Waits    map[string]int64 // per-ancestor-bucket wait at Decision
}

// Delay is the total in-model delay.
func (e EmitRecord) Delay() int64 { return e.Emit - e.Arrival }

// TokenWait is the combined ancestor constraint: the max, not any single
// bucket's local wait.
func (e EmitRecord) TokenWait() int64 {
	var m int64
	for _, w := range e.Waits {
		if w > m {
			m = w
		}
	}
	return m
}

// PassRecord logs a policed packet that was let through.
type PassRecord struct {
	Pkt  Packet
	Time int64
}

// ControlOp is a mid-run control action applied identically to the
// event-hopping shaper and the per-tick reference.
type ControlOp struct {
	Time   int64
	Kind   string // "setrate" | "setweight" | "cancel"
	Bucket string
	Flow   string
	RateN  int64 // numerator of the new rate
	RateD  int64 // denominator of the new rate
	Weight int
	PktID  int
}
