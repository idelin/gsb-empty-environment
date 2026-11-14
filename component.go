package netsec

// Canonicalizer is a small stateless component for services that prefer
// methods over package-level functions. The rules are intentionally not
// configurable: there is exactly one canonical encoding.
type Canonicalizer struct{}

// NewCanonicalizer returns the fixed canonicalizer.
func NewCanonicalizer() *Canonicalizer {
	return &Canonicalizer{}
}

// Canonicalize returns the canonical bytes for input.
func (Canonicalizer) Canonicalize(input any) ([]byte, error) {
	return Canonicalize(input)
}

// Validate checks that input is already canonical.
func (Canonicalizer) Validate(input any) error {
	return ValidateCanonical(input)
}

// ValidateQuery checks that a raw query is already canonical.
func (Canonicalizer) ValidateQuery(raw string) error {
	return ValidateCanonicalQuery(raw)
}

// Sign canonicalizes input and signs it with secret.
func (Canonicalizer) Sign(secret []byte, input any) ([]byte, error) {
	return Sign(secret, input)
}

// Verify canonicalizes input and verifies signature with secret.
func (Canonicalizer) Verify(secret []byte, input any, signature []byte) (bool, error) {
	return Verify(secret, input, signature)
}

// EscapeComponent escapes one logical key or value.
func (Canonicalizer) EscapeComponent(component string) (string, error) {
	return EscapeComponent(component)
}
