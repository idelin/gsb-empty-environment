package netsec

// StateMachine, Store, AuthService, and AuthorizationService are aliases for
// Machine so the package can be embedded under the name used by the caller.
type (
	StateMachine         = Machine
	Store                = Machine
	AuthService          = Machine
	AuthorizationService = Machine
)

// NewStore returns an empty authorization-code store.
func NewStore(codes ...string) *Machine { return NewMachine(codes...) }

// NewStateMachine returns an empty code state machine.
func NewStateMachine(codes ...string) *Machine { return NewMachine(codes...) }

// NewAuthService returns an empty authorization service.
func NewAuthService(codes ...string) *Machine { return NewMachine(codes...) }

// AddCode and Register are aliases for Issue.
func (m *Machine) AddCode(code string) error  { return m.Issue(code) }
func (m *Machine) Register(code string) error { return m.Issue(code) }

// RedeemCode, Exchange, and Consume are aliases for Redeem.
func (m *Machine) RedeemCode(code string) (Token, error) { return m.Redeem(code) }
func (m *Machine) Exchange(code string) (Token, error)   { return m.Redeem(code) }
func (m *Machine) Consume(code string) (Token, error)    { return m.Redeem(code) }

// IsValid and TokenValid are aliases for Validate.
func (m *Machine) IsValid(token Token) bool    { return m.Validate(token) }
func (m *Machine) TokenValid(token Token) bool { return m.Validate(token) }

// RevokeToken is an alias for Revoke.
func (m *Machine) RevokeToken(token Token) error { return m.Revoke(token) }

// GetState is an alias for StateOf.
func (m *Machine) GetState(code string) (State, bool) { return m.StateOf(code) }

// Transition is an alias for Apply.
func (m *Machine) Transition(event Event) Result { return m.Apply(event) }
