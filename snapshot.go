// Package snapshot implements a Chandy-Lamport consistent global snapshot
// recorder. A cluster of processes continuously transfers money between each
// other over FIFO channels; at any moment one process may initiate a snapshot
// by emitting Marker messages, and the algorithm captures a consistent cut
// (local states + in-flight channel messages) without pausing the workload.
package snapshot

import (
	"math/rand"
	"sort"
	"time"
)

// Kind distinguishes application messages from control (Marker) messages.
type Kind int

const (
	Transfer Kind = iota // money transfer between processes
	Marker               // Chandy-Lamport marker
)

// Message travels over a directed FIFO channel between two processes.
type Message struct {
	Kind   Kind
	From   string
	Amount int // Transfer only
	SnapID int // Marker only
}

// SnapshotResult is the per-process output of a completed snapshot: the local
// state at record time plus all in-flight transfers recorded on each incoming
// channel before that channel's Marker arrived.
type SnapshotResult struct {
	ProcessID string
	Balance   int
	InFlight  map[string][]Message // keyed by sender id
}

// InFlightTotal sums the amounts of all recorded in-flight transfers.
func (r SnapshotResult) InFlightTotal() int {
	total := 0
	for _, msgs := range r.InFlight {
		for _, m := range msgs {
			total += m.Amount
		}
	}
	return total
}

type envelope struct {
	from string
	msg  Message
}

// Process is a node in the cluster. All mutable state is owned by its single
// event-loop goroutine, so no locks are required.
type Process struct {
	ID      string
	balance int

	inbox  map[string]chan Message // incoming FIFO channels, keyed by sender id
	outbox map[string]chan Message // outgoing FIFO channels, keyed by receiver id

	events    chan envelope
	trigger   chan int
	stopCh    chan struct{}
	collector chan<- SnapshotResult

	// snapshot bookkeeping
	recorded    bool
	reported    bool
	snapBalance int
	inFlight    map[string][]Message
	openChans   map[string]bool // incoming channels still awaiting a Marker

	rng *rand.Rand
}

// NewCluster builds a fully connected cluster with the given initial balances.
// Snapshot results are delivered on collector.
func NewCluster(balances map[string]int, collector chan<- SnapshotResult) []*Process {
	ids := make([]string, 0, len(balances))
	for id := range balances {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	byID := make(map[string]*Process, len(ids))
	for i, id := range ids {
		byID[id] = &Process{
			ID:        id,
			balance:   balances[id],
			inbox:     make(map[string]chan Message),
			outbox:    make(map[string]chan Message),
			events:    make(chan envelope, 4096),
			trigger:   make(chan int, 1),
			stopCh:    make(chan struct{}),
			collector: collector,
			rng:       rand.New(rand.NewSource(time.Now().UnixNano() + int64(i)*7919)),
		}
	}
	for _, a := range byID {
		for _, b := range byID {
			if a.ID == b.ID {
				continue
			}
			ch := make(chan Message, 4096)
			a.outbox[b.ID] = ch
			b.inbox[a.ID] = ch
		}
	}

	procs := make([]*Process, 0, len(ids))
	for _, id := range ids {
		procs = append(procs, byID[id])
	}
	return procs
}

// Start launches the process's forwarding and event-loop goroutines.
func (p *Process) Start() {
	for from, ch := range p.inbox {
		go func(from string, ch chan Message) {
			for {
				select {
				case msg := <-ch:
					select {
					case p.events <- envelope{from, msg}:
					case <-p.stopCh:
						return
					}
				case <-p.stopCh:
					return
				}
			}
		}(from, ch)
	}
	go p.run()
}

// Stop terminates the process's goroutines. Call at most once.
func (p *Process) Stop() { close(p.stopCh) }

// InitiateSnapshot asks this process to start a Chandy-Lamport snapshot.
func (p *Process) InitiateSnapshot(snapID int) { p.trigger <- snapID }

func (p *Process) run() {
	ticker := time.NewTicker(2 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-p.stopCh:
			return
		case <-ticker.C:
			p.maybeSendTransfer()
		case snapID := <-p.trigger:
			// Initiator rule: record local state, then send a Marker on
			// every outgoing channel, then record all incoming channels.
			p.recordState(snapID)
			p.maybeReport()
		case env := <-p.events:
			p.handle(env)
		}
	}
}

func (p *Process) maybeSendTransfer() {
	n := len(p.outbox)
	if n == 0 {
		return
	}
	idx := p.rng.Intn(n)
	i := 0
	var ch chan Message
	for _, c := range p.outbox {
		ch = c
		if i == idx {
			break
		}
		i++
	}
	amount := 1 + p.rng.Intn(10)
	if p.balance < amount {
		return
	}
	ch <- Message{Kind: Transfer, From: p.ID, Amount: amount}
	p.balance -= amount
}

func (p *Process) handle(env envelope) {
	msg := env.msg
	switch msg.Kind {
	case Transfer:
		p.balance += msg.Amount
		// Channel-state recording: transfers arriving on a channel after the
		// local state was recorded, but before that channel's Marker, are
		// in-flight and belong to the snapshot.
		if p.recorded && p.openChans[env.from] {
			p.inFlight[env.from] = append(p.inFlight[env.from], msg)
		}
	case Marker:
		if !p.recorded {
			// First Marker: record local state, send Markers downstream,
			// and start recording all other incoming channels.
			p.recordState(msg.SnapID)
		}
		// The channel the Marker arrived on is closed for recording.
		delete(p.openChans, env.from)
		p.maybeReport()
	}
}

func (p *Process) recordState(snapID int) {
	p.recorded = true
	p.snapBalance = p.balance
	p.inFlight = make(map[string][]Message)
	p.openChans = make(map[string]bool, len(p.inbox))
	for from := range p.inbox {
		p.openChans[from] = true
	}
	for _, ch := range p.outbox {
		ch <- Message{Kind: Marker, From: p.ID, SnapID: snapID}
	}
}

func (p *Process) maybeReport() {
	if !p.recorded || p.reported || len(p.openChans) != 0 {
		return
	}
	p.reported = true
	p.collector <- SnapshotResult{
		ProcessID: p.ID,
		Balance:   p.snapBalance,
		InFlight:  p.inFlight,
	}
}
