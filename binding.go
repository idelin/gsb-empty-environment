package netsec

import (
	"context"
	"fmt"
	"strings"
)

// Policy controls which hash algorithms are acceptable and whether realm
// measurements must be present. Digest lengths are always checked when a
// measurement is present.
type Policy struct {
	AllowedHashAlgorithms         []HashAlgorithm
	RequireInitialMeasurement     bool
	RequireExtensibleMeasurements bool
	RequirePlatformHashAlgorithm  bool
}

// DefaultPolicy permits SHA-256, SHA-384, and SHA-512. Measurement claims are
// validated when present but are not mandatory, so the package can also be
// used as a focused binding check after a separate appraisal step.
func DefaultPolicy() Policy {
	return Policy{
		AllowedHashAlgorithms: append([]HashAlgorithm(nil), DefaultAllowedHashAlgorithms...),
	}
}

// Option mutates a Policy. Options are accepted anywhere in the argument list
// of VerifyBinding.
type Option func(*Policy) error

// WithAllowedAlgorithms replaces the allow-list.
func WithAllowedAlgorithms(algorithms ...HashAlgorithm) Option {
	return func(p *Policy) error {
		p.AllowedHashAlgorithms = append([]HashAlgorithm(nil), algorithms...)
		return nil
	}
}

// WithAllowedAlgorithmNames replaces the allow-list using claim spellings.
func WithAllowedAlgorithmNames(names ...string) Option {
	return func(p *Policy) error {
		algorithms, err := parseHashAlgorithmList(names)
		if err != nil {
			return err
		}
		p.AllowedHashAlgorithms = algorithms
		return nil
	}
}

// RequireMeasurements makes the realm initial measurement and extensible
// measurements mandatory in addition to their length checks.
func RequireMeasurements() Option {
	return func(p *Policy) error {
		p.RequireInitialMeasurement = true
		p.RequireExtensibleMeasurements = true
		return nil
	}
}

type bindingInput struct {
	realm      *claimSet
	platform   *claimSet
	candidates []*claimSet
	nonce      [][]byte
	policy     Policy
	haveNonce  bool
}

// VerifyBinding checks that the realm and platform COSE_Sign1 claim sets are
// bound to each other and to the caller's nonce. Signatures MUST have been
// verified before this function is called; this package checks claim binding,
// not signatures.
//
// Accepted arguments are flexible: a realm token, a platform token, and a
// caller nonce in any order. Tokens may be raw COSE_Sign1 bytes, the CCA
// collection map containing both tokens, decoded claim maps, Claims values, or
// claim-shaped structs. A Policy, Option, HashAlgorithm, []HashAlgorithm,
// []string, or map[string]bool may be supplied to configure the allow-list.
func VerifyBinding(args ...any) error {
	input, err := parseBindingArgs(args)
	if err != nil {
		return err
	}
	return verifyBindingClaims(input)
}

// CheckBinding is an alias for VerifyBinding.
func CheckBinding(args ...any) error { return VerifyBinding(args...) }

// VerifyTokenBinding is an alias for VerifyBinding.
func VerifyTokenBinding(args ...any) error { return VerifyBinding(args...) }

// CheckTokenBinding is an alias for VerifyBinding.
func CheckTokenBinding(args ...any) error { return VerifyBinding(args...) }

// ValidateBinding is an alias for VerifyBinding.
func ValidateBinding(args ...any) error { return VerifyBinding(args...) }

// ValidateTokenBinding is an alias for VerifyBinding.
func ValidateTokenBinding(args ...any) error { return VerifyBinding(args...) }

// VerifyCCABinding is an alias for VerifyBinding.
func VerifyCCABinding(args ...any) error { return VerifyBinding(args...) }

// Verify is the common three-argument form: realm token, platform token,
// caller nonce, followed by optional policy/allow-list arguments.
func Verify(realm, platform, nonce any, options ...any) error {
	args := []any{realm, platform, nonce}
	args = append(args, options...)
	return VerifyBinding(args...)
}

// Check is the common three-argument form of VerifyBinding.
func Check(realm, platform, nonce any, options ...any) error {
	return Verify(realm, platform, nonce, options...)
}

// Validate is the common three-argument form of VerifyBinding.
func Validate(realm, platform, nonce any, options ...any) error {
	return Verify(realm, platform, nonce, options...)
}

func parseBindingArgs(args []any) (*bindingInput, error) {
	input := &bindingInput{policy: DefaultPolicy()}

	for _, arg := range args {
		if arg == nil {
			continue
		}
		if _, ok := arg.(context.Context); ok {
			continue
		}

		if values, ok := arg.([]any); ok && allAlgorithmNames(values) {
			for _, value := range values {
				alg, _ := ParseHashAlgorithm(value)
				input.policy.AllowedHashAlgorithms = appendUniqueAlgorithm(
					input.policy.AllowedHashAlgorithms, alg)
			}
			continue
		}

		switch x := arg.(type) {
		case Policy:
			input.policy = copyPolicy(x)
			continue
		case *Policy:
			if x == nil {
				continue
			}
			input.policy = copyPolicy(*x)
			continue
		case Option:
			if err := x(&input.policy); err != nil {
				return nil, err
			}
			continue
		case []Option:
			for _, option := range x {
				if err := option(&input.policy); err != nil {
					return nil, err
				}
			}
			continue
		case HashAlgorithm:
			input.policy.AllowedHashAlgorithms = []HashAlgorithm{x}
			continue
		case []HashAlgorithm:
			input.policy.AllowedHashAlgorithms = append([]HashAlgorithm(nil), x...)
			continue
		case []string:
			algorithms, err := parseHashAlgorithmList(x)
			if err != nil {
				return nil, err
			}
			input.policy.AllowedHashAlgorithms = algorithms
			continue
		case map[string]bool:
			algorithms, err := algorithmsFromBoolMap(x)
			if err != nil {
				return nil, err
			}
			input.policy.AllowedHashAlgorithms = algorithms
			continue
		}

		if handled, err := maybeSingleAlgorithmString(arg, input); handled || err != nil {
			if err != nil {
				return nil, err
			}
			continue
		}

		if b, ok := arg.([]byte); ok {
			realm, platform, claims, err := decodeEvidenceBytes(b, 0)
			if err == nil && evidenceLooksRelevant(realm, platform, claims) {
				if err := addDecodedEvidence(input, realm, platform, claims); err != nil {
					return nil, err
				}
				continue
			}
			if err := addNonce(input, b); err != nil {
				return nil, err
			}
			continue
		}

		if s, ok := arg.(string); ok {
			realm, platform, claims, err := decodeEvidenceString(s, 0)
			if err == nil && evidenceLooksRelevant(realm, platform, claims) {
				if err := addDecodedEvidence(input, realm, platform, claims); err != nil {
					return nil, err
				}
				continue
			}
			if err := addNonce(input, s); err != nil {
				return nil, err
			}
			continue
		}

		realm, platform, claims, err := decodeEvidenceValue(arg)
		if err != nil {
			return nil, err
		}
		if err := addDecodedEvidence(input, realm, platform, claims); err != nil {
			return nil, err
		}
	}

	if !input.haveNonce {
		return nil, fmt.Errorf("%w: caller nonce is missing", ErrInvalidArgument)
	}
	if err := classifyBindingInput(input); err != nil {
		return nil, err
	}
	return input, nil
}

func allAlgorithmNames(values []any) bool {
	if len(values) == 0 {
		return false
	}
	for _, value := range values {
		if _, err := ParseHashAlgorithm(value); err != nil {
			return false
		}
	}
	return true
}

func maybeSingleAlgorithmString(arg any, input *bindingInput) (bool, error) {
	s, ok := arg.(string)
	if !ok {
		return false, nil
	}
	if _, err := ParseHashAlgorithm(s); err != nil {
		return false, nil
	}
	// A known algorithm name is much more likely to be an allow-list option
	// than a nonce. This keeps the four-argument convenience form working.
	input.policy.AllowedHashAlgorithms = []HashAlgorithm{}
	a, _ := ParseHashAlgorithm(s)
	input.policy.AllowedHashAlgorithms = []HashAlgorithm{a}
	return true, nil
}

func addDecodedEvidence(input *bindingInput, realm, platform, claims *claimSet) error {
	if realm != nil {
		if input.realm != nil {
			return fmt.Errorf("%w: multiple realm tokens", ErrInvalidArgument)
		}
		input.realm = realm
	}
	if platform != nil {
		if input.platform != nil {
			return fmt.Errorf("%w: multiple platform tokens", ErrInvalidArgument)
		}
		input.platform = platform
	}
	if claims != nil {
		input.candidates = append(input.candidates, claims)
	}
	return nil
}

func addNonce(input *bindingInput, nonce any) error {
	if input.haveNonce {
		return fmt.Errorf("%w: multiple caller nonces", ErrInvalidArgument)
	}
	candidates, err := octetCandidates(nonce)
	if err != nil {
		return fmt.Errorf("%w: invalid caller nonce: %v", ErrInvalidArgument, err)
	}
	if len(candidates) == 0 {
		return fmt.Errorf("%w: empty caller nonce", ErrInvalidArgument)
	}
	empty := true
	for _, candidate := range candidates {
		if len(candidate) != 0 {
			empty = false
			break
		}
	}
	if empty {
		return fmt.Errorf("%w: empty caller nonce", ErrInvalidArgument)
	}
	input.nonce = candidates
	input.haveNonce = true
	return nil
}

func evidenceLooksRelevant(realm, platform, claims *claimSet) bool {
	if realm != nil || platform != nil {
		return true
	}
	if claims == nil {
		return false
	}
	return claims.has(claimNonce) || claims.realmScore() > 0 || claims.platformScore() > 0
}

func classifyBindingInput(input *bindingInput) error {
	var realmCandidates, platformCandidates, ambiguous []*claimSet
	for _, cs := range input.candidates {
		rs, ps := cs.realmScore(), cs.platformScore()
		switch {
		case rs > 0 && ps == 0:
			realmCandidates = append(realmCandidates, cs)
		case ps > 0 && rs == 0:
			platformCandidates = append(platformCandidates, cs)
		case rs > ps:
			realmCandidates = append(realmCandidates, cs)
		case ps > rs:
			platformCandidates = append(platformCandidates, cs)
		default:
			ambiguous = append(ambiguous, cs)
		}
	}

	if input.realm == nil {
		if len(realmCandidates) > 1 {
			return fmt.Errorf("%w: multiple realm token candidates", ErrInvalidArgument)
		}
		if len(realmCandidates) == 1 {
			input.realm = realmCandidates[0]
		} else if len(ambiguous) > 0 {
			input.realm = ambiguous[0]
			ambiguous = ambiguous[1:]
		}
	}
	if input.platform == nil {
		if len(platformCandidates) > 1 {
			return fmt.Errorf("%w: multiple platform token candidates", ErrInvalidArgument)
		}
		if len(platformCandidates) == 1 {
			input.platform = platformCandidates[0]
		} else if len(ambiguous) > 0 {
			input.platform = ambiguous[0]
			ambiguous = ambiguous[1:]
		}
	}

	if len(ambiguous) != 0 {
		return fmt.Errorf("%w: could not assign all tokens to realm/platform roles", ErrInvalidArgument)
	}
	if input.realm == nil {
		return fmt.Errorf("%w: realm token is missing", ErrInvalidArgument)
	}
	if input.platform == nil {
		return fmt.Errorf("%w: platform token is missing", ErrInvalidArgument)
	}
	return nil
}

func verifyBindingClaims(input *bindingInput) error {
	policy, allowed, err := normalisePolicy(input.policy)
	if err != nil {
		return err
	}

	nonceValue, ok := input.realm.get(claimNonce)
	if !ok {
		return fmt.Errorf("%w: realm token has no challenge/nonce claim", ErrMissingClaim)
	}
	realmNonce, err := octetCandidates(nonceValue)
	if err != nil {
		return fmt.Errorf("%w: realm challenge: %v", ErrInvalidClaim, err)
	}
	if !anyOctetsEqual(realmNonce, input.nonce...) {
		return fmt.Errorf("%w: realm challenge does not equal caller nonce", ErrChallengeMismatch)
	}

	platformNonceValue, ok := input.platform.get(claimNonce)
	if !ok {
		return fmt.Errorf("%w: platform token has no challenge/nonce claim", ErrMissingClaim)
	}
	platformNonce, err := octetCandidates(platformNonceValue)
	if err != nil {
		return fmt.Errorf("%w: platform challenge: %v", ErrInvalidClaim, err)
	}

	publicKeyValue, ok := input.realm.realmPublicKeyValue()
	if !ok {
		return fmt.Errorf("%w: realm token has no realm public key claim", ErrMissingClaim)
	}
	publicKey, err := octetCandidates(publicKeyValue)
	if err != nil {
		return fmt.Errorf("%w: realm public key: %v", ErrInvalidClaim, err)
	}
	hasPublicKey := false
	for _, candidate := range publicKey {
		if len(candidate) != 0 {
			hasPublicKey = true
			break
		}
	}
	if !hasPublicKey {
		return fmt.Errorf("%w: realm public key is empty", ErrInvalidClaim)
	}

	measurementAlg, publicKeyHashAlg, err := bindingAlgorithms(input.realm, allowed)
	if err != nil {
		return err
	}
	if err := validateRealmMeasurements(input.realm, measurementAlg, policy); err != nil {
		return err
	}
	if err := validatePlatformHashAlgorithm(input.platform, allowed, policy); err != nil {
		return err
	}
	if err := validatePlatformMeasurements(input.platform, allowed); err != nil {
		return err
	}

	matched := false
	for _, pk := range publicKey {
		if len(pk) == 0 {
			continue
		}
		keyHash := hashBytes(publicKeyHashAlg, pk)
		if anyOctetsEqual(platformNonce, keyHash) {
			matched = true
			break
		}
	}
	if !matched {
		return fmt.Errorf("%w: platform challenge does not equal hash(%s) of the realm public key",
			ErrChallengeMismatch, publicKeyHashAlg)
	}

	return nil
}

func bindingAlgorithms(realm *claimSet, allowed map[HashAlgorithm]bool) (HashAlgorithm, HashAlgorithm, error) {
	measurementValue, measurementPresent := realm.realmHashAlgorithmValue()
	publicKeyValue, publicKeyPresent := realm.publicKeyHashAlgorithmValue()
	if !measurementPresent && !publicKeyPresent {
		// If the caller supplied a policy with exactly one permitted
		// algorithm, it is safe to use that algorithm when the token omits
		// the optional algorithm claim.
		if len(allowed) == 1 {
			for alg := range allowed {
				return alg, alg, nil
			}
		}
		return "", "", fmt.Errorf("%w: realm token has no hash algorithm claim", ErrMissingClaim)
	}

	var measurementAlg, publicKeyAlg HashAlgorithm
	var err error
	if measurementPresent {
		measurementAlg, err = ParseHashAlgorithm(measurementValue)
		if err != nil {
			return "", "", fmt.Errorf("%w: realm measurement hash algorithm: %v", ErrAlgorithmNotAllowed, err)
		}
	}
	if publicKeyPresent {
		publicKeyAlg, err = ParseHashAlgorithm(publicKeyValue)
		if err != nil {
			return "", "", fmt.Errorf("%w: realm public key hash algorithm: %v", ErrAlgorithmNotAllowed, err)
		}
	}
	if !measurementPresent {
		measurementAlg = publicKeyAlg
	}
	if !publicKeyPresent {
		publicKeyAlg = measurementAlg
	}

	for _, alg := range []HashAlgorithm{measurementAlg, publicKeyAlg} {
		if !allowed[alg] {
			return "", "", fmt.Errorf("%w: %s", ErrAlgorithmNotAllowed, alg)
		}
	}
	return measurementAlg, publicKeyAlg, nil
}

func validateRealmMeasurements(realm *claimSet, alg HashAlgorithm, policy Policy) error {
	if value, ok := realm.initialMeasurementValue(); ok {
		if err := checkMeasurementLength(value, alg, "realm initial measurement"); err != nil {
			return err
		}
	} else if policy.RequireInitialMeasurement {
		return fmt.Errorf("%w: realm initial measurement", ErrMissingClaim)
	}

	if value, ok := realm.extensibleMeasurementsValue(); ok {
		values, err := measurementValues(value)
		if err != nil {
			return fmt.Errorf("%w: realm extensible measurements: %v", ErrInvalidClaim, err)
		}
		for i, measurement := range values {
			if err := checkMeasurementLength(measurement, alg, fmt.Sprintf("realm extensible measurement %d", i)); err != nil {
				return err
			}
		}
	} else if policy.RequireExtensibleMeasurements {
		return fmt.Errorf("%w: realm extensible measurements", ErrMissingClaim)
	}
	return nil
}

func checkMeasurementLength(value any, alg HashAlgorithm, name string) error {
	candidates, err := octetCandidates(value)
	if err != nil {
		return fmt.Errorf("%w: %s: %v", ErrInvalidClaim, name, err)
	}
	for _, candidate := range candidates {
		if len(candidate) == alg.DigestSize() {
			return nil
		}
	}
	return fmt.Errorf("%w: %s has length not equal to %d bytes for %s",
		ErrMeasurementLength, name, alg.DigestSize(), alg)
}

func validatePlatformHashAlgorithm(platform *claimSet, allowed map[HashAlgorithm]bool, policy Policy) error {
	value, ok := platform.platformHashAlgorithmValue()
	if !ok {
		if policy.RequirePlatformHashAlgorithm {
			return fmt.Errorf("%w: platform token has no hash algorithm claim", ErrMissingClaim)
		}
		return nil
	}
	alg, err := ParseHashAlgorithm(value)
	if err != nil {
		return fmt.Errorf("%w: platform hash algorithm: %v", ErrAlgorithmNotAllowed, err)
	}
	if !allowed[alg] {
		return fmt.Errorf("%w: %s", ErrAlgorithmNotAllowed, alg)
	}
	return nil
}

func validatePlatformMeasurements(platform *claimSet, allowed map[HashAlgorithm]bool) error {
	value, ok := platform.platformHashAlgorithmValue()
	if !ok {
		return nil
	}
	alg, err := ParseHashAlgorithm(value)
	if err != nil || !allowed[alg] {
		return nil // the dedicated algorithm check reports this first
	}
	componentsValue, ok := platform.get("cca-platform-sw-components", "cca-platform-software-components")
	if !ok {
		return nil
	}
	components, err := measurementValues(componentsValue)
	if err != nil {
		return fmt.Errorf("%w: platform software components: %v", ErrInvalidClaim, err)
	}
	for i, component := range components {
		componentMap, ok := component.(map[any]any)
		if !ok {
			if reflectionMap, err := claimsFromAny(component); err == nil {
				componentMap = reflectionMap
			} else {
				continue
			}
		}
		measurement, ok := claimMapValue(componentMap, "measurement-value", "measurement_value",
			"measurementvalue", uint64(2), int64(2), "2")
		if !ok {
			continue
		}
		if err := checkMeasurementLength(measurement, alg, fmt.Sprintf("platform software component %d measurement", i)); err != nil {
			return err
		}
	}
	return nil
}

func claimMapValue(m map[any]any, names ...any) (any, bool) {
	for _, key := range names {
		if value, ok := m[key]; ok {
			return value, true
		}
	}
	for k, v := range m {
		if s, ok := k.(string); ok {
			lower := strings.ToLower(strings.TrimSpace(s))
			for _, name := range names {
				if n, ok := name.(string); ok && lower == n {
					return v, true
				}
			}
		}
	}
	return nil, false
}

func normalisePolicy(p Policy) (Policy, map[HashAlgorithm]bool, error) {
	if p.AllowedHashAlgorithms == nil {
		p.AllowedHashAlgorithms = append([]HashAlgorithm(nil), DefaultAllowedHashAlgorithms...)
	}
	allowed := make(map[HashAlgorithm]bool, len(p.AllowedHashAlgorithms))
	for _, alg := range p.AllowedHashAlgorithms {
		normalised, err := ParseHashAlgorithm(alg)
		if err != nil {
			return Policy{}, nil, fmt.Errorf("%w: policy allow-list: %v", ErrAlgorithmNotAllowed, err)
		}
		allowed[normalised] = true
	}
	return p, allowed, nil
}

func copyPolicy(p Policy) Policy {
	p.AllowedHashAlgorithms = append([]HashAlgorithm(nil), p.AllowedHashAlgorithms...)
	return p
}

func algorithmsFromBoolMap(m map[string]bool) ([]HashAlgorithm, error) {
	out := []HashAlgorithm{}
	for name, enabled := range m {
		if !enabled {
			continue
		}
		alg, err := ParseHashAlgorithm(name)
		if err != nil {
			return nil, err
		}
		out = appendUniqueAlgorithm(out, alg)
	}
	return out, nil
}
