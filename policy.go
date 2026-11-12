package netsec

import (
	"fmt"
	"strings"
)

const securityPolicyBase = "http://opcfoundation.org/UA/SecurityPolicy#"

// CanonicalSecurityPolicyURI normalizes the common empty and short forms of
// OPC UA security policy URIs. Unknown URIs are returned unchanged.
func CanonicalSecurityPolicyURI(uri string) string {
	uri = strings.TrimSpace(uri)
	if uri == "" {
		return SecurityPolicyNone
	}

	name := uri
	if len(uri) >= len(securityPolicyBase) &&
		strings.EqualFold(uri[:len(securityPolicyBase)], securityPolicyBase) {
		name = uri[len(securityPolicyBase):]
	} else if strings.Contains(uri, "://") {
		return uri
	}

	if canonical, ok := canonicalSecurityPolicyName(name); ok {
		return canonical
	}
	return uri
}

func canonicalSecurityPolicyName(name string) (string, bool) {
	switch strings.ToLower(name) {
	case "none":
		return SecurityPolicyNone, true
	case "basic128rsa15":
		return SecurityPolicyBasic128Rsa15, true
	case "basic256":
		return SecurityPolicyBasic256, true
	case "basic256sha256":
		return SecurityPolicyBasic256Sha256, true
	case "aes128_sha256_rsaoaep":
		return SecurityPolicyAes128Sha256RsaOaep, true
	case "aes256_sha256_rsapss":
		return SecurityPolicyAes256Sha256RsaPss, true
	case "ecc_nistp256":
		return SecurityPolicyECCNistP256, true
	case "ecc_nistp384":
		return SecurityPolicyECCNistP384, true
	case "ecc_brainpoolp256r1":
		return SecurityPolicyECCBrainpoolP256R1, true
	case "ecc_brainpoolp384r1":
		return SecurityPolicyECCBrainpoolP384R1, true
	case "ecc_curve25519":
		return SecurityPolicyECCCurve25519, true
	case "ecc_curve448":
		return SecurityPolicyECCCurve448, true
	default:
		return "", false
	}
}

// EffectiveSecurityPolicyURI resolves a user token policy's security policy.
// An empty user-token-policy value inherits the endpoint/channel policy.
func (e Endpoint) EffectiveSecurityPolicyURI(policy UserTokenPolicy) string {
	if policy.SecurityPolicyURI != "" {
		return CanonicalSecurityPolicyURI(policy.SecurityPolicyURI)
	}
	return CanonicalSecurityPolicyURI(e.SecurityPolicyURI)
}

// LookupUserTokenPolicy finds exactly one declared policy matching policyID
// and tokenType. Conflicting duplicate PolicyIds are rejected rather than
// allowing an attacker to choose the weaker declaration.
func (e Endpoint) LookupUserTokenPolicy(policyID string, tokenType TokenType) (UserTokenPolicy, error) {
	if policyID == "" {
		return UserTokenPolicy{}, fmt.Errorf("%w: empty PolicyId", ErrPolicyNotFound)
	}

	var declared []UserTokenPolicy
	for _, policy := range e.UserTokenPolicies {
		if policy.PolicyID == policyID {
			declared = append(declared, policy)
		}
	}
	if len(declared) == 0 {
		return UserTokenPolicy{}, fmt.Errorf("%w: %q", ErrPolicyNotFound, policyID)
	}

	first := declared[0]
	firstPolicyURI := e.EffectiveSecurityPolicyURI(first)
	for _, policy := range declared[1:] {
		if policy.TokenType != first.TokenType ||
			e.EffectiveSecurityPolicyURI(policy) != firstPolicyURI {
			return UserTokenPolicy{}, fmt.Errorf("%w: PolicyId %q", ErrPolicyAmbiguous, policyID)
		}
	}

	if first.TokenType != tokenType {
		return UserTokenPolicy{}, fmt.Errorf(
			"%w: PolicyId %q declares %q, request uses %q",
			ErrTokenTypeMismatch, policyID, first.TokenType, tokenType,
		)
	}
	return first, nil
}

// ValidateUserNameToken applies the username-token admission rules:
//   - PolicyId must be declared for a UserName token.
//   - The token encryption algorithm must equal the policy's effective
//     security policy URI (empty policy values inherit the channel policy).
//   - Two None values are rejected because that is a plaintext password.
func ValidateUserNameToken(endpoint Endpoint, policyID, encryptionAlgorithm string) error {
	policy, err := endpoint.LookupUserTokenPolicy(policyID, TokenTypeUserName)
	if err != nil {
		return err
	}

	required := endpoint.EffectiveSecurityPolicyURI(policy)
	provided := CanonicalSecurityPolicyURI(encryptionAlgorithm)

	if provided == SecurityPolicyNone && required == SecurityPolicyNone {
		return fmt.Errorf("%w: PolicyId %q", ErrPlaintextPassword, policyID)
	}
	if provided != required {
		return fmt.Errorf(
			"%w: PolicyId %q requires %q, token uses %q",
			ErrEncryptionAlgorithmMismatch, policyID, required, provided,
		)
	}
	return nil
}
