package netsec

import (
	"bytes"
	"errors"
	"fmt"
)

const (
	// TimeWindow is the maximum permitted difference between the local and
	// received engineTime values, in seconds. RFC 3414 uses 150 seconds.
	TimeWindow = 150

	// MaxEngineBoots is the largest value that SNMPv3 permits for
	// snmpEngineBoots.
	MaxEngineBoots int32 = 2147483647

	// MaxEngineTime is the largest value that SNMPv3 permits for
	// snmpEngineTime.
	MaxEngineTime int32 = 2147483647
)

// ReportCode identifies a USM report generated while processing a message.
type ReportCode string

const (
	ReportNone                  ReportCode = ""
	ReportUnknownEngineID       ReportCode = "unknownEngineID"
	ReportNotInTimeWindow       ReportCode = "notInTimeWindow"
	ReportAuthenticationFailure ReportCode = "authenticationFailure"
	ReportWrongDigest                      = ReportAuthenticationFailure
	ReportWrongDigests                     = ReportAuthenticationFailure
)

// Common aliases make the report names convenient in callers that mirror the
// corresponding SNMP error names.
const (
	UnknownEngineID       = ReportUnknownEngineID
	NotInTimeWindow       = ReportNotInTimeWindow
	AuthenticationFailure = ReportAuthenticationFailure
	WrongDigest           = ReportAuthenticationFailure

	CodeUnknownEngineID       = ReportUnknownEngineID
	CodeNotInTimeWindow       = ReportNotInTimeWindow
	CodeAuthenticationFailure = ReportAuthenticationFailure
	CodeWrongDigest           = ReportAuthenticationFailure
)

// USM report object identifiers from RFC 3414.
const (
	OIDUnsupportedSecLevels = "1.3.6.1.6.3.15.1.1.1.0"
	OIDNotInTimeWindow      = "1.3.6.1.6.3.15.1.1.2.0"
	OIDUnknownUserName      = "1.3.6.1.6.3.15.1.1.3.0"
	OIDUnknownEngineID      = "1.3.6.1.6.3.15.1.1.4.0"
	OIDWrongDigest          = "1.3.6.1.6.3.15.1.1.5.0"
	OIDDecryptionError      = "1.3.6.1.6.3.15.1.1.6.0"

	OIDStatsUnsupportedSecLevels = OIDUnsupportedSecLevels
	OIDStatsNotInTimeWindows     = OIDNotInTimeWindow
	OIDStatsUnknownUserNames     = OIDUnknownUserName
	OIDStatsUnknownEngineIDs     = OIDUnknownEngineID
	OIDStatsWrongDigests         = OIDWrongDigest
	OIDStatsDecryptionErrors     = OIDDecryptionError
)

// Report describes the report PDU that the message entry point should send.
// The zero value means that no report is needed.
type Report struct {
	Code    ReportCode
	OID     string
	Counter uint32
}

var (
	// ErrUnknownEngineID, ErrNotInTimeWindow, and
	// ErrAuthenticationFailure can be used with errors.Is.
	ErrUnknownEngineID       = errors.New(string(ReportUnknownEngineID))
	ErrNotInTimeWindow       = errors.New(string(ReportNotInTimeWindow))
	ErrAuthenticationFailure = errors.New(string(ReportAuthenticationFailure))

	// Ready-to-compare report values. Internal code constructs fresh values
	// with the same contents, so callers cannot affect processing by changing
	// these variables.
	UnknownEngineIDReport       = Report{Code: ReportUnknownEngineID, OID: OIDUnknownEngineID, Counter: 1}
	NotInTimeWindowReport       = Report{Code: ReportNotInTimeWindow, OID: OIDNotInTimeWindow, Counter: 1}
	AuthenticationFailureReport = Report{Code: ReportAuthenticationFailure, OID: OIDWrongDigest, Counter: 1}
)

func (r Report) String() string {
	return string(r.Code)
}

// Error makes Report usable as an error value.
func (r Report) Error() string {
	return r.String()
}

func (r Report) Is(target error) bool {
	switch {
	case target == ErrUnknownEngineID:
		return r.Code == ReportUnknownEngineID
	case target == ErrNotInTimeWindow:
		return r.Code == ReportNotInTimeWindow
	case target == ErrAuthenticationFailure:
		return r.Code == ReportAuthenticationFailure
	default:
		return false
	}
}

// State is the caller-owned local USM engine state used for timeliness
// checking. EngineTime is normally maintained by the caller's clock; an
// accepted message may advance it, but it is never allowed to move backwards.
type State struct {
	EngineID    []byte
	EngineBoots int32
	EngineTime  int32
}

// Aliases for callers that use different terminology for local engine state.
type (
	LocalState  = State
	EngineState = State
	Engine      = State

	TimeWindowState = State
)

// NewState constructs local engine state.
func NewState(engineID []byte, engineBoots, engineTime int32) *State {
	return &State{
		EngineID:    append([]byte(nil), engineID...),
		EngineBoots: engineBoots,
		EngineTime:  engineTime,
	}
}

// New is an alias for NewState.
func New(engineID []byte, engineBoots, engineTime int32) *State {
	return NewState(engineID, engineBoots, engineTime)
}

// NewTimeWindow is an alias for NewState.
func NewTimeWindow(engineID []byte, engineBoots, engineTime int32) *State {
	return NewState(engineID, engineBoots, engineTime)
}

// NewEngine is an alias for NewState.
func NewEngine(engineID []byte, engineBoots, engineTime int32) *State {
	return NewState(engineID, engineBoots, engineTime)
}

// Message is the subset of an incoming SNMPv3 message needed by USM
// timeliness processing.
//
// The primary fields use short names. The Authoritative* and Msg* fields are
// accepted as aliases so this package can be called directly from code that
// uses the RFC field names.
type Message struct {
	EngineID    []byte
	EngineBoots int32
	EngineTime  int32
	UserName    string

	Discovery bool

	AuthenticationParameters []byte
	Data                     []byte

	AuthoritativeEngineID       []byte
	AuthoritativeEngineBoots    int32
	AuthoritativeEngineTime     int32
	MsgUserName                 string
	MsgAuthenticationParameters []byte
	DiscoveryRequest            bool
}

// Aliases for callers that use different names for an incoming message.
type (
	IncomingMessage = Message
	Request         = Message
	Packet          = Message
)

// AuthenticateFunc verifies the authentication parameters for msg. It must
// return true only after the digest/authentication check has succeeded.
type AuthenticateFunc func(Message) bool

// UserExistsFunc reports whether userName exists. It is optional; when it is
// not supplied, AuthenticateFunc is responsible for rejecting unknown users.
type UserExistsFunc func(userName string) bool

// UserAuthenticator is implemented by a user database that authenticates an
// incoming message.
type UserAuthenticator interface {
	Authenticate(Message) bool
}

// UserDirectory combines user lookup and authentication.
type UserDirectory interface {
	UserExists(string) bool
	Authenticate(Message) bool
}

// Result is the decision returned to the message entry point.
type Result struct {
	OK        bool
	Accepted  bool
	Discovery bool
	Synced    bool
	State     State
	Report    Report
}

// Err returns the report as an error, or nil for an accepted message.
func (r Result) Err() error {
	switch r.Report.Code {
	case ReportUnknownEngineID:
		return ErrUnknownEngineID
	case ReportNotInTimeWindow:
		return ErrNotInTimeWindow
	case ReportAuthenticationFailure:
		return ErrAuthenticationFailure
	default:
		if r.Accepted || r.OK {
			return nil
		}
		return ErrAuthenticationFailure
	}
}

func (r Result) Error() string {
	if err := r.Err(); err != nil {
		return err.Error()
	}
	return ""
}

func (r Result) Unwrap() error {
	return r.Err()
}

func newReport(code ReportCode, oid string) Report {
	return Report{Code: code, OID: oid, Counter: 1}
}

func unknownEngineIDReport() Report {
	return newReport(ReportUnknownEngineID, OIDUnknownEngineID)
}

func notInTimeWindowReport() Report {
	return newReport(ReportNotInTimeWindow, OIDNotInTimeWindow)
}

func authenticationFailureReport() Report {
	// Unknown users deliberately use the same report as bad authentication
	// data, preventing username enumeration through distinct USM reports.
	return newReport(ReportAuthenticationFailure, OIDWrongDigest)
}

func (m Message) normalized() Message {
	if len(m.EngineID) == 0 && len(m.AuthoritativeEngineID) != 0 {
		m.EngineID = m.AuthoritativeEngineID
	}
	if m.EngineBoots == 0 && m.AuthoritativeEngineBoots != 0 {
		m.EngineBoots = m.AuthoritativeEngineBoots
	}
	if m.EngineTime == 0 && m.AuthoritativeEngineTime != 0 {
		m.EngineTime = m.AuthoritativeEngineTime
	}
	if m.UserName == "" && m.MsgUserName != "" {
		m.UserName = m.MsgUserName
	}
	if m.AuthenticationParameters == nil && m.MsgAuthenticationParameters != nil {
		m.AuthenticationParameters = m.MsgAuthenticationParameters
	}
	if !m.Discovery && m.DiscoveryRequest {
		m.Discovery = true
	}
	return m
}

func sameEngineID(a, b []byte) bool {
	return bytes.Equal(a, b)
}

func engineTimeDifference(local, received int32) int64 {
	d := int64(received) - int64(local)
	if d < 0 {
		return -d
	}
	return d
}

// WindowReport checks only the engineBoots/engineTime fields. It returns the
// zero Report when the values are timely.
func WindowReport(localBoots, localTime, messageBoots, messageTime int32) Report {
	if localBoots == MaxEngineBoots ||
		localBoots < 0 ||
		localTime < 0 ||
		messageBoots < 0 ||
		messageTime < 0 ||
		messageBoots != localBoots ||
		engineTimeDifference(localTime, messageTime) > TimeWindow {
		return notInTimeWindowReport()
	}
	return Report{}
}

// InTimeWindow reports whether the message timeliness fields are acceptable.
// The 150-second boundary is inclusive; 151 seconds is outside the window.
func InTimeWindow(localBoots, localTime, messageBoots, messageTime int32) bool {
	return WindowReport(localBoots, localTime, messageBoots, messageTime).Code == ReportNone
}

// CheckTimeWindow is an alias for InTimeWindow.
func CheckTimeWindow(localBoots, localTime, messageBoots, messageTime int32) bool {
	return InTimeWindow(localBoots, localTime, messageBoots, messageTime)
}

// WithinTimeWindow is an alias for InTimeWindow.
func WithinTimeWindow(localBoots, localTime, messageBoots, messageTime int32) bool {
	return InTimeWindow(localBoots, localTime, messageBoots, messageTime)
}

// IsInTimeWindow is an alias for InTimeWindow.
func IsInTimeWindow(localBoots, localTime, messageBoots, messageTime int32) bool {
	return InTimeWindow(localBoots, localTime, messageBoots, messageTime)
}

// ValidateTimeWindow returns ErrNotInTimeWindow when the values are stale.
func ValidateTimeWindow(localBoots, localTime, messageBoots, messageTime int32) error {
	report := WindowReport(localBoots, localTime, messageBoots, messageTime)
	if report.Code == ReportNone {
		return nil
	}
	return report
}

// WindowReport checks the message against this local state.
func (s State) WindowReport(msg Message) Report {
	msg = msg.normalized()
	return WindowReport(s.EngineBoots, s.EngineTime, msg.EngineBoots, msg.EngineTime)
}

// InTimeWindow reports whether msg is within this local state's time window.
func (s State) InTimeWindow(msg Message) bool {
	return s.WindowReport(msg).Code == ReportNone
}

// CheckTimeWindow reports whether msg is within this local state's time
// window.
func (s State) CheckTimeWindow(msg Message) bool {
	return s.InTimeWindow(msg)
}

// ValidateTimeWindow returns ErrNotInTimeWindow when msg is stale.
func (s State) ValidateTimeWindow(msg Message) error {
	report := s.WindowReport(msg)
	if report.Code == ReportNone {
		return nil
	}
	return report
}

func resolveCallbacks(opts []any) (UserExistsFunc, AuthenticateFunc) {
	var exists UserExistsFunc
	var auth AuthenticateFunc

	for _, opt := range opts {
		switch cb := opt.(type) {
		case nil:
			continue
		case UserExistsFunc:
			exists = cb
		case func(string) bool:
			exists = UserExistsFunc(cb)
		case AuthenticateFunc:
			auth = cb
		case func(Message) bool:
			auth = AuthenticateFunc(cb)
		case func(*Message) bool:
			auth = func(m Message) bool { return cb(&m) }
		case func(string, []byte) bool:
			auth = func(m Message) bool { return cb(m.UserName, m.AuthenticationParameters) }
		case func(string, Message) bool:
			auth = func(m Message) bool { return cb(m.UserName, m) }
		case func(Message) error:
			auth = func(m Message) bool { return cb(m) == nil }
		case func(*Message) error:
			auth = func(m Message) bool { return cb(&m) == nil }
		case func(Message) (bool, error):
			auth = func(m Message) bool {
				ok, err := cb(m)
				return ok && err == nil
			}
		case UserDirectory:
			exists = cb.UserExists
			auth = cb.Authenticate
		case UserAuthenticator:
			auth = cb.Authenticate
		}
	}

	return exists, auth
}

func reject(state State, report Report) Result {
	return Result{
		OK:       false,
		Accepted: false,
		State:    state,
		Report:   report,
	}
}

func accept(state State, discovery, synced bool) Result {
	return Result{
		OK:        true,
		Accepted:  true,
		Discovery: discovery,
		Synced:    synced,
		State:     state,
		Report:    Report{},
	}
}

func stateFrom(local any) (State, *State, bool) {
	switch s := local.(type) {
	case State:
		return s, nil, true
	case *State:
		if s == nil {
			return State{}, nil, false
		}
		return *s, s, true
	default:
		return State{}, nil, false
	}
}

// Evaluate processes a message without mutating local. local may be a State
// or *State. The returned Result contains the state that should be committed
// by the caller.
//
// Optional callbacks may be passed as:
//   - one AuthenticateFunc, or
//   - one UserDirectory/UserAuthenticator, or
//   - a UserExistsFunc followed by an AuthenticateFunc.
//
// Authentication and user lookup happen before any timeliness decision or
// state update. Consequently an unauthenticated message can neither advance
// engineTime nor alter engineBoots. No callback is invoked for messages whose
// engine ID is unknown.
func Evaluate(msg Message, local any, opts ...any) Result {
	state, _, ok := stateFrom(local)
	if !ok {
		return reject(State{}, authenticationFailureReport())
	}
	return evaluate(msg, state, opts...)
}

func evaluate(msg Message, local State, opts ...any) Result {
	msg = msg.normalized()

	if !sameEngineID(msg.EngineID, local.EngineID) {
		// This is also the only result for discovery on an unknown engine.
		// In particular, do not probe usernames in this branch.
		result := reject(local, unknownEngineIDReport())
		result.Discovery = msg.Discovery
		return result
	}

	if msg.Discovery {
		return accept(local, true, false)
	}

	userExists, authenticate := resolveCallbacks(opts)

	if userExists != nil && !userExists(msg.UserName) {
		return reject(local, authenticationFailureReport())
	}
	if authenticate == nil || !authenticate(msg) {
		return reject(local, authenticationFailureReport())
	}

	// snmpEngineBoots is capped at 2^31-1. Once that value has been reached,
	// authenticated messages must not be accepted.
	if local.EngineBoots == MaxEngineBoots {
		return reject(local, notInTimeWindowReport())
	}

	if report := WindowReport(local.EngineBoots, local.EngineTime, msg.EngineBoots, msg.EngineTime); report.Code != ReportNone {
		return reject(local, report)
	}

	// The time window is monotonic local state. A valid message may advance
	// the caller's notion of engineTime, but must never roll it backwards.
	next := local
	next.EngineID = append([]byte(nil), local.EngineID...)
	synced := false
	if msg.EngineTime > next.EngineTime {
		next.EngineTime = msg.EngineTime
		synced = true
	}

	return accept(next, false, synced)
}

// EvaluateWithUsers is the explicit form of Evaluate for a known user store.
func EvaluateWithUsers(msg Message, local any, userExists UserExistsFunc, authenticate AuthenticateFunc) Result {
	return Evaluate(msg, local, userExists, authenticate)
}

// Check is an alias for Evaluate.
func Check(msg Message, local any, opts ...any) Result {
	return Evaluate(msg, local, opts...)
}

// ProcessMessage evaluates msg and commits the resulting state only when the
// message is accepted. If local is a *State, the accepted state is committed
// to it; a value State cannot be mutated. On every report path local is left
// unchanged.
func ProcessMessage(msg Message, local any, opts ...any) Result {
	state, target, ok := stateFrom(local)
	if !ok {
		return reject(State{}, authenticationFailureReport())
	}
	result := evaluate(msg, state, opts...)
	if result.Accepted && target != nil {
		*target = result.State
	}
	return result
}

// Process is an alias for ProcessMessage.
func Process(msg Message, local any, opts ...any) Result {
	return ProcessMessage(msg, local, opts...)
}

// Check commits an accepted result to the receiver. It is the method form of
// ProcessMessage.
func (s *State) Check(msg Message, opts ...any) Result {
	if s == nil {
		return reject(State{}, authenticationFailureReport())
	}
	return ProcessMessage(msg, s, opts...)
}

// Process is the method form of ProcessMessage.
func (s *State) Process(msg Message, opts ...any) Result {
	return s.Check(msg, opts...)
}

// ProcessMessage is the method form of ProcessMessage.
func (s *State) ProcessMessage(msg Message, opts ...any) Result {
	return s.Check(msg, opts...)
}

// Validate is the method form of Process.
func (s *State) Validate(msg Message, opts ...any) Result {
	return s.Check(msg, opts...)
}

// Format implements fmt.Formatter so engine IDs are easy to inspect in logs.
func (s State) Format(f fmt.State, verb rune) {
	switch verb {
	case 'v', 's':
		fmt.Fprintf(f, "engineID=%x boots=%d time=%d", s.EngineID, s.EngineBoots, s.EngineTime)
	default:
		fmt.Fprintf(f, "%%!%c(netsec.State)", verb)
	}
}
