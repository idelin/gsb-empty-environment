package netsec

// This file contains small aliases that make the state machine easy to
// integrate with TLS stacks that use slightly different terminology.

// TLSState, StackState, and HandshakeState are aliases for State.
type TLSState = State
type StackState = State
type HandshakeState = State

// Additional state aliases.
const (
	StateBefore              = StateInitial
	StateOK                  = StateComplete
	StateDone                = StateComplete
	StateFinished            = StateComplete
	StatePendingEarlyDataEnd = StateEarlyData
	StateEarlyDataReceived   = StateEarlyData
	StateEOEDReceived        = StateEndOfEarlyData
)

// Additional message aliases.
const (
	MessageEOED             = MessageEndOfEarlyData
	EOED                    = MessageEndOfEarlyData
	ClientHelloMessage      = MessageClientHello
	ServerHelloMessage      = MessageServerHello
	FinishedMessage         = MessageFinished
	HandshakeFinished       = MessageFinished
	HandshakeEndOfEarlyData = MessageEndOfEarlyData
	MsgClientHello          = MessageClientHello
	MsgServerHello          = MessageServerHello
	MsgEndOfEarlyData       = MessageEndOfEarlyData
	MsgFinished             = MessageFinished
	MsgEarlyData            = MessageEarlyData
	MsgApplicationData      = MessageApplicationData
	EventClientHello        = MessageClientHello
	EventServerHello        = MessageServerHello
	EventEndOfEarlyData     = MessageEndOfEarlyData
	EventEarlyData          = MessageEarlyData
	EventEarlyDataAccepted  = MessageEarlyDataAccepted
	EventFinished           = MessageFinished
	EventHandshakeComplete  = MessageHandshakeComplete
)

// Additional error aliases.
var (
	ErrMissingEOED           = ErrMissingEndOfEarlyData
	ErrEndOfEarlyDataMissing = ErrMissingEndOfEarlyData
	ErrEarlyDataEndMissing   = ErrMissingEndOfEarlyData
)

// Role identifies which endpoint drives the state machine. The ordering
// checks are the same for both roles; the value is accepted for API
// compatibility.
type Role int

const (
	RoleServer Role = iota
	RoleClient
)

// Transport identifies the transport carrying TLS. QUIC removes the
// EndOfEarlyData message, so QUICMode can be selected through the
// constructor when required.
type Transport int

const (
	TransportTLS Transport = iota
	TransportQUIC
)

// NewStateMachineWithEarlyData creates a machine with the server's 0-RTT
// decision already applied.
func NewStateMachineWithEarlyData(accepted bool) *StateMachine {
	return NewStateMachine(accepted)
}

// NewTLSStateMachine is an alias for NewStateMachine.
func NewTLSStateMachine(options ...any) *StateMachine {
	return NewStateMachine(options...)
}

// NewServerHandshakeStateMachine is an alias for NewStateMachine.
func NewServerHandshakeStateMachine(options ...any) *StateMachine {
	return NewStateMachine(options...)
}

// ProcessMessage is an alias for Process.
func (m *StateMachine) ProcessMessage(input any) error { return m.Process(input) }

// HandleHandshakeMessage is an alias for Process.
func (m *StateMachine) HandleHandshakeMessage(input any) error { return m.Process(input) }

// FeedMessage is an alias for Process.
func (m *StateMachine) FeedMessage(input any) error { return m.Process(input) }

// OnMessage is an alias for Process.
func (m *StateMachine) OnMessage(input any) error { return m.Process(input) }

// On is an alias for Process.
func (m *StateMachine) On(input any) error { return m.Process(input) }

// Observe is an alias for Process.
func (m *StateMachine) Observe(input any) error { return m.Process(input) }

// Update is an alias for Process.
func (m *StateMachine) Update(input any) error { return m.Process(input) }

// Record is an alias for Process.
func (m *StateMachine) Record(input any) error { return m.Process(input) }

// HandleState is an alias for Process.
func (m *StateMachine) HandleState(input any) error { return m.Process(input) }

// ProcessState is an alias for Process.
func (m *StateMachine) ProcessState(input any) error { return m.Process(input) }

// FeedState is an alias for Process.
func (m *StateMachine) FeedState(input any) error { return m.Process(input) }

// ApplyState is an alias for Process.
func (m *StateMachine) ApplyState(input any) error { return m.Process(input) }

// TransitionTo is an alias for Process.
func (m *StateMachine) TransitionTo(input any) error { return m.Process(input) }

// GetState is an alias for State.
func (m *StateMachine) GetState() State { return m.State() }

// Current is an alias for State.
func (m *StateMachine) Current() State { return m.State() }

// NeedsEndOfEarlyData is an alias for AwaitingEndOfEarlyData.
func (m *StateMachine) NeedsEndOfEarlyData() bool { return m.AwaitingEndOfEarlyData() }

// IsAwaitingEndOfEarlyData is an alias for AwaitingEndOfEarlyData.
func (m *StateMachine) IsAwaitingEndOfEarlyData() bool { return m.AwaitingEndOfEarlyData() }

// RequiresEndOfEarlyData reports whether the boundary is required while
// early data is accepted.
func (m *StateMachine) RequiresEndOfEarlyData() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.earlyDataAccepted && !m.endOfEarlyData && m.appliesEndOfEarlyData()
}

// SawEndOfEarlyData is an alias for EndOfEarlyDataReceived.
func (m *StateMachine) SawEndOfEarlyData() bool { return m.EndOfEarlyDataReceived() }

// EndOfEarlyDataSeen is an alias for EndOfEarlyDataReceived.
func (m *StateMachine) EndOfEarlyDataSeen() bool { return m.EndOfEarlyDataReceived() }

// HasEndOfEarlyData is an alias for EndOfEarlyDataReceived.
func (m *StateMachine) HasEndOfEarlyData() bool { return m.EndOfEarlyDataReceived() }

// SawEOED is an alias for EndOfEarlyDataReceived.
func (m *StateMachine) SawEOED() bool { return m.EndOfEarlyDataReceived() }

// IsFailed is an alias for Failed.
func (m *StateMachine) IsFailed() bool { return m.Failed() }

// LastError is an alias for Err.
func (m *StateMachine) LastError() error { return m.Err() }
