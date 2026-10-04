package paxos

import "errors"

// PersistentState is the acceptor/learner state that must survive crashes:
// the maximum promised ballot, the per-slot accepted (ballot, value) table,
// and the chosen values learned so far.
type PersistentState struct {
	MaxPromise Ballot
	Accepted   map[int]AcceptedEntry
	Chosen     map[int]string
}

// ErrDiskFault is returned by Disk.Save while a fault is injected.
var ErrDiskFault = errors.New("paxos: disk write failed")

// Disk is a simulated durable store. A node crash/restart does not affect
// the disk; an injected fault makes Save fail (modeling persistence failure).
type Disk struct {
	state PersistentState
	fault bool
}

func NewDisk() *Disk {
	return &Disk{state: PersistentState{
		Accepted: map[int]AcceptedEntry{},
		Chosen:   map[int]string{},
	}}
}

func (d *Disk) SetFault(f bool) { d.fault = f }

func (d *Disk) Load() PersistentState { return cloneState(d.state) }

func (d *Disk) Save(s PersistentState) error {
	if d.fault {
		return ErrDiskFault
	}
	d.state = cloneState(s)
	return nil
}

func cloneState(s PersistentState) PersistentState {
	out := PersistentState{
		MaxPromise: s.MaxPromise,
		Accepted:   make(map[int]AcceptedEntry, len(s.Accepted)),
		Chosen:     make(map[int]string, len(s.Chosen)),
	}
	for k, v := range s.Accepted {
		out.Accepted[k] = v
	}
	for k, v := range s.Chosen {
		out.Chosen[k] = v
	}
	return out
}
