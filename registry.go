package netsec

import (
	"crypto/rsa"
	"fmt"
	"math/big"
	"sort"
)

// Recipient registers a recipient key name with its RSA public key.
type Recipient struct {
	KeyName   string
	PublicKey *rsa.PublicKey
}

// RecipientPrivateKey registers a recipient key name with its RSA private key.
// The public half must match the key registered under the same name.
type RecipientPrivateKey struct {
	KeyName    string
	PrivateKey *rsa.PrivateKey
}

// Registry is an immutable set of authorized recipient key names and public
// keys shared by producers and consumers.
type Registry struct {
	names []string
	keys  map[string]*rsa.PublicKey
}

// NewRegistry creates an immutable recipient registry. Key names must be
// non-empty and unique.
func NewRegistry(recipients []Recipient) (*Registry, error) {
	if len(recipients) == 0 {
		return nil, fmt.Errorf("%w: no recipients", ErrInvalidConfig)
	}

	registry := &Registry{
		names: make([]string, 0, len(recipients)),
		keys:  make(map[string]*rsa.PublicKey, len(recipients)),
	}
	for _, recipient := range recipients {
		if recipient.KeyName == "" {
			return nil, fmt.Errorf("%w: empty recipient key name", ErrInvalidConfig)
		}
		if _, exists := registry.keys[recipient.KeyName]; exists {
			return nil, fmt.Errorf("%w: duplicate recipient %q", ErrInvalidConfig, recipient.KeyName)
		}
		if err := validateRSAPublicKey(recipient.PublicKey); err != nil {
			return nil, fmt.Errorf("%w: recipient %q: %v", ErrInvalidConfig, recipient.KeyName, err)
		}

		registry.keys[recipient.KeyName] = cloneRSAPublicKey(recipient.PublicKey)
		registry.names = append(registry.names, recipient.KeyName)
	}
	sort.Strings(registry.names)
	return registry, nil
}

// Names returns a copy of the registered key names in lexicographic order.
func (r *Registry) Names() []string {
	return append([]string(nil), r.names...)
}

func (r *Registry) publicKey(name string) (*rsa.PublicKey, bool) {
	if r == nil {
		return nil, false
	}
	key, ok := r.keys[name]
	return key, ok
}

func validateRSAPublicKey(key *rsa.PublicKey) error {
	if key == nil {
		return fmt.Errorf("nil public key")
	}
	if key.N == nil || key.N.Sign() <= 0 {
		return fmt.Errorf("invalid RSA modulus")
	}
	if key.E < 2 {
		return fmt.Errorf("invalid RSA exponent")
	}
	return nil
}

func cloneRSAPublicKey(key *rsa.PublicKey) *rsa.PublicKey {
	return &rsa.PublicKey{
		N: new(big.Int).Set(key.N),
		E: key.E,
	}
}
