package netsec

// Adapter decapsulates canonical-length ciphertexts of one KEM component.
//
// Implementations are registered by the handshake host for a fixed suite ID.
// For any ciphertext of the canonical length registered for the component, an
// Adapter must always return a 32-byte shared secret plus a validity flag.
// On a decapsulation failure it must perform implicit rejection: it returns
// false together with its implicit-rejection secret rather than an error, an
// empty secret, or a shortened secret.
//
// The package rejects non-canonical lengths before invoking an Adapter, so
// implementations only ever receive ciphertexts of the configured length.
type Adapter interface {
	// Decapsulate returns the 32-byte shared secret and whether the
	// ciphertext was accepted. It never returns a secret shorter than 32
	// bytes; failures use the implicit-rejection secret and false.
	Decapsulate(ciphertext []byte) (sharedSecret [32]byte, valid bool)
}

// AdapterFunc adapts an ordinary function to the Adapter interface.
type AdapterFunc func(ciphertext []byte) (sharedSecret [32]byte, valid bool)

// Decapsulate calls the underlying function.
func (f AdapterFunc) Decapsulate(ciphertext []byte) (sharedSecret [32]byte, valid bool) {
	return f(ciphertext)
}

// FailureMask records which KEM components failed decapsulation.
//
// Bit 0 (MaskComponent1) is set when the first component reports invalid and
// bit 1 (MaskComponent2) when the second component reports invalid. The zero
// value means both components were valid. These bits are the only failure
// detail retained internally; no failure detail is returned to callers.
type FailureMask uint8

const (
	// MaskComponent1 marks a failure of the first KEM component.
	MaskComponent1 FailureMask = 1 << iota
	// MaskComponent2 marks a failure of the second KEM component.
	MaskComponent2
)

// Component1 reports whether the first component failed.
func (m FailureMask) Component1() bool { return (m & MaskComponent1) != 0 }

// Component2 reports whether the second component failed.
func (m FailureMask) Component2() bool { return (m & MaskComponent2) != 0 }

// String renders the mask as a two-bit string, e.g. "00", "01", "10", "11".
func (m FailureMask) String() string {
	return []string{"00", "01", "10", "11"}[m&(MaskComponent1|MaskComponent2)]
}
