package migrationadmission

import (
	"crypto/ed25519"
	"fmt"
)

// VerifiedBundle is a bundle that has passed every independent loader check.
type VerifiedBundle struct {
	bundle    Bundle
	oldPolicy *compiledPolicy
	newPolicy *compiledPolicy
	aliases   map[string]bool
}

// Verify independently checks the signed admission bundle:
//
//  1. envelope and per-claim root signatures;
//  2. binding of both grammar/table versions, tenant and epoch;
//  3. the existence of at least one emit-able login identifier;
//  4. the product-automaton collision analysis (deletion, expansion, end
//     marker and unequal-length inputs included), without enumerating names;
//  5. exact agreement of the lateness certificate with the observed backlog,
//     rejecting any backlog above MaxLateness.
func Verify(wire []byte, trustedRoot ed25519.PublicKey) (*VerifiedBundle, error) {
	signed, err := UnmarshalSignedBundle(wire)
	if err != nil {
		return nil, err
	}
	bundle, err := signed.Unmarshal()
	if err != nil {
		return nil, err
	}
	if trustedRoot != nil && !trustedRoot.Equal(bundle.RootKey) {
		return nil, ErrInvalidSignature
	}
	if bundle.TenantID == "" {
		return nil, fmt.Errorf("migrationadmission: empty tenant id")
	}
	oldCompiled, err := bundle.OldPolicy.compile()
	if err != nil {
		return nil, fmt.Errorf("migrationadmission: old policy invalid: %w", err)
	}
	newCompiled, err := bundle.NewPolicy.compile()
	if err != nil {
		return nil, fmt.Errorf("migrationadmission: new policy invalid: %w", err)
	}
	if !newCompiled.hasAcceptingWord() {
		return nil, ErrNoLoginIdentifier
	}
	if !newCompiled.hasEmittingWord() {
		return nil, ErrNoLoginIdentifier
	}

	aliases, err := verifyAliases(bundle)
	if err != nil {
		return nil, err
	}
	if err := verifyLateness(bundle); err != nil {
		return nil, err
	}

	result := analyzeProduct(productConfig{
		oldPolicy: oldCompiled,
		newPolicy: newCompiled,
		aliases:   aliases,
	})
	if result.Violation != nil {
		return nil, result.Violation
	}
	if bundle.Lateness.MaxBacklog != result.MaxBacklog {
		return nil, fmt.Errorf("%w: declared %d, observed %d",
			ErrWrongLatenessBound, bundle.Lateness.MaxBacklog, result.MaxBacklog)
	}

	return &VerifiedBundle{
		bundle:    bundle,
		oldPolicy: oldCompiled,
		newPolicy: newCompiled,
		aliases:   aliases,
	}, nil
}

func verifyAliases(bundle Bundle) (map[string]bool, error) {
	serials := map[string]bool{}
	aliases := make(map[string]bool, len(bundle.Aliases))
	for _, claim := range bundle.Aliases {
		if claim.TenantID != bundle.TenantID {
			return nil, ErrAliasProofIncomplete
		}
		if claim.PolicyVersion != bundle.NewPolicy.Version {
			return nil, fmt.Errorf("%w: claim targets version %q, bundle has %q",
				ErrAliasProofIncomplete, claim.PolicyVersion, bundle.NewPolicy.Version)
		}
		if claim.Epoch != bundle.Epoch {
			return nil, ErrAliasProofIncomplete
		}
		if len(claim.OldSubjectA) == 0 || len(claim.OldSubjectB) == 0 {
			return nil, ErrAliasProofIncomplete
		}
		if symbolsEqual(claim.OldSubjectA, claim.OldSubjectB) {
			return nil, ErrAliasProofIncomplete
		}
		if !claim.verify(bundle.RootKey) {
			return nil, ErrInvalidSignature
		}
		key := aliasKey(claim.OldSubjectA, claim.OldSubjectB)
		if serials[key] {
			return nil, fmt.Errorf("migrationadmission: duplicate alias claim")
		}
		serials[key] = true
		// Tenant, policy version and epoch are already checked above, and the
		// signature binds the exact claim payload to this root.
		aliases[key] = true
	}
	return aliases, nil
}

func verifyLateness(bundle Bundle) error {
	cert := bundle.Lateness
	if cert.TenantID != bundle.TenantID {
		return ErrAliasProofIncomplete
	}
	if cert.PolicyVersion != bundle.NewPolicy.Version {
		return ErrWrongLatenessBound
	}
	if cert.Epoch != bundle.Epoch {
		return ErrWrongLatenessBound
	}
	if cert.MaxBacklog < 0 || cert.MaxBacklog > MaxLateness {
		return ErrLatenessExceeded
	}
	if !cert.verify(bundle.RootKey) {
		return ErrInvalidSignature
	}
	return nil
}

// TenantID returns the tenant bound to the verified bundle.
func (v *VerifiedBundle) TenantID() string { return v.bundle.TenantID }

// Epoch returns the alias epoch; credentials from older epochs are stale.
func (v *VerifiedBundle) Epoch() uint64 { return v.bundle.Epoch }

// Admit transduces a legal raw name under the new policy and returns the
// login identifier. It rejects illegal names and configurations that cannot
// emit any identifier.
func (v *VerifiedBundle) Admit(name []byte) ([]Symbol, error) {
	identifier, err := v.newPolicy.transduce(name)
	if err != nil {
		return nil, err
	}
	if len(identifier) == 0 {
		return nil, ErrNoLoginIdentifier
	}
	return identifier, nil
}

// Credential is a root-signed, epoch-bound login credential.
type Credential struct {
	TenantID  string
	Epoch     uint64
	Name      []byte
	Subject   []Symbol
	Signature []byte
}

func encodeCredential(c Credential) []byte {
	e := &encoder{}
	e.str(c.TenantID)
	e.uvarint(c.Epoch)
	e.bytes(c.Name)
	e.symbols(c.Subject)
	return e.buf.Bytes()
}

// IssueCredential signs a login credential for an admitted name.
func (v *VerifiedBundle) IssueCredential(priv ed25519.PrivateKey, name []byte) (Credential, error) {
	subject, err := v.Admit(name)
	if err != nil {
		return Credential{}, err
	}
	cred := Credential{
		TenantID: v.bundle.TenantID,
		Epoch:    v.bundle.Epoch,
		Name:     append([]byte(nil), name...),
		Subject:  subject,
	}
	cred.Signature = ed25519.Sign(priv, signedMessage(domainCredential, encodeCredential(cred)))
	return cred, nil
}

// VerifyCredential checks a credential against the current verified bundle.
// After an alias update the epoch changes, so old credentials fail here.
func (v *VerifiedBundle) VerifyCredential(cred Credential) error {
	if cred.TenantID != v.bundle.TenantID || cred.Epoch != v.bundle.Epoch {
		return ErrCredentialExpired
	}
	if len(cred.Signature) != ed25519.SignatureSize {
		return ErrInvalidSignature
	}
	signature := cred.Signature
	cred.Signature = nil
	if !ed25519.Verify(v.bundle.RootKey, signedMessage(domainCredential, encodeCredential(cred)), signature) {
		return ErrInvalidSignature
	}
	subject, err := v.Admit(cred.Name)
	if err != nil {
		return err
	}
	if !symbolsEqual(subject, cred.Subject) {
		return ErrUnauthorizedFold
	}
	return nil
}
