package netsec

import (
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"sync"
)

var (
	// ErrMissingEndOfEarlyData is returned when early data was accepted and a
	// subsequent handshake message arrives before EndOfEarlyData.
	ErrMissingEndOfEarlyData = errors.New("netsec: early data accepted but EndOfEarlyData was not received before the next handshake message")
	// ErrEndOfEarlyDataRequired is an alias for ErrMissingEndOfEarlyData.
	ErrEndOfEarlyDataRequired = ErrMissingEndOfEarlyData
	// ErrEarlyDataBoundaryViolation is an alias for ErrMissingEndOfEarlyData.
	ErrEarlyDataBoundaryViolation = ErrMissingEndOfEarlyData

	// ErrUnexpectedEndOfEarlyData is returned when EndOfEarlyData is received
	// without the server having accepted early data.
	ErrUnexpectedEndOfEarlyData = errors.New("netsec: unexpected EndOfEarlyData without accepted early data")
	// ErrDuplicateEndOfEarlyData is returned for a second EndOfEarlyData.
	ErrDuplicateEndOfEarlyData = errors.New("netsec: duplicate EndOfEarlyData")
	// ErrEndOfEarlyDataAfterFinished is returned when EndOfEarlyData is
	// reported after the client Finished message.
	ErrEndOfEarlyDataAfterFinished = errors.New("netsec: EndOfEarlyData received after Finished")
	// ErrUnexpectedEarlyData is returned when early data is received but was
	// not accepted by the server.
	ErrUnexpectedEarlyData = errors.New("netsec: unexpected early data")
	// ErrLateEarlyData is returned when early data is received after the
	// EndOfEarlyData boundary.
	ErrLateEarlyData = errors.New("netsec: early data received after EndOfEarlyData")
	// ErrUnknownEvent is returned when a stack event cannot be interpreted.
	ErrUnknownEvent = errors.New("netsec: unknown TLS stack event")
	// ErrHandshakeIncomplete is returned by Validate when Finished has not
	// been observed.
	ErrHandshakeIncomplete = errors.New("netsec: handshake is incomplete")
	// ErrStateMachineFailed is returned after a state-machine violation.
	ErrStateMachineFailed = errors.New("netsec: state machine has failed")
)

// Option configures a StateMachine. Options are accepted by the variadic
// constructors and can also be supplied as plain values to those
// constructors.
type Option func(*StateMachine)

// WithEarlyDataAccepted starts the machine with 0-RTT already accepted.
func WithEarlyDataAccepted() Option {
	return func(m *StateMachine) {
		m.earlyDataAccepted = true
		m.state = StateEarlyData
	}
}

// WithEarlyDataRejected starts the machine with 0-RTT explicitly rejected.
func WithEarlyDataRejected() Option {
	return func(m *StateMachine) {
		m.earlyDataRejected = true
		m.state = StateHandshake
	}
}

// WithoutEndOfEarlyData allows an early-data session to continue without
// EndOfEarlyData. This is intended for transports such as QUIC where the
// message is removed from the handshake. TLS-over-TCP must leave the default
// enforcement enabled.
func WithoutEndOfEarlyData() Option {
	return func(m *StateMachine) {
		m.requireEndOfEarlyData = false
		m.requiredSet = true
	}
}

// QUICMode is an alias for WithoutEndOfEarlyData.
func QUICMode() Option {
	return WithoutEndOfEarlyData()
}

// StateMachine enforces the handshake ordering rules around 0-RTT data.
// Its zero value is ready for use and enforces EndOfEarlyData.
type StateMachine struct {
	mu sync.Mutex

	state             State
	failure           error
	clientHello       bool
	serverHello       bool
	earlyDataOffered  bool
	earlyDataAccepted bool
	earlyDataRejected bool
	endOfEarlyData    bool
	clientFinished    bool
	serverFinished    bool
	complete          bool

	requireEndOfEarlyData bool
	requiredSet           bool
}

// NewStateMachine creates a state machine. Optional arguments may be booleans
// (true means early data was accepted), Options, MessageTypes, States, or
// configuration structs. With no arguments it starts at StateInitial.
func NewStateMachine(options ...any) *StateMachine {
	m := &StateMachine{}
	m.applyOptions(options...)
	return m
}

// New is an alias for NewStateMachine.
func New(options ...any) *StateMachine { return NewStateMachine(options...) }

// NewHandshakeStateMachine is an alias for NewStateMachine.
func NewHandshakeStateMachine(options ...any) *StateMachine {
	return NewStateMachine(options...)
}

// NewServerStateMachine is an alias for NewStateMachine.
func NewServerStateMachine(options ...any) *StateMachine {
	return NewStateMachine(options...)
}

// NewMachine is an alias for NewStateMachine.
func NewMachine(options ...any) *StateMachine { return NewStateMachine(options...) }

// Config is an optional constructor configuration.
type Config struct {
	EarlyDataAccepted      bool
	EarlyDataRejected      bool
	RequireEndOfEarlyData  bool
	EndOfEarlyDataRequired bool
}

func (m *StateMachine) appliesEndOfEarlyData() bool {
	if !m.requiredSet {
		return true
	}
	return m.requireEndOfEarlyData
}

func (m *StateMachine) applyOptions(options ...any) {
	for _, opt := range options {
		switch v := opt.(type) {
		case nil:
			continue
		case Option:
			v(m)
		case Config:
			m.applyConfig(v)
		case *Config:
			if v != nil {
				m.applyConfig(*v)
			}
		case Role:
		case Transport:
			if v == TransportQUIC {
				m.requireEndOfEarlyData = false
				m.requiredSet = true
			}
		case bool:
			if v {
				m.earlyDataAccepted = true
				m.state = StateEarlyData
			}
		case MessageType:
			m.applyAcceptedState(v)
		case State:
			ev, err := eventFromState(v)
			if err == nil {
				_ = m.applyEvent(ev)
			}
		case Message:
			_ = m.applyEvent(eventFromMessage(v))
		case *Message:
			if v != nil {
				_ = m.applyEvent(eventFromMessage(*v))
			}
		case string:
			m.applyAcceptedStateName(v)
		default:
			if t, ok := messageTypeFromName(fmt.Sprint(opt)); ok {
				m.applyAcceptedState(t)
			}
		}
	}
}

func (m *StateMachine) applyConfig(c Config) {
	if c.EarlyDataAccepted {
		m.earlyDataAccepted = true
		m.state = StateEarlyData
	}
	if c.EarlyDataRejected {
		m.earlyDataRejected = true
		m.state = StateHandshake
	}
	if c.RequireEndOfEarlyData || c.EndOfEarlyDataRequired {
		m.requireEndOfEarlyData = true
		m.requiredSet = true
	}
}

func (m *StateMachine) applyAcceptedState(t MessageType) {
	switch t {
	case MessageEarlyDataAccepted:
		m.earlyDataAccepted = true
		m.state = StateEarlyData
	case MessageEarlyDataRejected:
		m.earlyDataRejected = true
		m.state = StateHandshake
	case MessageEndOfEarlyData:
		if m.earlyDataAccepted {
			m.endOfEarlyData = true
			m.state = StateEndOfEarlyData
		}
	}
}

func (m *StateMachine) applyAcceptedStateName(s string) {
	if t, ok := messageTypeFromName(s); ok {
		m.applyAcceptedState(t)
	}
}

// Feed consumes one stack event. It accepts Message/HandshakeMessage/Event
// values, MessageType constants, TLS numeric handshake types, State values,
// strings, maps, and structs containing those values. A slice is consumed
// element by element.
func (m *StateMachine) Feed(input any) error {
	return m.Process(input)
}

// Process is the canonical event entry point.
func (m *StateMachine) Process(input any) error {
	if input == nil {
		return ErrUnknownEvent
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.processLocked(input, 0)
}

// Handle is an alias for Process.
func (m *StateMachine) Handle(input any) error { return m.Process(input) }

// HandleMessage is an alias for Process.
func (m *StateMachine) HandleMessage(input any) error { return m.Process(input) }

// Advance is an alias for Process.
func (m *StateMachine) Advance(input any) error { return m.Process(input) }

// Step is an alias for Process.
func (m *StateMachine) Step(input any) error { return m.Process(input) }

// Next is an alias for Process.
func (m *StateMachine) Next(input any) error { return m.Process(input) }

// Receive is an alias for Process.
func (m *StateMachine) Receive(input any) error { return m.Process(input) }

// Consume is an alias for Process.
func (m *StateMachine) Consume(input any) error { return m.Process(input) }

// Apply is an alias for Process.
func (m *StateMachine) Apply(input any) error { return m.Process(input) }

// Transition is an alias for Process.
func (m *StateMachine) Transition(input any) error { return m.Process(input) }

// Push is an alias for Process.
func (m *StateMachine) Push(input any) error { return m.Process(input) }

// FeedAll consumes events in order and stops at the first error.
func (m *StateMachine) FeedAll(inputs ...any) error {
	for _, input := range inputs {
		if err := m.Process(input); err != nil {
			return err
		}
	}
	return nil
}

// AcceptEarlyData marks early data as accepted by the server.
func (m *StateMachine) AcceptEarlyData() error {
	return m.Process(MessageEarlyDataAccepted)
}

// RejectEarlyData marks early data as rejected by the server.
func (m *StateMachine) RejectEarlyData() error {
	return m.Process(MessageEarlyDataRejected)
}

// SetEarlyDataAccepted sets the server's early-data decision.
func (m *StateMachine) SetEarlyDataAccepted(accepted bool) error {
	if accepted {
		return m.AcceptEarlyData()
	}
	return m.RejectEarlyData()
}

func (m *StateMachine) processLocked(input any, depth int) error {
	if depth > 32 {
		return ErrUnknownEvent
	}
	if m.failure != nil {
		return m.failure
	}

	if seq, ok := eventSlice(input); ok {
		for _, item := range seq {
			if err := m.processLocked(item, depth+1); err != nil {
				return err
			}
		}
		return nil
	}

	ev, err := decodeEvent(input, depth+1)
	if err != nil {
		return err
	}
	return m.applyEvent(ev)
}

func (m *StateMachine) fail(err error) error {
	m.failure = err
	m.state = StateFailed
	return err
}

func (m *StateMachine) applyEvent(ev internalEvent) error {
	if ev.hasType && ev.typ == MessageUnknown {
		return ErrUnknownEvent
	}

	if ev.accepted {
		if err := m.acceptEarlyData(); err != nil {
			return err
		}
		if !ev.hasType || ev.typ == MessageEarlyDataAccepted {
			return nil
		}
	}
	if ev.rejected {
		m.earlyDataRejected = true
		m.earlyDataAccepted = false
		if m.state == StateEarlyDataPending || m.state == StateInitial || m.state == StateEarlyData {
			m.state = StateHandshake
		}
		if !ev.hasType || ev.typ == MessageEarlyDataRejected {
			return nil
		}
	}
	if ev.complete && !ev.hasType {
		m.complete = true
		m.state = StateComplete
		return nil
	}
	if ev.noop {
		return nil
	}
	if !ev.hasType {
		return ErrUnknownEvent
	}

	switch ev.typ {
	case MessageEarlyDataAccepted:
		return m.acceptEarlyData()
	case MessageEarlyDataRejected:
		m.earlyDataRejected = true
		m.earlyDataAccepted = false
		if m.state == StateEarlyDataPending || m.state == StateInitial {
			m.state = StateHandshake
		}
		return nil
	case MessageHandshakeComplete:
		if m.earlyDataAccepted && !m.endOfEarlyData && m.appliesEndOfEarlyData() {
			return m.fail(ErrMissingEndOfEarlyData)
		}
		m.complete = true
		m.state = StateComplete
		return nil
	case MessageClientHello:
		m.clientHello = true
		if ev.earlyData || ev.message.EarlyData {
			m.earlyDataOffered = true
			if !m.earlyDataAccepted && !m.earlyDataRejected {
				m.state = StateEarlyDataPending
			}
		} else if m.state == StateInitial {
			m.state = StateClientHello
		}
		return nil
	case MessageServerHello:
		m.serverHello = true
		if m.state == StateInitial || m.state == StateClientHello {
			m.state = StateHandshake
		}
		return nil
	case MessageEncryptedExtensions:
		if ev.accepted || ev.message.Accepted || ev.message.EarlyDataAccepted {
			return m.acceptEarlyData()
		}
		if m.state == StateEarlyDataPending && ev.rejected {
			m.earlyDataRejected = true
			m.state = StateHandshake
		}
		if m.state == StateEarlyDataPending {
			m.state = StateHandshake
		}
		return nil
	case MessageEarlyData:
		if !m.earlyDataAccepted {
			if m.earlyDataRejected || !m.earlyDataOffered {
				return ErrUnexpectedEarlyData
			}
			if err := m.acceptEarlyData(); err != nil {
				return err
			}
		}
		if m.endOfEarlyData {
			return ErrLateEarlyData
		}
		m.state = StateEarlyData
		return nil
	case MessageEndOfEarlyData:
		if !m.earlyDataAccepted {
			return ErrUnexpectedEndOfEarlyData
		}
		if m.endOfEarlyData {
			return ErrDuplicateEndOfEarlyData
		}
		if m.clientFinished {
			return ErrEndOfEarlyDataAfterFinished
		}
		m.endOfEarlyData = true
		m.earlyDataOffered = false
		m.state = StateEndOfEarlyData
		return nil
	case MessageFinished:
		if m.earlyDataAccepted && !m.endOfEarlyData && m.appliesEndOfEarlyData() {
			return m.fail(ErrMissingEndOfEarlyData)
		}
		m.clientFinished = true
		m.complete = true
		m.state = StateComplete
		return nil
	case MessageServerFinished:
		m.serverFinished = true
		return nil
	case MessageApplicationData:
		if (m.earlyDataAccepted || (m.earlyDataOffered && !m.earlyDataRejected)) &&
			!m.endOfEarlyData && m.appliesEndOfEarlyData() {
			return m.fail(ErrMissingEndOfEarlyData)
		}
		if m.complete {
			m.state = StateComplete
		}
		return nil
	case MessageNewSessionTicket, MessageCertificate, MessageCertificateRequest, MessageCertificateVerify, MessageKeyUpdate:
		if m.earlyDataAccepted && !m.endOfEarlyData && m.appliesEndOfEarlyData() {
			return m.fail(ErrMissingEndOfEarlyData)
		}
		if m.state == StateInitial || m.state == StateClientHello {
			m.state = StateHandshake
		}
		return nil
	default:
		if m.earlyDataAccepted && !m.endOfEarlyData && m.appliesEndOfEarlyData() {
			return m.fail(ErrMissingEndOfEarlyData)
		}
		return ErrUnknownEvent
	}
}

func (m *StateMachine) acceptEarlyData() error {
	if m.complete || m.clientFinished {
		return m.fail(errors.New("netsec: early data accepted after handshake completion"))
	}
	if m.endOfEarlyData {
		return m.fail(errors.New("netsec: early data accepted after EndOfEarlyData"))
	}
	m.earlyDataOffered = true
	m.earlyDataRejected = false
	m.earlyDataAccepted = true
	m.state = StateEarlyData
	return nil
}

// State returns the current state.
func (m *StateMachine) State() State {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state == StateInitial && m.failure != nil {
		return StateFailed
	}
	return m.state
}

// CurrentState is an alias for State.
func (m *StateMachine) CurrentState() State { return m.State() }

// Phase is an alias for State.
func (m *StateMachine) Phase() State { return m.State() }

// Status is an alias for State.
func (m *StateMachine) Status() State { return m.State() }

// Err returns the first violation, if any.
func (m *StateMachine) Err() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.failure
}

// Failed reports whether the machine rejected the sequence.
func (m *StateMachine) Failed() bool { return m.Err() != nil }

// EarlyDataAccepted reports whether the server accepted 0-RTT data.
func (m *StateMachine) EarlyDataAccepted() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.earlyDataAccepted
}

// EarlyDataRejected reports whether the server rejected 0-RTT data.
func (m *StateMachine) EarlyDataRejected() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.earlyDataRejected
}

// EndOfEarlyDataReceived reports whether the boundary message was consumed.
func (m *StateMachine) EndOfEarlyDataReceived() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.endOfEarlyData
}

// AwaitingEndOfEarlyData reports whether early data was accepted and the
// EndOfEarlyData boundary has not yet been received.
func (m *StateMachine) AwaitingEndOfEarlyData() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.earlyDataAccepted && !m.endOfEarlyData && m.appliesEndOfEarlyData()
}

// Complete reports whether Finished has been observed.
func (m *StateMachine) Complete() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.complete
}

// Done is an alias for Complete.
func (m *StateMachine) Done() bool { return m.Complete() }

// IsComplete is an alias for Complete.
func (m *StateMachine) IsComplete() bool { return m.Complete() }

// Reset returns the machine to its initial state.
func (m *StateMachine) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	require := m.requireEndOfEarlyData
	required := m.requiredSet
	*m = StateMachine{requireEndOfEarlyData: require, requiredSet: required}
}

// Validate checks that no violation occurred and the handshake reached
// Finished. An accepted early-data session that ends before EndOfEarlyData is
// reported as missing the boundary message.
func (m *StateMachine) Validate() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failure != nil {
		return m.failure
	}
	if m.earlyDataAccepted && !m.endOfEarlyData && m.appliesEndOfEarlyData() {
		return ErrMissingEndOfEarlyData
	}
	if !m.complete {
		return ErrHandshakeIncomplete
	}
	return nil
}

// ValidateSequence consumes a sequence supplied by a TLS stack and validates
// the final state. A boolean true argument marks early data as accepted.
func ValidateSequence(inputs ...any) error {
	m := NewStateMachine(false)
	for _, input := range inputs {
		if v, ok := input.(bool); ok && v {
			if err := m.AcceptEarlyData(); err != nil {
				return err
			}
		}
	}
	for _, input := range inputs {
		if _, ok := input.(bool); ok {
			continue
		}
		if err := m.Feed(input); err != nil {
			return err
		}
	}
	return m.Validate()
}

// Validate is an alias for ValidateSequence.
func Validate(inputs ...any) error { return ValidateSequence(inputs...) }

// CheckSequence is an alias for ValidateSequence.
func CheckSequence(inputs ...any) error { return ValidateSequence(inputs...) }

// ValidateHandshake is an alias for ValidateSequence.
func ValidateHandshake(inputs ...any) error { return ValidateSequence(inputs...) }

// ValidateEarlyDataSequence is an alias for ValidateSequence.
func ValidateEarlyDataSequence(inputs ...any) error { return ValidateSequence(inputs...) }

// DeleteMessage removes every occurrence of target from a sequence and
// returns the remaining sequence. It is useful for constructing negative
// tests where a message such as EndOfEarlyData is removed.
func DeleteMessage(target MessageType, inputs []any) []any {
	result := make([]any, 0, len(inputs))
	for _, input := range inputs {
		if t, ok := input.(MessageType); ok && t == target {
			continue
		}
		if t, ok := input.(*MessageType); ok && t != nil && *t == target {
			continue
		}
		if msg, ok := input.(Message); ok && msg.Type == target {
			continue
		}
		if msg, ok := input.(*Message); ok && msg != nil && msg.Type == target {
			continue
		}
		result = append(result, input)
	}
	return result
}

// RemoveMessage is an alias for DeleteMessage.
func RemoveMessage(target MessageType, inputs []any) []any {
	return DeleteMessage(target, inputs)
}

type internalEvent struct {
	typ       MessageType
	hasType   bool
	message   Message
	earlyData bool
	accepted  bool
	rejected  bool
	complete  bool
	noop      bool
}

func eventSlice(input any) ([]any, bool) {
	switch v := input.(type) {
	case []any:
		return v, true
	case [][]byte:
		result := make([]any, len(v))
		for i := range v {
			result[i] = v[i]
		}
		return result, true
	}
	rv := reflect.ValueOf(input)
	if rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array {
		return nil, false
	}
	if rv.Kind() == reflect.Slice && rv.Type().Elem().Kind() == reflect.Uint8 {
		return nil, false
	}
	result := make([]any, rv.Len())
	for i := 0; i < rv.Len(); i++ {
		result[i] = rv.Index(i).Interface()
	}
	return result, true
}

func decodeEvent(input any, depth int) (internalEvent, error) {
	if depth > 32 {
		return internalEvent{}, ErrUnknownEvent
	}
	if ev, ok := input.(internalEvent); ok {
		return ev, nil
	}
	switch v := input.(type) {
	case Message:
		return eventFromMessage(v), nil
	case *Message:
		if v == nil {
			return internalEvent{}, ErrUnknownEvent
		}
		return eventFromMessage(*v), nil
	case MessageType:
		return eventFromType(v), nil
	case *MessageType:
		if v == nil {
			return internalEvent{}, ErrUnknownEvent
		}
		return eventFromType(*v), nil
	case State:
		return eventFromState(v)
	case string:
		t, ok := messageTypeFromName(v)
		if !ok {
			if iv, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
				return eventFromType(MessageType(iv)), nil
			}
			return internalEvent{}, fmt.Errorf("%w: %q", ErrUnknownEvent, v)
		}
		return eventFromType(t), nil
	case bool:
		if v {
			return internalEvent{accepted: true}, nil
		}
		return internalEvent{rejected: true}, nil
	case []byte:
		t, ok := messageTypeFromName(string(v))
		if !ok {
			return internalEvent{}, fmt.Errorf("%w: %q", ErrUnknownEvent, string(v))
		}
		return eventFromType(t), nil
	case int:
		return eventFromType(MessageType(v)), nil
	case int8:
		return eventFromType(MessageType(v)), nil
	case int16:
		return eventFromType(MessageType(v)), nil
	case int32:
		return eventFromType(MessageType(v)), nil
	case int64:
		return eventFromType(MessageType(v)), nil
	case uint:
		return eventFromType(MessageType(v)), nil
	case uint8:
		return eventFromType(MessageType(v)), nil
	case uint16:
		return eventFromType(MessageType(v)), nil
	case uint32:
		return eventFromType(MessageType(v)), nil
	case uint64:
		return eventFromType(MessageType(v)), nil
	}

	rv := reflect.ValueOf(input)
	for rv.Kind() == reflect.Pointer || rv.Kind() == reflect.Interface {
		if rv.IsNil() {
			return internalEvent{}, ErrUnknownEvent
		}
		rv = rv.Elem()
	}
	switch rv.Kind() {
	case reflect.Struct:
		return decodeStruct(rv, depth+1)
	case reflect.Map:
		return decodeMap(rv, depth+1)
	default:
		return internalEvent{}, fmt.Errorf("%w: %T", ErrUnknownEvent, input)
	}
}

func eventFromType(t MessageType) internalEvent {
	ev := internalEvent{typ: t, hasType: true, message: Message{Type: t}}
	switch t {
	case MessageEarlyDataAccepted:
		ev.accepted = true
	case MessageEarlyDataRejected:
		ev.rejected = true
	case MessageHandshakeComplete:
		ev.complete = true
	case MessageEarlyData:
		ev.earlyData = true
	}
	return ev
}

func eventFromMessage(msg Message) internalEvent {
	ev := internalEvent{typ: msg.Type, hasType: msg.Type != MessageUnknown, message: msg}
	if msg.EarlyData {
		ev.earlyData = true
	}
	if msg.Accepted || msg.EarlyDataAccepted {
		ev.accepted = true
	}
	if msg.Rejected {
		ev.rejected = true
	}
	if msg.ApplicationData {
		ev.typ = MessageApplicationData
		ev.hasType = true
	}
	if msg.Type == MessageEarlyDataAccepted {
		ev.accepted = true
	}
	if msg.Type == MessageEarlyDataRejected {
		ev.rejected = true
	}
	if msg.Type == MessageHandshakeComplete {
		ev.complete = true
	}
	if msg.Type == MessageEncryptedExtensions && msg.EarlyData {
		ev.accepted = true
	}
	return ev
}

func eventFromState(s State) (internalEvent, error) {
	switch s {
	case StateInitial:
		return internalEvent{noop: true}, nil
	case StateClientHello:
		return eventFromType(MessageClientHello), nil
	case StateEarlyDataPending:
		return internalEvent{noop: true}, nil
	case StateEarlyData:
		return internalEvent{accepted: true}, nil
	case StateEndOfEarlyData:
		return eventFromType(MessageEndOfEarlyData), nil
	case StateHandshake:
		return internalEvent{noop: true}, nil
	case StateComplete:
		return eventFromType(MessageHandshakeComplete), nil
	case StateFailed:
		return internalEvent{}, ErrStateMachineFailed
	default:
		return internalEvent{}, fmt.Errorf("%w: state %d", ErrUnknownEvent, int(s))
	}
}

func decodeStruct(rv reflect.Value, depth int) (internalEvent, error) {
	ev := internalEvent{}
	handled := false

	for _, name := range []string{
		"Type", "MessageType", "HandshakeType", "MsgType", "HandshakeMessageType",
		"EventType", "Kind", "State", "Message", "Msg", "Name",
	} {
		if f := rv.FieldByName(name); f.IsValid() && f.CanInterface() {
			sub, err := decodeEvent(f.Interface(), depth+1)
			if err != nil {
				continue
			}
			if sub.hasType {
				ev.typ = sub.typ
				ev.hasType = true
				handled = true
			}
			if sub.accepted {
				ev.accepted = true
			}
			if sub.rejected {
				ev.rejected = true
			}
		}
	}

	for _, name := range []string{"EarlyDataAccepted", "Accepted", "AcceptedEarlyData", "SelectedEarlyData"} {
		if f := rv.FieldByName(name); f.IsValid() && f.CanInterface() {
			if b, ok := f.Interface().(bool); ok && b {
				ev.accepted = true
				handled = true
			}
		}
	}
	for _, name := range []string{"EarlyDataRejected", "Rejected"} {
		if f := rv.FieldByName(name); f.IsValid() && f.CanInterface() {
			if b, ok := f.Interface().(bool); ok && b {
				ev.rejected = true
				handled = true
			}
		}
	}
	for _, name := range []string{"EarlyData", "IsEarlyData", "EarlyDataRecord", "OfferedEarlyData"} {
		if f := rv.FieldByName(name); f.IsValid() && f.CanInterface() {
			if b, ok := f.Interface().(bool); ok && b {
				ev.earlyData = true
				handled = true
			}
		}
	}

	if !handled && rv.Type().Name() != "" {
		if t, ok := messageTypeFromName(rv.Type().Name()); ok {
			ev.typ = t
			ev.hasType = true
			handled = true
		}
	}
	if !handled {
		return internalEvent{}, fmt.Errorf("%w: %s", ErrUnknownEvent, rv.Type())
	}
	if ev.hasType {
		ev.message.Type = ev.typ
		if ev.earlyData {
			ev.message.EarlyData = true
		}
		if ev.accepted {
			ev.message.EarlyDataAccepted = true
		}
	}
	return ev, nil
}

func decodeMap(rv reflect.Value, depth int) (internalEvent, error) {
	ev := internalEvent{}
	handled := false
	iter := rv.MapRange()
	for iter.Next() {
		key := strings.ToLower(fmt.Sprint(iter.Key().Interface()))
		value := iter.Value().Interface()
		switch normalizeName(key) {
		case "type", "messagetype", "handshaketype", "msgtype", "handshakemessagetype",
			"eventtype", "kind", "message", "msg", "name":
			sub, err := decodeEvent(value, depth+1)
			if err != nil {
				continue
			}
			if sub.hasType {
				ev.typ = sub.typ
				ev.hasType = true
				handled = true
			}
		case "accepted", "earlydataaccepted", "acceptedearlydata", "selectedearlydata":
			if b, ok := value.(bool); ok && b {
				ev.accepted = true
				handled = true
			}
		case "rejected", "earlydatarejected", "rejectedearlydata":
			if b, ok := value.(bool); ok && b {
				ev.rejected = true
				handled = true
			}
		case "earlydata", "isearlydata", "offeredearlydata":
			if b, ok := value.(bool); ok && b {
				ev.earlyData = true
				handled = true
			}
		}
	}
	if !handled {
		return internalEvent{}, ErrUnknownEvent
	}
	return ev, nil
}
