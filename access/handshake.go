// Package access implements the wireless controller access-layer handshake
// state machine.
//
// The controller derives a fresh session key for every handshake
// negotiation. Each negotiation is identified by a monotonically increasing
// generation number supplied by the association flow. The state machine in
// this package pins key installation to a (client, generation) pair so that
// retransmitted handshake messages can only trigger a re-sent ACK, never a
// key reinstallation (which would reset the data-plane send counter and
// reopen the channel to replay attacks).
package access

import (
	"errors"
	"fmt"
	"sync"
)

// State is the per-client handshake state.
type State int

const (
	// StateIdle means no handshake has ever completed for the client and
	// no negotiation is in progress.
	StateIdle State = iota
	// StateHandshaking means a new generation is being negotiated and no
	// key has been installed for it yet.
	StateHandshaking
	// StateEstablished means the key for the current generation is
	// installed and the secure channel is live.
	StateEstablished
	// StateInstallFailed means the most recent key installation failed.
	// The previously established channel, if any, remains the active one.
	StateInstallFailed
)

func (s State) String() string {
	switch s {
	case StateIdle:
		return "Idle"
	case StateHandshaking:
		return "Handshaking"
	case StateEstablished:
		return "Established"
	case StateInstallFailed:
		return "InstallFailed"
	default:
		return fmt.Sprintf("State(%d)", int(s))
	}
}

// Kind identifies the type of an incoming handshake event.
type Kind int

const (
	// KindReassociate is a (re)association request. It opens a new
	// handshake generation.
	KindReassociate Kind = iota
	// KindKeyConfirm is the client's key-confirmation message. Accepting
	// it triggers session-key installation for the current generation.
	KindKeyConfirm
)

// Event is a handshake message arriving at the access layer.
type Event struct {
	ClientID   string
	Generation uint64
	Kind       Kind
	// ReplayCounter is the sender's per-generation message counter. A
	// counter already seen for the generation marks a retransmission.
	ReplayCounter uint64
}

// Callbacks connect the state machine to the data plane and the transmitter.
type Callbacks struct {
	// InstallKey installs the session key negotiated in the given
	// generation. The state machine guarantees at most one successful call
	// per (client, generation); retries after an error are allowed.
	InstallKey func(clientID string, generation uint64) error
	// SendAck (re)transmits the handshake acknowledgement for the given
	// generation and replay counter.
	SendAck func(clientID string, generation uint64, replayCounter uint64)
}

// Outcome classifies how an event was handled.
type Outcome int

const (
	// OutcomeDropped means the event was stale, out of order, or otherwise
	// not actionable. Nothing was installed or transmitted.
	OutcomeDropped Outcome = iota
	// OutcomeHandshaking means a new generation was opened.
	OutcomeHandshaking
	// OutcomeInstalled means the key was installed and the ACK sent.
	OutcomeInstalled
	// OutcomeReacked means the event was a retransmission of an already
	// completed handshake: the ACK was re-sent and the key was NOT
	// reinstalled.
	OutcomeReacked
	// OutcomeInstallFailed means key installation failed; no ACK was sent
	// and the old channel, if any, stays active.
	OutcomeInstallFailed
)

// Decision reports how the state machine handled an event.
type Decision struct {
	Outcome Outcome
	Reason  string
}

// Status is a snapshot of a client's handshake state.
type Status struct {
	State State
	// CurrentGen is the generation currently negotiated or established.
	CurrentGen uint64
	// EstablishedGen is the generation backing the live channel. It
	// differs from CurrentGen while a rekey/reassociation is in progress
	// or after a failed installation.
	EstablishedGen uint64
	// HasChannel reports whether an established channel exists at all.
	HasChannel bool
	// LastErr is the most recent key-installation error, if any.
	LastErr error
}

type client struct {
	state          State
	currentGen     uint64
	establishedGen uint64
	hasChannel     bool
	installed      bool // key installed for currentGen
	haveReplay     bool
	lastReplay     uint64
	lastErr        error
}

// Manager owns the per-client handshake state machines.
type Manager struct {
	mu      sync.Mutex
	cb      Callbacks
	clients map[string]*client
}

// NewManager creates a Manager. Both callbacks are required.
func NewManager(cb Callbacks) (*Manager, error) {
	if cb.InstallKey == nil || cb.SendAck == nil {
		return nil, errors.New("access: InstallKey and SendAck callbacks are required")
	}
	return &Manager{cb: cb, clients: make(map[string]*client)}, nil
}

// Handle feeds one handshake event into the client's state machine.
func (m *Manager) Handle(ev Event) Decision {
	m.mu.Lock()
	defer m.mu.Unlock()
	c := m.clients[ev.ClientID]
	if c == nil {
		c = &client{state: StateIdle}
		m.clients[ev.ClientID] = c
	}
	switch ev.Kind {
	case KindReassociate:
		return c.reassociate(ev)
	case KindKeyConfirm:
		return c.confirm(ev, m.cb)
	default:
		return Decision{OutcomeDropped, "unknown event kind"}
	}
}

// Status returns a snapshot of the client's state machine.
func (m *Manager) Status(clientID string) Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	c := m.clients[clientID]
	if c == nil {
		return Status{State: StateIdle}
	}
	return Status{
		State:          c.state,
		CurrentGen:     c.currentGen,
		EstablishedGen: c.establishedGen,
		HasChannel:     c.hasChannel,
		LastErr:        c.lastErr,
	}
}

// Forget drops all state for a client (e.g. after deauthentication).
func (m *Manager) Forget(clientID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.clients, clientID)
}

func (c *client) reassociate(ev Event) Decision {
	if c.state != StateIdle && ev.Generation <= c.currentGen {
		return Decision{OutcomeDropped,
			fmt.Sprintf("stale (re)association for generation %d, current is %d", ev.Generation, c.currentGen)}
	}
	c.currentGen = ev.Generation
	c.state = StateHandshaking
	c.installed = false
	c.haveReplay = false
	c.lastErr = nil
	return Decision{OutcomeHandshaking,
		fmt.Sprintf("generation %d negotiation opened", ev.Generation)}
}

func (c *client) confirm(ev Event, cb Callbacks) Decision {
	switch {
	case c.state == StateIdle:
		return Decision{OutcomeDropped, "key confirm with no handshake in progress"}
	case ev.Generation < c.currentGen:
		return Decision{OutcomeDropped,
			fmt.Sprintf("late key confirm for old generation %d, current is %d", ev.Generation, c.currentGen)}
	case ev.Generation > c.currentGen:
		return Decision{OutcomeDropped,
			fmt.Sprintf("key confirm for unknown generation %d, current is %d", ev.Generation, c.currentGen)}
	}

	dup := c.haveReplay && ev.ReplayCounter <= c.lastReplay
	if !dup {
		c.haveReplay = true
		c.lastReplay = ev.ReplayCounter
	}

	if c.installed {
		// The key for this generation is already installed. A repeated
		// confirm may only re-trigger the ACK; reinstalling would reset
		// the data-plane send counter.
		cb.SendAck(ev.ClientID, ev.Generation, ev.ReplayCounter)
		return Decision{OutcomeReacked,
			fmt.Sprintf("duplicate confirm for generation %d; ACK re-sent, key not reinstalled", ev.Generation)}
	}
	if dup && c.state != StateInstallFailed {
		return Decision{OutcomeDropped, "duplicate confirm before installation completed"}
	}

	// First confirm for this generation, or a retry after a failed
	// installation. The callback is bound to the negotiated generation.
	if err := cb.InstallKey(ev.ClientID, ev.Generation); err != nil {
		c.state = StateInstallFailed
		c.lastErr = err
		return Decision{OutcomeInstallFailed,
			fmt.Sprintf("key install for generation %d failed: %v", ev.Generation, err)}
	}
	c.installed = true
	c.state = StateEstablished
	c.establishedGen = ev.Generation
	c.hasChannel = true
	c.lastErr = nil
	cb.SendAck(ev.ClientID, ev.Generation, ev.ReplayCounter)
	return Decision{OutcomeInstalled,
		fmt.Sprintf("key installed for generation %d", ev.Generation)}
}
