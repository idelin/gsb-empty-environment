package ctap2auth

// credProtect policy levels (FIDO CTAP §12.1 "credProtect Extension").
const (
	CredProtectUserVerificationOptional = 1
	CredProtectUserVerificationWithList = 2
	CredProtectUserVerificationRequired = 3
	defaultCredProtect                  = CredProtectUserVerificationOptional
)

// CredentialInfo describes a stored resident/discoverable credential as far as
// policy enforcement is concerned.
type CredentialInfo struct {
	CredentialID []byte
	RPID         string
	CredProtect  int
}

// AssertionRequest is the policy-relevant subset of an authenticatorGetAssertion
// request.
type AssertionRequest struct {
	RPID         string
	AllowList    [][]byte
	UserVerified bool
}

// hasAllowList reports whether the request supplies a non-empty allowList.
func (r *AssertionRequest) hasAllowList() bool {
	for _, id := range r.AllowList {
		if len(id) > 0 {
			return true
		}
	}
	return false
}

// ResolveCredProtect returns the effective policy for a makeCredential,
// applying the authenticator default (level 1) when the extension is absent and
// clamping to the authenticator minimum.
func ResolveCredProtect(requested, authenticatorMinimum int) int {
	if requested < CredProtectUserVerificationOptional ||
		requested > CredProtectUserVerificationRequired {
		requested = defaultCredProtect
	}
	if authenticatorMinimum > requested {
		return authenticatorMinimum
	}
	return requested
}

// AssertionAuthorized applies the credProtect rules to a single credential:
//
// level 1 is returned whenever the RP matches; level 2 requires a valid
// allowList entry or a verified user; level 3 always requires a verified user
// regardless of the allowList.
//
// Credentials belonging to a different RP ID are never authorized.
func AssertionAuthorized(cred CredentialInfo, req AssertionRequest) bool {
	if cred.RPID != req.RPID {
		return false
	}
	switch cred.CredProtect {
	case CredProtectUserVerificationRequired:
		return req.UserVerified
	case CredProtectUserVerificationWithList:
		return req.UserVerified || req.hasAllowList()
	default:
		return true
	}
}

// FilterDiscoverableCredentials returns only the credentials the caller is
// authorized to observe. Unauthorized credentials are dropped entirely, so no
// metadata (ID, RP, user) leaks when UV is missing. This is the getAssertion
// enumeration guard for resident keys.
func FilterDiscoverableCredentials(creds []CredentialInfo, req AssertionRequest) []CredentialInfo {
	allowed := make([]CredentialInfo, 0, len(creds))
	for _, cred := range creds {
		if AssertionAuthorized(cred, req) {
			allowed = append(allowed, cred)
		}
	}
	return allowed
}

// EnsureAssertionAccess enforces the policy before assembling an assertion and
// returns ErrOperationDenied (CTAP2_ERR_OPERATION_DENIED) if the credential may
// not be used. Prefer this when resolving one specific credential.
func EnsureAssertionAccess(cred CredentialInfo, req AssertionRequest) error {
	if !AssertionAuthorized(cred, req) {
		return ErrOperationDenied
	}
	return nil
}
