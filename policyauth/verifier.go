package policyauth

import (
	"context"
	"crypto/ecdsa"
	"crypto/subtle"
	"errors"
	"fmt"
)

// ObjectPolicy is the policy fixed for one sealed object.
type ObjectPolicy struct {
	// AuthorizedKeyName is the TPM name of the only key allowed to sign
	// authorizations for this object.
	AuthorizedKeyName Name
	// ApprovedPolicy is the PCR policy digest (re)computed from the
	// currently measured firmware/OS: the only approvedPolicy the object
	// accepts. After a system upgrade changes the PCRs, a new digest is
	// pinned here together with a freshly signed token.
	ApprovedPolicy Digest
	// PolicyRef is the device/object binding every presented token must
	// carry.
	PolicyRef PolicyRef
}

var (
	// ErrUnauthorizedKey is returned when the signer's public-key name does
	// not equal the authorized key name fixed in the object policy.
	ErrUnauthorizedKey = errors.New("policyauth: signer public key name does not match the authorized key name")
	// ErrPolicyRefMismatch is returned when the token policyRef does not
	// bind the expected device and object.
	ErrPolicyRefMismatch = errors.New("policyauth: token policyRef does not bind this device and object")
	// ErrPolicyMismatch is returned when the signed approvedPolicy is not
	// the PCR policy digest the object policy currently approves.
	ErrPolicyMismatch = errors.New("policyauth: approved policy does not match the object policy")
	// ErrStaleGeneration is returned when the token generation is below the
	// persisted monotonic counter.
	ErrStaleGeneration = errors.New("policyauth: token generation is below the NV counter")
	// ErrBadSignature is returned when the ECDSA signature does not verify.
	ErrBadSignature = errors.New("policyauth: signature verification failed")
)

// Verifier runs the pre-unseal authorization check against a fixed object
// policy and a monotonic counter store.
type Verifier struct {
	policy  ObjectPolicy
	counter CounterStore
}

// NewVerifier validates the object policy and binds it to a counter store.
func NewVerifier(policy ObjectPolicy, counter CounterStore) (*Verifier, error) {
	if counter == nil {
		return nil, errors.New("policyauth: nil counter store")
	}
	if _, err := policy.PolicyRef.MarshalBinary(); err != nil {
		return nil, fmt.Errorf("policyauth: invalid object policy ref: %w", err)
	}
	var zeroName Name
	if subtle.ConstantTimeCompare(policy.AuthorizedKeyName[:], zeroName[:]) == 1 {
		return nil, errors.New("policyauth: object policy missing authorized key name")
	}
	return &Verifier{policy: policy, counter: counter}, nil
}

// Verify checks a token without advancing the counter. The checks are:
//  1. signer public-key name equals the object policy's authorized key name;
//  2. token policyRef equals the object policy's device/object binding;
//  3. token approvedPolicy equals the object policy's approved PCR digest;
//  4. ECDSA signature verifies over approvedPolicy||policyRef||generation;
//  5. token generation is at or above the persisted NV counter value.
func (v *Verifier) Verify(ctx context.Context, token *Token, signer *ecdsa.PublicKey) error {
	if token == nil {
		return errors.New("policyauth: nil token")
	}
	if signer == nil {
		return errors.New("policyauth: nil signer public key")
	}

	// (1) Key pinning: a cryptographically valid signature from any other
	// key is still rejected.
	name, err := PublicKeyName(signer)
	if err != nil {
		return err
	}
	if subtle.ConstantTimeCompare(name[:], v.policy.AuthorizedKeyName[:]) != 1 {
		return ErrUnauthorizedKey
	}

	// (2) Device/object binding: a token signed for another device or
	// object cannot be replayed against this one.
	if !token.PolicyRef.Equal(v.policy.PolicyRef) {
		return ErrPolicyRefMismatch
	}

	// (3) The signed digest must be the current PCR policy. A signature
	// over the old vulnerable image's PCR digest is rejected here.
	if subtle.ConstantTimeCompare(token.ApprovedPolicy[:], v.policy.ApprovedPolicy[:]) != 1 {
		return ErrPolicyMismatch
	}

	// (4) Authenticate every token field, including the generation.
	if err := verifySignature(token, signer); err != nil {
		return err
	}

	// (5) Monotonic algebra: forbid replayed generations.
	current, err := v.counter.Current(ctx)
	if err != nil {
		return fmt.Errorf("policyauth: read NV counter: %w", err)
	}
	if token.Generation < current {
		return ErrStaleGeneration
	}
	return nil
}

// UnsealFunc releases the sealed key once the authorization check has passed.
type UnsealFunc func(context.Context) error

// Authorize verifies the token and, only when verification succeeds, invokes
// unseal. The NV counter advances only after unseal returns nil; a failed
// release leaves the counter untouched so the same token generation can be
// retried.
func (v *Verifier) Authorize(ctx context.Context, token *Token, signer *ecdsa.PublicKey, unseal UnsealFunc) error {
	if unseal == nil {
		return errors.New("policyauth: nil unseal callback")
	}
	if err := v.Verify(ctx, token, signer); err != nil {
		return err
	}
	if err := unseal(ctx); err != nil {
		return fmt.Errorf("policyauth: unseal failed, NV counter not advanced: %w", err)
	}
	if _, err := v.counter.Advance(ctx, token.Generation); err != nil {
		return fmt.Errorf("policyauth: advance NV counter: %w", err)
	}
	return nil
}
