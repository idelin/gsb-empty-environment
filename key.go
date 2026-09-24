package canonkey

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
)

var (
	// ErrMissingTenant reports a request without a tenant.
	ErrMissingTenant = errors.New("canonkey: tenant is required")
	// ErrMissingConfigVersion reports a request without a config version.
	// Omitting it is rejected fail-closed because versionless keys can
	// wrongly hit entries produced under a different configuration.
	ErrMissingConfigVersion = errors.New("canonkey: config version is required")
)

// Request is a structured request to be keyed. Params == nil means the
// parameter set is absent; Params == Null means an explicit JSON null.
// The two never encode to the same key.
type Request struct {
	Tenant        string
	ConfigVersion string
	Params        any
}

// Key is an unambiguous request key. Digest indexes storage; Canon holds
// the full canonical bytes so a forced digest collision can be detected
// and re-verified against the original canonical bytes.
type Key struct {
	Digest [32]byte
	Canon  []byte
}

func (k Key) String() string { return "k1:" + hex.EncodeToString(k.Digest[:]) }

// Keyer builds keys. Hash is injectable (default SHA-256), which lets
// tests force digest collisions to exercise re-verification.
type Keyer struct {
	Hash   func([]byte) [32]byte
	Limits Limits
}

// NewKeyer returns a Keyer with SHA-256 and the given limits.
func NewKeyer(lim Limits) *Keyer {
	return &Keyer{Hash: sha256.Sum256, Limits: lim.withDefaults()}
}

func (k *Keyer) hash() func([]byte) [32]byte {
	if k.Hash != nil {
		return k.Hash
	}
	return sha256.Sum256
}

// CanonicalRequest returns the canonical bytes of the key envelope:
//
//	k1( <tenant:string> <configVersion:string> <params|'m'> )
//
// 'm' marks absent params, distinct from explicit null ('z').
func (k *Keyer) CanonicalRequest(r Request) ([]byte, error) {
	if r.Tenant == "" {
		return nil, ErrMissingTenant
	}
	if r.ConfigVersion == "" {
		return nil, ErrMissingConfigVersion
	}
	st := newCState(k.Limits)
	st.buf.WriteString("k1(")
	if err := st.encString(r.Tenant); err != nil {
		return nil, err
	}
	if err := st.encString(r.ConfigVersion); err != nil {
		return nil, err
	}
	if r.Params == nil {
		st.buf.WriteByte('m')
	} else {
		if err := st.encode(r.Params, 1); err != nil {
			return nil, err
		}
	}
	st.buf.WriteByte(')')
	return st.buf.Bytes(), nil
}

// BuildKey canonicalizes r and returns its key.
func (k *Keyer) BuildKey(r Request) (Key, error) {
	canon, err := k.CanonicalRequest(r)
	if err != nil {
		return Key{}, err
	}
	return Key{Digest: k.hash()(canon), Canon: canon}, nil
}

// MustBuildKey is BuildKey that panics on error; for tests and demos.
func (k *Keyer) MustBuildKey(r Request) Key {
	key, err := k.BuildKey(r)
	if err != nil {
		panic(fmt.Sprintf("canonkey: %v", err))
	}
	return key
}
