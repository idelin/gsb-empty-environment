// Package netsec implements a server-side state machine that decides whether
// TLS 1.3 early data (0-RTT) may be accepted for a handshake.
//
// The machine is driven by events delivered from the TLS stack:
//
//	OnClientHello(ticketAgeSkew)  -- a ClientHello arrived, carrying the
//	                                 observed ticket age skew.
//	OnHelloRetryRequest()         -- the stack sent a HelloRetryRequest.
//	OnEarlyData()                 -- the client offered early data.
//
// Decision rules:
//   - Once a HelloRetryRequest has been sent, early data MUST be rejected,
//     even if the client still sends it bound to the new handshake.
//   - A ticket age skew outside the configured acceptance window is rejected.
//   - With no retry and a ticket age skew inside the window, early data is
//     accepted.
package netsec

import (
	"errors"
	"fmt"
	"time"
)

// State identifies the current handshake phase tracked by the machine.
type State int

const (
	// StateInit is the state before any handshake message is processed.
	StateInit State = iota
	// StateClientHello means a ClientHello has been received.
	StateClientHello
	// StateRetrySent means a HelloRetryRequest has been sent for this
	// connection; early data can never be accepted from here on.
	StateRetrySent
	// StateEarlyDataAccepted means offered early data was accepted.
	StateEarlyDataAccepted
	// StateEarlyDataRejected means offered early data was rejected.
	StateEarlyDataRejected
)

func (s State) String() string {
	switch s {
	case StateInit:
		return "Init"
	case StateClientHello:
		return "ClientHello"
	case StateRetrySent:
		return "RetrySent"
	case StateEarlyDataAccepted:
		return "EarlyDataAccepted"
	case StateEarlyDataRejected:
		return "EarlyDataRejected"
	default:
		return fmt.Sprintf("State(%d)", int(s))
	}
}

// Decision is the verdict returned for an early data offer.
type Decision int

const (
	// DecisionRejectEarlyData drops the offered early data; the client must
	// retransmit it after the handshake completes.
	DecisionRejectEarlyData Decision = iota
	// DecisionAcceptEarlyData binds the offered early data to this handshake.
	DecisionAcceptEarlyData
)

func (d Decision) String() string {
	switch d {
	case DecisionRejectEarlyData:
		return "RejectEarlyData"
	case DecisionAcceptEarlyData:
		return "AcceptEarlyData"
	default:
		return fmt.Sprintf("Decision(%d)", int(d))
	}
}

// DefaultMaxTicketAgeSkew is the default acceptance window for the observed
// ticket age skew.
const DefaultMaxTicketAgeSkew = 10 * time.Second

// ErrUnexpectedEvent is returned when an event arrives in a state where it
// is not valid.
var ErrUnexpectedEvent = errors.New("netsec: unexpected event for current state")

// Machine tracks one server-side connection and decides the fate of early
// data. It is not safe for concurrent use; the TLS stack must serialize
// events per connection.
type Machine struct {
	state           State
	maxTicketSkew   time.Duration
	ticketAgeSkew   time.Duration
	retrySent       bool
	clientHelloSeen bool
}

// New returns a Machine using the given ticket age skew acceptance window.
// A non-positive window selects DefaultMaxTicketAgeSkew.
func New(maxTicketAgeSkew time.Duration) *Machine {
	if maxTicketAgeSkew <= 0 {
		maxTicketAgeSkew = DefaultMaxTicketAgeSkew
	}
	return &Machine{state: StateInit, maxTicketSkew: maxTicketAgeSkew}
}

// State reports the machine's current state.
func (m *Machine) State() State { return m.state }

// OnClientHello records a ClientHello from the TLS stack together with the
// observed ticket age skew (client-reported ticket age minus server-side
// ticket age). The skew is captured now so the early data decision can be
// made when the offer arrives.
func (m *Machine) OnClientHello(ticketAgeSkew time.Duration) error {
	if m.clientHelloSeen {
		return fmt.Errorf("%w: duplicate ClientHello in state %s", ErrUnexpectedEvent, m.state)
	}
	m.clientHelloSeen = true
	m.ticketAgeSkew = ticketAgeSkew
	m.state = StateClientHello
	return nil
}

// OnHelloRetryRequest records that the TLS stack sent a HelloRetryRequest.
// From this point on early data is always rejected for this connection.
func (m *Machine) OnHelloRetryRequest() error {
	if !m.clientHelloSeen {
		return fmt.Errorf("%w: HelloRetryRequest before ClientHello in state %s", ErrUnexpectedEvent, m.state)
	}
	if m.retrySent {
		return fmt.Errorf("%w: duplicate HelloRetryRequest in state %s", ErrUnexpectedEvent, m.state)
	}
	m.retrySent = true
	m.state = StateRetrySent
	return nil
}

// OnEarlyData decides whether an early data offer is accepted. The decision
// is latched: once made, the machine stays in the resulting state.
func (m *Machine) OnEarlyData() (Decision, error) {
	if !m.clientHelloSeen {
		return DecisionRejectEarlyData, fmt.Errorf("%w: early data before ClientHello in state %s", ErrUnexpectedEvent, m.state)
	}
	switch m.state {
	case StateEarlyDataAccepted, StateEarlyDataRejected:
		return DecisionRejectEarlyData, fmt.Errorf("%w: duplicate early data offer in state %s", ErrUnexpectedEvent, m.state)
	}
	if m.retrySent {
		m.state = StateEarlyDataRejected
		return DecisionRejectEarlyData, nil
	}
	if !m.ticketWithinWindow() {
		m.state = StateEarlyDataRejected
		return DecisionRejectEarlyData, nil
	}
	m.state = StateEarlyDataAccepted
	return DecisionAcceptEarlyData, nil
}

func (m *Machine) ticketWithinWindow() bool {
	skew := m.ticketAgeSkew
	if skew < 0 {
		skew = -skew
	}
	return skew <= m.maxTicketSkew
}
