package netsec

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"sort"
)

// EAT claim keys used by this package. The values are the CWT claim keys
// registered for the Entity Attestation Token (EAT).
const (
	ClaimNonce = 10
	ClaimUEID  = 256
	ClaimSUEID = 257
)

// DefaultUEIDLengths contains the fixed byte lengths defined for EAT UEIDs.
// The type byte is included in every length.
var DefaultUEIDLengths = []int{
	7,  // IEEE EUI-48
	8,  // IEEE EUI-60
	9,  // IEEE EUI-64
	15, // IMEI
	17, // IMEISV, UUID, or 128-bit RAND
	25, // 192-bit RAND
	33, // 256-bit RAND
}

var (
	// ErrUEIDRequired is returned when policy requires ueid but the token
	// does not carry one. A sueid is never accepted as a substitute.
	ErrUEIDRequired = errors.New("netsec: policy requires ueid")
	// ErrUEIDLength is returned when ueid has a length that policy does not
	// permit.
	ErrUEIDLength = errors.New("netsec: ueid length is not allowed")
	// ErrSUEIDRequired is returned when policy requires a sueid but none is
	// present.
	ErrSUEIDRequired = errors.New("netsec: policy requires sueid")
	// ErrSUEIDLength is returned when a supplied sueid has a length that
	// policy does not permit.
	ErrSUEIDLength = errors.New("netsec: sueid length is not allowed")
	// ErrNonceRequired is returned when the token does not carry a nonce.
	ErrNonceRequired = errors.New("netsec: nonce is required")
	// ErrNonceMismatch is returned when the token nonce does not equal the
	// verifier's challenge.
	ErrNonceMismatch = errors.New("netsec: nonce does not match challenge")
	// ErrChallengeRequired is returned when verification is attempted without
	// a challenge. Without a challenge, nonce freshness cannot be established.
	ErrChallengeRequired = errors.New("netsec: challenge is required")
	// ErrPolicyInvalid is returned when a policy itself is invalid.
	ErrPolicyInvalid = errors.New("netsec: invalid policy")
)

// Policy describes the claims a verification component requires.
//
// AllowedUEIDLengths and AllowedSUEIDLengths are byte lengths that include
// the EAT UEID/SUEID type byte. When an allowed-length list is nil, the
// package uses DefaultUEIDLengths. An explicitly empty list is invalid.
type Policy struct {
	RequireUEID  bool
	RequireSUEID bool

	AllowedUEIDLengths  []int
	AllowedSUEIDLengths []int
}

// Claims is the subset of EAT claims needed for policy verification.
//
// SUEID is provided for components that have a single unlabelled SUEID.
// SUEIDs carries the labels defined by EAT. Both may be populated; every
// supplied value is validated.
type Claims struct {
	UEID   []byte
	SUEID  []byte
	SUEIDs map[string][]byte
	Nonce  []byte
}

// Proof is an alias for Claims, for callers that refer to the attested claim
// set as a proof.
type Proof = Claims

// Verifier enforces a Policy. A Verifier is safe for concurrent use.
type Verifier struct {
	policy       Policy
	ueidLengths  map[int]struct{}
	sueidLengths map[int]struct{}
}

// NewVerifier validates policy and returns a verifier for it.
func NewVerifier(policy Policy) (*Verifier, error) {
	ueidLengths, err := normalizeLengths(policy.AllowedUEIDLengths)
	if err != nil {
		return nil, fmt.Errorf("%w: ueid lengths: %w", ErrPolicyInvalid, err)
	}
	sueidLengths, err := normalizeLengths(policy.AllowedSUEIDLengths)
	if err != nil {
		return nil, fmt.Errorf("%w: sueid lengths: %w", ErrPolicyInvalid, err)
	}

	return &Verifier{
		policy:       policy,
		ueidLengths:  ueidLengths,
		sueidLengths: sueidLengths,
	}, nil
}

// Verify checks claims against policy and the challenge supplied by the
// verifier. It returns nil only when the policy is satisfied, every supplied
// identifier has an allowed length, and the nonce exactly matches challenge.
func (v *Verifier) Verify(claims Claims, challenge []byte) error {
	if v == nil {
		return fmt.Errorf("%w: nil verifier", ErrPolicyInvalid)
	}
	return verify(v.policy, v.ueidLengths, v.sueidLengths, claims, challenge)
}

// Verify is a convenience wrapper around NewVerifier and Verifier.Verify.
func Verify(claims Claims, policy Policy, challenge []byte) error {
	verifier, err := NewVerifier(policy)
	if err != nil {
		return err
	}
	return verifier.Verify(claims, challenge)
}

// VerifyClaims is an alias for Verify.
func VerifyClaims(claims Claims, policy Policy, challenge []byte) error {
	return Verify(claims, policy, challenge)
}

// CheckUEID validates the presence and length of a ueid. When allowedLengths
// is nil, DefaultUEIDLengths is used. CheckUEID never consults a sueid.
func CheckUEID(ueid []byte, allowedLengths []int) error {
	lengths, err := normalizeLengths(allowedLengths)
	if err != nil {
		return fmt.Errorf("%w: ueid lengths: %w", ErrPolicyInvalid, err)
	}
	if len(ueid) == 0 {
		return ErrUEIDRequired
	}
	if _, ok := lengths[len(ueid)]; !ok {
		return fmt.Errorf("%w: got %d bytes, allowed %v", ErrUEIDLength, len(ueid), sortedLengths(lengths))
	}
	return nil
}

// CheckNonce requires nonce to be non-empty and byte-for-byte equal to
// challenge. The comparison runs in constant time.
func CheckNonce(nonce, challenge []byte) error {
	if len(challenge) == 0 {
		return ErrChallengeRequired
	}
	if len(nonce) == 0 {
		return ErrNonceRequired
	}
	if len(nonce) != len(challenge) ||
		subtle.ConstantTimeCompare(nonce, challenge) != 1 {
		return ErrNonceMismatch
	}
	return nil
}

func verify(
	policy Policy,
	ueidLengths map[int]struct{},
	sueidLengths map[int]struct{},
	claims Claims,
	challenge []byte,
) error {
	sueids := claims.allSUEIDs()
	if policy.RequireUEID && len(claims.UEID) == 0 {
		if len(sueids) != 0 {
			return fmt.Errorf("%w: sueid cannot substitute for ueid", ErrUEIDRequired)
		}
		return ErrUEIDRequired
	}
	if len(claims.UEID) != 0 {
		if _, ok := ueidLengths[len(claims.UEID)]; !ok {
			return fmt.Errorf(
				"%w: got %d bytes, allowed %v",
				ErrUEIDLength,
				len(claims.UEID),
				sortedLengths(ueidLengths),
			)
		}
	}

	if policy.RequireSUEID && len(sueids) == 0 {
		return ErrSUEIDRequired
	}
	for label, sueid := range sueids {
		if len(sueid) == 0 {
			return fmt.Errorf("%w: empty sueid %q", ErrSUEIDLength, label)
		}
		if _, ok := sueidLengths[len(sueid)]; !ok {
			return fmt.Errorf(
				"%w: sueid %q has %d bytes, allowed %v",
				ErrSUEIDLength,
				label,
				len(sueid),
				sortedLengths(sueidLengths),
			)
		}
	}

	if err := CheckNonce(claims.Nonce, challenge); err != nil {
		return err
	}

	return nil
}

func (c Claims) allSUEIDs() map[string][]byte {
	sueids := make(map[string][]byte, len(c.SUEIDs)+1)
	for label, value := range c.SUEIDs {
		sueids[label] = value
	}
	if len(c.SUEID) != 0 {
		sueids[""] = c.SUEID
	}
	return sueids
}

func normalizeLengths(configured []int) (map[int]struct{}, error) {
	lengths := configured
	if lengths == nil {
		lengths = DefaultUEIDLengths
	}
	if len(lengths) == 0 {
		return nil, errors.New("at least one allowed length is required")
	}

	set := make(map[int]struct{}, len(lengths))
	for _, length := range lengths {
		if length <= 0 {
			return nil, fmt.Errorf("length %d is not positive", length)
		}
		set[length] = struct{}{}
	}
	return set, nil
}

func sortedLengths(lengths map[int]struct{}) []int {
	result := make([]int, 0, len(lengths))
	for length := range lengths {
		result = append(result, length)
	}
	sort.Ints(result)
	return result
}
