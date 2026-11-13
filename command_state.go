package netsec

import (
	"sync"
	"time"
)

const (
	// TypeIDSingleCommand is C_SC_NA_1, the IEC 60870-5-104 single command.
	TypeIDSingleCommand uint8 = 45

	// CauseCancel is the cause of transmission used to cancel a selected command.
	CauseCancel uint8 = 8

	// SelectExecuteBit is bit 7 of a single-command SCO. It is set for SELECT
	// and clear for EXECUTE.
	SelectExecuteBit uint8 = 0x80

	// SingleCommandValueBit is bit 0 of a single-command SCO.
	SingleCommandValueBit uint8 = 0x01

	// DefaultSelectTimeout is how long a SELECT remains valid.
	DefaultSelectTimeout = 30 * time.Second
)

// IOA is a 24-bit IEC 60870-5-104 information object address.
type IOA = uint32

// ConnectionID identifies one accepted transport connection. A selection is
// bound to the connection on which it was made and is never shared with another
// connection.
type ConnectionID = string

// PointMode controls whether a point may be operated without a preceding
// SELECT.
type PointMode uint8

const (
	// PointDirectExecute permits EXECUTE without SELECT.
	PointDirectExecute PointMode = iota
	// PointSelectBeforeOperate requires a matching SELECT before EXECUTE.
	PointSelectBeforeOperate
)

// Reason describes the result of processing a control command.
type Reason string

const (
	ReasonSelectAccepted        Reason = "select-accepted"
	ReasonExecuteAccepted       Reason = "execute-accepted"
	ReasonDirectExecuteAccepted Reason = "direct-execute-accepted"
	ReasonCancelAccepted        Reason = "cancel-accepted"
	ReasonUnsupportedType       Reason = "unsupported-type"
	ReasonSelectRequired        Reason = "select-required"
	ReasonSelectionExpired      Reason = "selection-expired"
	ReasonCommandValueMismatch  Reason = "command-value-mismatch"
)

// Result reports whether a command may be applied to the controlled process.
// Allowed is false for every rejected command.
type Result struct {
	Allowed bool
	Reason  Reason
}

// Accepted reports whether the command was allowed.
func (r Result) Accepted() bool { return r.Allowed }

// OK reports whether the command was allowed.
func (r Result) OK() bool { return r.Allowed }

// Rejected reports whether the command was denied.
func (r Result) Rejected() bool { return !r.Allowed }

// ControlCommand is a decoded C_SC_NA_1 command. Value and Select are decoded
// from the SCO byte; use DecodeSCO or EncodeSCO when working with wire bytes.
type ControlCommand struct {
	TypeID uint8
	COT    uint8
	IOA    IOA
	Value  bool
	Select bool
}

// SCO returns the single-command SCO byte represented by the command.
func (c ControlCommand) SCO() uint8 {
	return EncodeSCO(c.Value, c.Select)
}

// NewSingleCommand decodes a raw SCO byte into a C_SC_NA_1 command.
func NewSingleCommand(cot uint8, ioa IOA, sco uint8) ControlCommand {
	value, selectBit := DecodeSCO(sco)
	return ControlCommand{
		TypeID: TypeIDSingleCommand,
		COT:    cot,
		IOA:    ioa,
		Value:  value,
		Select: selectBit,
	}
}

// DecodeSCO splits a single-command SCO byte into its command value (bit 0)
// and S/E bit (bit 7).
func DecodeSCO(sco uint8) (value bool, selectBit bool) {
	return sco&SingleCommandValueBit != 0, sco&SelectExecuteBit != 0
}

// EncodeSCO builds a single-command SCO byte from a command value and the S/E
// bit. Reserved bits 1 through 6 are left clear.
func EncodeSCO(value bool, selectBit bool) uint8 {
	var sco uint8
	if value {
		sco |= SingleCommandValueBit
	}
	if selectBit {
		sco |= SelectExecuteBit
	}
	return sco
}

// Option configures a CommandState at construction time.
type Option func(*CommandState)

// WithClock replaces the clock used by command handling. It is primarily
// useful for deterministic timeout tests.
func WithClock(now func() time.Time) Option {
	return func(s *CommandState) {
		if now != nil {
			s.now = now
		}
	}
}

// WithSelectTimeout changes how long a SELECT remains valid.
func WithSelectTimeout(timeout time.Duration) Option {
	return func(s *CommandState) {
		s.selectTimeout = timeout
	}
}

// WithSelectBeforeOperate marks the supplied IOAs as requiring SELECT before
// EXECUTE.
func WithSelectBeforeOperate(ioas ...IOA) Option {
	return func(s *CommandState) {
		for _, ioa := range ioas {
			s.modes[ioa] = PointSelectBeforeOperate
		}
	}
}

// CommandState is a concurrency-safe select-before-operate state machine for
// C_SC_NA_1 commands received by an IEC 60870-5-104 slave.
type CommandState struct {
	mu            sync.Mutex
	now           func() time.Time
	selectTimeout time.Duration
	modes         map[IOA]PointMode
	pending       map[selectionKey]selection
}

type selectionKey struct {
	conn ConnectionID
	ioa  IOA
}

type selection struct {
	value   bool
	expires time.Time
}

// New creates a CommandState. The default SELECT timeout is 30 seconds.
func New(options ...Option) *CommandState {
	s := &CommandState{
		now:           time.Now,
		selectTimeout: DefaultSelectTimeout,
		modes:         make(map[IOA]PointMode),
		pending:       make(map[selectionKey]selection),
	}
	for _, option := range options {
		if option != nil {
			option(s)
		}
	}
	return s
}

// NewCommandState is an explicit constructor alias for New.
func NewCommandState(options ...Option) *CommandState {
	return New(options...)
}

// SetPointMode changes the operation policy for one IOA.
func (s *CommandState) SetPointMode(ioa IOA, mode PointMode) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.modes[ioa] = mode
}

// SetSelectBeforeOperate enables or disables select-before-operate for one
// IOA.
func (s *CommandState) SetSelectBeforeOperate(ioa IOA, required bool) {
	mode := PointDirectExecute
	if required {
		mode = PointSelectBeforeOperate
	}
	s.SetPointMode(ioa, mode)
}

// PointMode returns the configured operation policy for an IOA.
func (s *CommandState) PointMode(ioa IOA) PointMode {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.modes[ioa]
}

// HandleCommand processes one command using the configured clock.
func (s *CommandState) HandleCommand(conn ConnectionID, cmd ControlCommand) Result {
	return s.HandleCommandAt(conn, cmd, s.now())
}

// HandleCommandAt processes one command at an explicit time. It permits
// deterministic timeout handling without replacing the state clock.
func (s *CommandState) HandleCommandAt(conn ConnectionID, cmd ControlCommand, now time.Time) Result {
	if cmd.TypeID != TypeIDSingleCommand {
		return Result{Allowed: false, Reason: ReasonUnsupportedType}
	}

	key := selectionKey{conn: conn, ioa: cmd.IOA}

	s.mu.Lock()
	defer s.mu.Unlock()

	if cmd.COT == CauseCancel {
		delete(s.pending, key)
		return Result{Allowed: true, Reason: ReasonCancelAccepted}
	}

	if cmd.Select {
		s.pending[key] = selection{
			value:   cmd.Value,
			expires: now.Add(s.selectTimeout),
		}
		return Result{Allowed: true, Reason: ReasonSelectAccepted}
	}

	mode := s.modes[cmd.IOA]
	selected, found := s.pending[key]
	if found && !now.Before(selected.expires) {
		delete(s.pending, key)
		if mode == PointSelectBeforeOperate {
			return Result{Allowed: false, Reason: ReasonSelectionExpired}
		}
		return Result{Allowed: true, Reason: ReasonDirectExecuteAccepted}
	}

	if found {
		if selected.value == cmd.Value {
			delete(s.pending, key)
			return Result{Allowed: true, Reason: ReasonExecuteAccepted}
		}
		if mode == PointSelectBeforeOperate {
			return Result{Allowed: false, Reason: ReasonCommandValueMismatch}
		}
		return Result{Allowed: true, Reason: ReasonDirectExecuteAccepted}
	}

	if mode == PointSelectBeforeOperate {
		return Result{Allowed: false, Reason: ReasonSelectRequired}
	}

	return Result{Allowed: true, Reason: ReasonDirectExecuteAccepted}
}

// HandleSCO processes a raw single-command SCO byte.
func (s *CommandState) HandleSCO(conn ConnectionID, cot uint8, ioa IOA, sco uint8) Result {
	return s.HandleCommand(conn, NewSingleCommand(cot, ioa, sco))
}

// Select records a SELECT for the connection and IOA.
func (s *CommandState) Select(conn ConnectionID, ioa IOA, value bool) Result {
	return s.HandleCommand(conn, ControlCommand{
		TypeID: TypeIDSingleCommand,
		COT:    6,
		IOA:    ioa,
		Value:  value,
		Select: true,
	})
}

// SelectAt records a SELECT at an explicit time.
func (s *CommandState) SelectAt(conn ConnectionID, ioa IOA, value bool, now time.Time) Result {
	return s.HandleCommandAt(conn, ControlCommand{
		TypeID: TypeIDSingleCommand,
		COT:    6,
		IOA:    ioa,
		Value:  value,
		Select: true,
	}, now)
}

// Execute applies or rejects an EXECUTE for the connection and IOA.
func (s *CommandState) Execute(conn ConnectionID, ioa IOA, value bool) Result {
	return s.HandleCommand(conn, ControlCommand{
		TypeID: TypeIDSingleCommand,
		COT:    6,
		IOA:    ioa,
		Value:  value,
		Select: false,
	})
}

// ExecuteAt applies or rejects an EXECUTE at an explicit time.
func (s *CommandState) ExecuteAt(conn ConnectionID, ioa IOA, value bool, now time.Time) Result {
	return s.HandleCommandAt(conn, ControlCommand{
		TypeID: TypeIDSingleCommand,
		COT:    6,
		IOA:    ioa,
		Value:  value,
		Select: false,
	}, now)
}

// Cancel clears the SELECT for the connection and IOA.
func (s *CommandState) Cancel(conn ConnectionID, ioa IOA) Result {
	return s.HandleCommand(conn, ControlCommand{
		TypeID: TypeIDSingleCommand,
		COT:    CauseCancel,
		IOA:    ioa,
	})
}

// CancelAt clears the SELECT for the connection and IOA at an explicit time.
func (s *CommandState) CancelAt(conn ConnectionID, ioa IOA, now time.Time) Result {
	return s.HandleCommandAt(conn, ControlCommand{
		TypeID: TypeIDSingleCommand,
		COT:    CauseCancel,
		IOA:    ioa,
	}, now)
}

// CloseConnection discards every pending selection owned by the connection.
// A slave should call this when its transport connection closes.
func (s *CommandState) CloseConnection(conn ConnectionID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key := range s.pending {
		if key.conn == conn {
			delete(s.pending, key)
		}
	}
}
