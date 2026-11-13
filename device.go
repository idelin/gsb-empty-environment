package netsec

// Device is the device under test, injected by the caller. It may only
// observe and produce the modelled protocol state and report whether it
// executed a Write. proofOK carries the simulated verifier's verdict for
// Proof messages.
//
// Implementations must be deterministic: stepping the same state with the
// same message must always yield the same result. The explorer double-steps
// every transition and reports a NondeterminismError otherwise.
type Device interface {
	Step(st State, msg Message, proofOK bool) (next State, wrote bool)
}

// DeviceFunc adapts a plain function to Device.
type DeviceFunc func(st State, msg Message, proofOK bool) (State, bool)

// Step implements Device.
func (f DeviceFunc) Step(st State, msg Message, proofOK bool) (State, bool) {
	return f(st, msg, proofOK)
}
