// Package chandy implements the Chandy-Lamport global snapshot algorithm.
//
// Processes exchange transfer messages over FIFO channels. Any process may
// initiate a snapshot by broadcasting a Marker; on first seeing a Marker a
// process records its local state and starts recording in-flight messages on
// its other incoming channels until a Marker arrives on each of them. The
// union of all local states plus all recorded in-flight messages forms a
// consistent global cut without pausing the system.
package chandy

import (
	"math/rand"
	"sync"
	"time"
)

// MsgKind distinguishes application transfers from snapshot markers.
type MsgKind int

const (
	KindTransfer MsgKind = iota
	KindMarker
)

// Message is the single envelope carried by every channel.
type Message struct {
	From, To int
	Kind     MsgKind
	Amount   int // KindTransfer only
	SnapID   int // KindMarker only
}

// Snapshot is the per-process result of one completed snapshot round.
type Snapshot struct {
	ProcessID int
	SnapID    int
	Balance   int               // local state recorded at marker time
	InFlight  map[int][]Message // sender -> transfers recorded in transit
}

// Total returns the balance plus all in-flight amounts captured.
func (s Snapshot) Total() int {
	t := s.Balance
	for _, msgs := range s.InFlight {
		for _, m := range msgs {
			t += m.Amount
		}
	}
	return t
}

// Process is a node in the distributed system.
type Process struct {
	id       int
	inbox    chan Message
	outgoing map[int]chan<- Message
	incoming []int

	mu           sync.Mutex
	balance      int
	recording    bool
	snapID       int
	localState   int
	markerSeen   map[int]bool
	channelState map[int][]Message
	resultCh     chan Snapshot
}

// NewProcess creates a process with the given id and initial balance.
// incoming lists the peer ids that hold a channel into this process.
func NewProcess(id, balance int, incoming []int) *Process {
	return &Process{
		id:       id,
		balance:  balance,
		inbox:    make(chan Message, 4096),
		outgoing: make(map[int]chan<- Message),
		incoming: append([]int(nil), incoming...),
		resultCh: make(chan Snapshot, 1),
	}
}

// ID returns the process id.
func (p *Process) ID() int { return p.id }

// Connect wires this process's outgoing channel to peer's inbox.
// Each link is a single Go channel, which guarantees FIFO delivery.
func (p *Process) Connect(peer *Process) {
	p.outgoing[peer.id] = peer.inbox
}

// Balance returns the current local balance.
func (p *Process) Balance() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.balance
}

// Result returns the channel on which the completed snapshot is delivered.
func (p *Process) Result() <-chan Snapshot { return p.resultCh }

// Run processes incoming messages until stop is closed.
func (p *Process) Run(stop <-chan struct{}, wg *sync.WaitGroup) {
	defer wg.Done()
	for {
		select {
		case <-stop:
			return
		case m := <-p.inbox:
			p.handle(m)
		}
	}
}

// Transfer atomically debits the local balance and enqueues a transfer
// message on the outgoing channel to `to`. Returns false if funds are
// insufficient or the peer is unknown.
func (p *Process) Transfer(to, amount int) bool {
	p.mu.Lock()
	ch, ok := p.outgoing[to]
	if !ok || p.balance < amount {
		p.mu.Unlock()
		return false
	}
	p.balance -= amount
	p.mu.Unlock()
	ch <- Message{From: p.id, To: to, Kind: KindTransfer, Amount: amount}
	return true
}

// InitiateSnapshot starts a new snapshot round at this process.
func (p *Process) InitiateSnapshot(snapID int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.recording {
		return
	}
	p.startSnapshotLocked(snapID)
	p.maybeFinishLocked()
}

func (p *Process) handle(m Message) {
	p.mu.Lock()
	defer p.mu.Unlock()
	switch m.Kind {
	case KindTransfer:
		// Recorded only if the snapshot started and the marker has not
		// yet arrived on this incoming channel: the message was in
		// transit on the consistent cut.
		if p.recording && !p.markerSeen[m.From] {
			p.channelState[m.From] = append(p.channelState[m.From], m)
		}
		p.balance += m.Amount
	case KindMarker:
		if !p.recording {
			p.startSnapshotLocked(m.SnapID)
		}
		p.markerSeen[m.From] = true
		p.maybeFinishLocked()
	}
}

// startSnapshotLocked records the local state and floods markers on all
// outgoing channels. Caller must hold p.mu.
func (p *Process) startSnapshotLocked(snapID int) {
	p.recording = true
	p.snapID = snapID
	p.localState = p.balance
	p.markerSeen = make(map[int]bool, len(p.incoming))
	p.channelState = make(map[int][]Message, len(p.incoming))
	marker := Message{From: p.id, Kind: KindMarker, SnapID: snapID}
	for _, ch := range p.outgoing {
		ch <- marker
	}
}

// maybeFinishLocked emits the snapshot once markers arrived on every
// incoming channel. Caller must hold p.mu.
func (p *Process) maybeFinishLocked() {
	if !p.recording || len(p.markerSeen) != len(p.incoming) {
		return
	}
	p.recording = false
	p.resultCh <- Snapshot{
		ProcessID: p.id,
		SnapID:    p.snapID,
		Balance:   p.localState,
		InFlight:  p.channelState,
	}
}

// ConnectDelay wires this process's outgoing channel to peer through a
// forwarding goroutine that adds up to maxDelay of random latency per
// message, modelling a real network link. Delivery remains FIFO. The
// forwarder exits when stop is closed.
func (p *Process) ConnectDelay(peer *Process, maxDelay time.Duration, stop <-chan struct{}) {
	q := make(chan Message, 4096)
	p.outgoing[peer.id] = q
	rng := rand.New(rand.NewSource(int64(p.id*1000+peer.id)*2654435761 + 1))
	go func() {
		for {
			select {
			case <-stop:
				return
			case m := <-q:
				if maxDelay > 0 {
					time.Sleep(time.Duration(rng.Int63n(int64(maxDelay))))
				}
				select {
				case peer.inbox <- m:
				case <-stop:
					return
				}
			}
		}
	}()
}
