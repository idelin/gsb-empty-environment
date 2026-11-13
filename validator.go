package netsec

import (
	"bytes"
	"sync"
)

// Message is the subset of an SNMPv3 USM message needed for discovery and
// timeliness processing. EngineTime is measured in seconds.
type Message struct {
	EngineID    []byte
	Username    string
	EngineBoots int32
	EngineTime  int64
	Discovery   bool
}

// Request is an alias for Message.
type Request = Message

// Authenticator is the caller's authentication callback. Returning true means
// that the message's user, credentials, and integrity check are valid.
//
// NewValidator and Process also accept a few convenience callback forms,
// including func(username string) bool and func(Message) bool.
type Authenticator func(Message) bool

// AuthFunc and AuthCallback are aliases for Authenticator.
type (
	AuthFunc     = Authenticator
	AuthCallback = Authenticator
)

type authChecker func(Message) bool

// Validator holds the local engine identity, caller-supplied local time, and
// the synchronized time state for an SNMPv3 USM ingress point.
//
// A Validator is safe for concurrent use. Its authentication callback is
// invoked without internal locks held, so the callback may call SyncState.
type Validator struct {
	mu           sync.RWMutex
	localID      []byte
	local        TimeState
	synchronized TimeState
	auth         authChecker
}

// NewValidator constructs a message-entry validator.
//
// localEngineID accepts []byte or string. localEngineBoots and
// localEngineTime are supplied by the caller and are used as the local
// authoritative values. auth may be:
//
//   - nil (all non-discovery messages fail authentication)
//   - func(Message) bool
//   - Authenticator
//   - func(string) bool
//   - func(string, Message) bool
//   - func() bool
//   - the equivalents returning an error, where a nil error means success
func NewValidator(localEngineID any, localEngineBoots int32, localEngineTime int64, auth any) *Validator {
	return &Validator{
		localID:      normalizeEngineID(localEngineID),
		local:        TimeState{EngineBoots: localEngineBoots, EngineTime: localEngineTime},
		synchronized: TimeState{EngineBoots: localEngineBoots, EngineTime: localEngineTime},
		auth:         normalizeAuthenticator(auth),
	}
}

// New is an alias for NewValidator.
func New(localEngineID any, localEngineBoots int32, localEngineTime int64, auth any) *Validator {
	return NewValidator(localEngineID, localEngineBoots, localEngineTime, auth)
}

// NewTimeWindow is an alias for NewValidator.
func NewTimeWindow(localEngineID any, localEngineBoots int32, localEngineTime int64, auth any) *Validator {
	return NewValidator(localEngineID, localEngineBoots, localEngineTime, auth)
}

// NewUSMTimeWindow is an alias for NewValidator.
func NewUSMTimeWindow(localEngineID any, localEngineBoots int32, localEngineTime int64, auth any) *Validator {
	return NewValidator(localEngineID, localEngineBoots, localEngineTime, auth)
}

// LocalEngineID returns a copy of the engine ID managed by this validator.
func (v *Validator) LocalEngineID() []byte {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return bytes.Clone(v.localID)
}

// LocalState returns the caller-supplied local time state.
func (v *Validator) LocalState() TimeState {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.local
}

// SyncState returns the last successfully authenticated synchronized state.
// Failed authentication and failed time-window checks never change it.
func (v *Validator) SyncState() TimeState {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.synchronized
}

// SynchronizedState is an alias for SyncState.
func (v *Validator) SynchronizedState() TimeState {
	return v.SyncState()
}

// SetLocalState replaces the caller-supplied local engine time. It also
// resets the synchronized state to the new caller-supplied values.
func (v *Validator) SetLocalState(state TimeState) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.local = state
	v.synchronized = state
}

// SetLocalTime replaces the caller-supplied local engineBoots and engineTime.
func (v *Validator) SetLocalTime(engineBoots int32, engineTime int64) {
	v.SetLocalState(TimeState{EngineBoots: engineBoots, EngineTime: engineTime})
}

// Process validates a USM message at the ingress point.
//
// Unknown engine IDs are handled before authentication. A discovery request
// receives only UnknownEngineID and never invokes the authentication callback.
// For messages for the local engine, authentication runs first. Only after it
// succeeds may the message's engineBoots and engineTime be examined and, on
// success, copied into the synchronized state.
//
// The optional auth argument overrides the callback supplied to
// NewValidator for this call. It accepts the same callback forms as
// NewValidator, or a bool containing the result of authentication already
// performed by the caller.
func (v *Validator) Process(msg Message, auth ...any) Report {
	v.mu.RLock()
	localID := bytes.Clone(v.localID)
	check := v.auth
	v.mu.RUnlock()

	if len(auth) > 0 {
		check = normalizeAuthenticator(auth[0])
		if authenticated, ok := auth[0].(bool); ok {
			if !authenticated {
				return AuthenticationFailed
			}
			check = func(Message) bool { return true }
		}
	}

	if !bytes.Equal(localID, normalizeEngineID(msg.EngineID)) {
		// Discovery must not reveal users. Do not call the authentication callback
		// here, even to determine whether the username exists.
		return UnknownEngineID
	}

	if check == nil || !check(msg) {
		// Missing users and bad credentials both map to this single report.
		return AuthenticationFailed
	}

	v.mu.Lock()
	defer v.mu.Unlock()

	if v.local.EngineBoots == MaxEngineBoots {
		return NotInTimeWindow
	}
	if msg.EngineBoots != v.local.EngineBoots {
		return NotInTimeWindow
	}
	if absDifference(msg.EngineTime, v.local.EngineTime) > TimeWindowSeconds {
		return NotInTimeWindow
	}

	v.synchronized = TimeState{EngineBoots: msg.EngineBoots, EngineTime: msg.EngineTime}
	return ReportOK
}

// ProcessWithLocal validates against an explicitly supplied local time state.
// This is useful when the caller obtains engineBoots and engineTime from its
// own engine clock at ingress rather than preconfiguring them.
func (v *Validator) ProcessWithLocal(local TimeState, msg Message, auth ...any) Report {
	v.mu.RLock()
	localID := bytes.Clone(v.localID)
	check := v.auth
	v.mu.RUnlock()

	if len(auth) > 0 {
		check = normalizeAuthenticator(auth[0])
		if authenticated, ok := auth[0].(bool); ok {
			if !authenticated {
				return AuthenticationFailed
			}
			check = func(Message) bool { return true }
		}
	}

	if !bytes.Equal(localID, normalizeEngineID(msg.EngineID)) {
		return UnknownEngineID
	}
	if check == nil || !check(msg) {
		return AuthenticationFailed
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if local.EngineBoots == MaxEngineBoots {
		return NotInTimeWindow
	}
	if msg.EngineBoots != local.EngineBoots {
		return NotInTimeWindow
	}
	if absDifference(msg.EngineTime, local.EngineTime) > TimeWindowSeconds {
		return NotInTimeWindow
	}
	v.local = local
	v.synchronized = TimeState{EngineBoots: msg.EngineBoots, EngineTime: msg.EngineTime}
	return ReportOK
}

// Handle is an alias for Process.
func (v *Validator) Handle(msg Message, auth ...any) Report {
	return v.Process(msg, auth...)
}

// Validate is an alias for Process.
func (v *Validator) Validate(msg Message, auth ...any) Report {
	return v.Process(msg, auth...)
}

// CheckMessage is an alias for Process.
func (v *Validator) CheckMessage(msg Message, auth ...any) Report {
	return v.Process(msg, auth...)
}

func normalizeEngineID(value any) []byte {
	switch id := value.(type) {
	case nil:
		return nil
	case []byte:
		return bytes.Clone(id)
	case string:
		return []byte(id)
	case [12]byte:
		return id[:]
	case [8]byte:
		return id[:]
	default:
		return nil
	}
}

func normalizeAuthenticator(auth any) authChecker {
	switch f := auth.(type) {
	case nil:
		return nil
	case Authenticator:
		if f == nil {
			return nil
		}
		return authChecker(f)
	case func(Message) bool:
		if f == nil {
			return nil
		}
		return authChecker(f)
	case func(string) bool:
		if f == nil {
			return nil
		}
		return func(msg Message) bool { return f(msg.Username) }
	case func(string, Message) bool:
		if f == nil {
			return nil
		}
		return func(msg Message) bool { return f(msg.Username, msg) }
	case func() bool:
		if f == nil {
			return nil
		}
		return func(Message) bool { return f() }
	case func(Message) error:
		if f == nil {
			return nil
		}
		return func(msg Message) bool { return f(msg) == nil }
	case func(string) error:
		if f == nil {
			return nil
		}
		return func(msg Message) bool { return f(msg.Username) == nil }
	case func(string, Message) error:
		if f == nil {
			return nil
		}
		return func(msg Message) bool { return f(msg.Username, msg) == nil }
	case func(Message) (bool, error):
		if f == nil {
			return nil
		}
		return func(msg Message) bool {
			ok, err := f(msg)
			return err == nil && ok
		}
	case func(string) (bool, error):
		if f == nil {
			return nil
		}
		return func(msg Message) bool {
			ok, err := f(msg.Username)
			return err == nil && ok
		}
	case func(string, Message) (bool, error):
		if f == nil {
			return nil
		}
		return func(msg Message) bool {
			ok, err := f(msg.Username, msg)
			return err == nil && ok
		}
	default:
		return nil
	}
}
