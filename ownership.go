// Package netsec provides a tenant ownership model for Go process hosts.
//
// Identity is captured at spawn time as an immutable tenant token
// (tenant ID + generation). Later process-group or session changes
// (setpgid, setsid) never change ownership. Cleanup freezes new
// spawns first, then terminates the full reachable descendant set and
// waits until every member's exit is confirmed.
package netsec

import (
	"errors"
	"fmt"
	"sync"
)

// Token is the immutable ownership identity inherited at creation time.
// A new generation of the same tenant is a distinct identity: stale
// cleanup for an old generation must never match it.
type Token struct {
	Tenant string
	Gen    uint64
}

func (t Token) String() string { return fmt.Sprintf("%s#%d", t.Tenant, t.Gen) }

// Instance identifies one incarnation of a PID. PIDs are reused by the
// OS, so ownership and signals are always resolved against (PID, Seq),
// never against a bare PID.
type Instance struct {
	PID int
	Seq uint64
}

// IsZero reports whether i refers to no process (e.g. an external or
// unknown parent outside the tracked tree).
func (i Instance) IsZero() bool { return i.PID == 0 && i.Seq == 0 }

// ErrFrozen is returned by Spawn when the token's generation has been
// frozen by cleanup: the host must refuse to fork new processes for it.
var ErrFrozen = errors.New("netsec: tenant generation frozen, spawn refused")

// ErrUnknownInstance is returned when an event references a process
// instance the tracker has no live record for.
var ErrUnknownInstance = errors.New("netsec: unknown process instance")

type proc struct {
	inst   Instance
	parent Instance // as recorded at spawn; survives reparenting
	token  Token
	pgid   int
	sid    int
}

// Tracker is the ownership state machine. The host feeds it lifecycle
// events (Spawn/Exit/SetPgid/Setsid) and consults it before forking
// (Admit) and during tenant teardown (Terminate).
//
// Tracker keeps the parent link recorded at spawn time even after the
// real parent exits and the child is reparented by the OS, so the
// reachable descendant set is stable against reparenting.
type Tracker struct {
	mu       sync.Mutex
	procs    map[Instance]*proc
	children map[Instance][]Instance
	lastSeq  map[int]uint64
	frozen   map[Token]bool
	wake     chan struct{} // closed and replaced on every mutation
}

// NewTracker returns an empty tracker.
func NewTracker() *Tracker {
	return &Tracker{
		procs:    make(map[Instance]*proc),
		children: make(map[Instance][]Instance),
		lastSeq:  make(map[int]uint64),
		frozen:   make(map[Token]bool),
		wake:     make(chan struct{}),
	}
}

// Admit reports whether the host may fork a new process for tok.
// It returns false once the token's generation has been frozen.
func (t *Tracker) Admit(tok Token) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return !t.frozen[tok]
}

// Spawn records a new process owned by tok, created by parent (zero
// Instance for a root process launched directly by the host). The
// tracker assigns the PID's next incarnation sequence. Group and
// session default to the new PID, as after a plain fork/exec.
//
// Spawn fails with ErrFrozen if tok's generation is being cleaned up;
// the host must not proceed with the fork in that case.
func (t *Tracker) Spawn(parent Instance, pid int, tok Token) (Instance, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.frozen[tok] {
		return Instance{}, ErrFrozen
	}
	if !parent.IsZero() {
		if _, ok := t.procs[parent]; !ok {
			return Instance{}, fmt.Errorf("%w: parent %v", ErrUnknownInstance, parent)
		}
	}
	t.lastSeq[pid]++
	inst := Instance{PID: pid, Seq: t.lastSeq[pid]}
	if _, exists := t.procs[inst]; exists {
		return Instance{}, fmt.Errorf("netsec: instance %v already live", inst)
	}
	t.procs[inst] = &proc{inst: inst, parent: parent, token: tok, pgid: pid, sid: pid}
	if !parent.IsZero() {
		t.children[parent] = append(t.children[parent], inst)
	}
	t.broadcastLocked()
	return inst, nil
}

// Exit records the termination of inst. A stale exit for an old
// incarnation of a reused PID matches nothing and is ignored.
func (t *Tracker) Exit(inst Instance) {
	t.mu.Lock()
	defer t.mu.Unlock()
	p, ok := t.procs[inst]
	if !ok {
		return
	}
	delete(t.procs, inst)
	if !p.parent.IsZero() {
		sibs := t.children[p.parent]
		for i, c := range sibs {
			if c == inst {
				t.children[p.parent] = append(sibs[:i], sibs[i+1:]...)
				break
			}
		}
	}
	// The node's own recorded children keep their parent link; the
	// ancestry graph is historical, not the live OS tree.
	t.broadcastLocked()
}

// SetPgid records a process-group change. Ownership is unaffected.
func (t *Tracker) SetPgid(inst Instance, pgid int) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	p, ok := t.procs[inst]
	if !ok {
		return fmt.Errorf("%w: %v", ErrUnknownInstance, inst)
	}
	p.pgid = pgid
	return nil
}

// Setsid records the process becoming a session leader. Ownership is
// unaffected.
func (t *Tracker) Setsid(inst Instance) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	p, ok := t.procs[inst]
	if !ok {
		return fmt.Errorf("%w: %v", ErrUnknownInstance, inst)
	}
	p.sid = inst.PID
	p.pgid = inst.PID
	return nil
}

// Freeze blocks all future spawns for tok's generation. It is the
// first step of cleanup and is irreversible.
func (t *Tracker) Freeze(tok Token) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.frozen[tok] = true
	t.broadcastLocked()
}

// Snapshot returns the live reachable descendant set owned by tok:
// every live process whose immutable token matches, found by walking
// the recorded ancestry graph from the generation's roots. Membership
// is independent of process-group and session IDs.
func (t *Tracker) Snapshot(tok Token) []Instance {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.snapshotLocked(tok)
}

func (t *Tracker) snapshotLocked(tok Token) []Instance {
	var out []Instance
	visited := make(map[Instance]bool)
	var walk func(inst Instance)
	walk = func(inst Instance) {
		if visited[inst] {
			return
		}
		visited[inst] = true
		if p, ok := t.procs[inst]; ok && p.token == tok {
			out = append(out, inst)
		}
		for _, c := range t.children[inst] {
			walk(c)
		}
	}
	for inst, p := range t.procs {
		if p.token != tok {
			continue
		}
		// Root of this tenant sub-tree: no recorded parent, or the
		// parent belongs to a different identity.
		parent, ok := t.procs[p.parent]
		if p.parent.IsZero() || !ok || parent.token != tok {
			walk(inst)
		}
	}
	return out
}

// OwnerOf returns the owning token for inst.
func (t *Tracker) OwnerOf(inst Instance) (Token, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	p, ok := t.procs[inst]
	if !ok {
		return Token{}, false
	}
	return p.token, true
}

// Live reports whether inst is still tracked as running.
func (t *Tracker) Live(inst Instance) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	_, ok := t.procs[inst]
	return ok
}

func (t *Tracker) broadcastLocked() {
	close(t.wake)
	t.wake = make(chan struct{})
}

// wakeCh returns a channel that is closed on the next state mutation.
// Callers use it to sleep without holding the lock while waiting for
// exit confirmations.
func (t *Tracker) wakeCh() chan struct{} {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.wake
}
