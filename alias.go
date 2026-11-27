package streamguard

// Terminology aliases for hosts integrating the package under security or
// protocol-specific names.
type (
	// SafeParser is the bounded incremental XML parser.
	SafeParser = Parser
	// IncrementalParser is an alias for Parser.
	IncrementalParser = Parser
	// StreamDefender is the StartTLS-aware guarded connection.
	StreamDefender = Guard
	// StartTLSStateMachine enforces the handshake order.
	StartTLSStateMachine = Machine
)

// NewSafeParser creates a bounded incremental parser.
func NewSafeParser(limits ...Limits) *SafeParser {
	return NewParser(limits...)
}

// NewStreamDefender creates a Guard.
func NewStreamDefender(config ...Config) *StreamDefender {
	return NewGuard(config...)
}

// NewStartTLSStateMachine creates the ordered state machine.
func NewStartTLSStateMachine(config ...Config) *StartTLSStateMachine {
	return NewMachine(config...)
}
