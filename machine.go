package netsec

import (
	"errors"
	"fmt"
	"net/url"
	"sync"
)

// Phase is the state machine phase after processing an authorization state.
type Phase string

const (
	PhaseIdle       Phase = "idle"
	PhaseAuthorized Phase = "authorized"
	PhaseRejected   Phase = "rejected"
)

// ServiceState is the authorization state supplied by the authorization
// service. State is the opaque OAuth state value and is preserved verbatim.
type ServiceState struct {
	ResponseType            string
	State                   string
	RegisteredResponseTypes []string
}

// AuthorizeRequest is an alias for ServiceState.
type AuthorizeRequest = ServiceState

// Result contains the policy decision and the resulting machine phase.
type Result struct {
	Decision
	Phase Phase
	State string
}

// AuthorizationResult is an alias for Result.
type AuthorizationResult = Result

// StateMachine evaluates authorization service state and records whether the
// request was accepted. It is safe for concurrent use.
type StateMachine struct {
	mu     sync.Mutex
	policy *Policy
	phase  Phase
}

// Machine is an alias for StateMachine.
type Machine = StateMachine

// NewStateMachine creates a state machine with a default set of registered
// hybrid response types.
func NewStateMachine(registeredHybrids ...string) *StateMachine {
	return &StateMachine{
		policy: NewPolicy(registeredHybrids...),
		phase:  PhaseIdle,
	}
}

// NewMachine is an alias for NewStateMachine.
func NewMachine(registeredHybrids ...string) *StateMachine {
	return NewStateMachine(registeredHybrids...)
}

// New creates a state machine with the supplied registered hybrid modes.
func New(registeredHybrids ...string) *StateMachine {
	return NewStateMachine(registeredHybrids...)
}

// NewStateMachineWithPolicy creates a state machine around an existing policy.
func NewStateMachineWithPolicy(policy *Policy) *StateMachine {
	if policy == nil {
		policy = NewPolicy()
	}
	return &StateMachine{policy: policy, phase: PhaseIdle}
}

// Phase returns the current state machine phase.
func (m *StateMachine) Phase() Phase {
	if m == nil {
		return PhaseIdle
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.phase
}

// Reset returns the machine to PhaseIdle.
func (m *StateMachine) Reset() {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.phase = PhaseIdle
}

// Policy returns the machine's default policy.
func (m *StateMachine) Policy() *Policy {
	if m == nil {
		return nil
	}
	return m.policy
}

// Apply processes authorization state from the authorization service. When the
// input supplies RegisteredResponseTypes, those registrations replace the
// machine default for this request; a nil slice keeps the default.
func (m *StateMachine) Apply(state ServiceState) Result {
	if m == nil {
		decision := Decision{}.denied(state.ResponseType, ReasonUnsupportedResponseType,
			"nil state machine")
		return Result{Decision: decision, Phase: PhaseRejected, State: state.State}
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	policy := m.policy
	if state.RegisteredResponseTypes != nil {
		policy = NewPolicyFrom(state.RegisteredResponseTypes)
	}

	decision := policy.Evaluate(state.ResponseType)
	phase := PhaseRejected
	if decision.Allowed {
		phase = PhaseAuthorized
	}
	m.phase = phase

	return Result{
		Decision: decision,
		Phase:    phase,
		State:    state.State,
	}
}

// Transition is an alias for Apply.
func (m *StateMachine) Transition(state ServiceState) Result {
	return m.Apply(state)
}

// Authorize is an alias for Apply.
func (m *StateMachine) Authorize(state ServiceState) Result {
	return m.Apply(state)
}

// Handle is an alias for Apply.
func (m *StateMachine) Handle(state ServiceState) Result {
	return m.Apply(state)
}

// FrontChannelResponse is an authorization response that is safe to hand to
// the user agent. It deliberately has no access-token field.
type FrontChannelResponse struct {
	ResponseMode ResponseMode
	Code         string
	IDToken      string
	State        string
}

// NewFrontChannelResponse builds a response for an allowed decision. A
// standalone code response carries only code. A hybrid response must carry
// both code and id_token. Neither form can carry an access token.
func (d Decision) NewFrontChannelResponse(code, idToken, state string) (FrontChannelResponse, error) {
	if !d.Allowed {
		if d.Err != nil {
			return FrontChannelResponse{}, d.Err
		}
		return FrontChannelResponse{}, ErrUnsupportedResponseType
	}

	switch d.Flow {
	case FlowAuthorizationCode:
		if code == "" {
			return FrontChannelResponse{}, errors.New("netsec: code response requires a code")
		}
		if idToken != "" {
			return FrontChannelResponse{}, fmt.Errorf(
				"netsec: id_token is not permitted for %q", d.ResponseType)
		}
		return FrontChannelResponse{
			ResponseMode: ResponseModeQuery,
			Code:         code,
			State:        state,
		}, nil

	case FlowHybrid:
		if code == "" || idToken == "" {
			return FrontChannelResponse{}, errors.New(
				"netsec: hybrid response requires both code and id_token")
		}
		return FrontChannelResponse{
			ResponseMode: ResponseModeFragment,
			Code:         code,
			IDToken:      idToken,
			State:        state,
		}, nil

	default:
		return FrontChannelResponse{}, ErrUnsupportedResponseType
	}
}

// NewFrontChannelResponse builds a response using the OAuth state carried by
// the authorization service.
func (r Result) NewFrontChannelResponse(code, idToken string) (FrontChannelResponse, error) {
	return r.Decision.NewFrontChannelResponse(code, idToken, r.State)
}

// Parameters returns the front-channel parameters. Access tokens are never
// included.
func (r FrontChannelResponse) Parameters() url.Values {
	values := make(url.Values)
	if r.Code != "" {
		values.Set(ResponseTypeCode, r.Code)
	}
	if r.IDToken != "" {
		values.Set(ResponseTypeIDToken, r.IDToken)
	}
	if r.State != "" {
		values.Set("state", r.State)
	}
	return values
}
