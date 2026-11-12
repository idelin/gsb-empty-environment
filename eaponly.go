package netsec

import "fmt"

// NotifyType is an IKEv2 Notify payload type (RFC 7296 section 3.10).
type NotifyType uint16

// EAPType is an EAP method type carried in the Type field of an EAP packet
// (RFC 3748 section 5).
type EAPType uint8

const (
	// NotifyEAPOnlyAuthentication is the EAP_ONLY_AUTHENTICATION notify
	// defined in RFC 5998 section 2.3.
	NotifyEAPOnlyAuthentication NotifyType = 16417
)

const (
	// EAPTypeIdentity is EAP-Identity (RFC 3748 section 5.1). It is an
	// enrollment/type-selection packet, not an authenticating EAP method.
	EAPTypeIdentity EAPType = 1
	// EAPTypeNotification is EAP-Notification (RFC 3748 section 5.2).
	EAPTypeNotification EAPType = 2
	// EAPTypeNak is EAP-Nak (RFC 3748 section 5.3).
	EAPTypeNak EAPType = 3
	// EAPTypeMD5Challenge is EAP-MD5-Challenge (RFC 3748), which RFC 5998
	// section 4 forbids because it provides no mutual authentication.
	EAPTypeMD5Challenge EAPType = 4
	// EAPTypeGTC is EAP-GTC (RFC 3748 section 5.5). RFC 5998 section 4
	// forbids it in EAP-only mode: it is a password-only method that does
	// not authenticate the server.
	EAPTypeGTC EAPType = 6
	// EAPTypeTLS is EAP-TLS (RFC 5216), a certificate-based mutually
	// authenticating method allowed by RFC 5998 section 4.
	EAPTypeTLS EAPType = 13
	// EAPTypeMSCHAPv2 is EAP-MSCHAPv2 (RFC 2759). RFC 5998 section 4
	// forbids it in EAP-only mode because the method does not authenticate
	// the server, leaving an on-path attacker able to harvest credentials.
	EAPTypeMSCHAPv2 EAPType = 26
	// EAPTypeAKAPrime is EAP-AKA' (RFC 5448), a mutually authenticating
	// method allowed by RFC 5998 section 4.
	EAPTypeAKAPrime EAPType = 50
)

// Sentinel errors returned by the policy. Callers can match the specific
// failure with errors.Is, or treat every wrapped ErrServerAuthFailed as an
// unconditional authentication failure.
var (
	// ErrServerAuthFailed is the common ancestor of every policy failure:
	// the IKE_AUTH exchange must be aborted, never downgraded to a weaker
	// username/password check.
	ErrServerAuthFailed = fmt.Errorf("ikev2: server authentication failed")

	// ErrAuthPayloadMissing indicates that the IKE_AUTH response carried no
	// AUTH payload even though EAP-only authentication was not successfully
	// negotiated (RFC 5998 section 3).
	ErrAuthPayloadMissing = fmt.Errorf("%w: response has no AUTH payload outside EAP-only authentication", ErrServerAuthFailed)

	// ErrEAPOnlyAsymmetric indicates that the EAP_ONLY_AUTHENTICATION
	// negotiation was asymmetric: the responder may only omit its AUTH
	// payload when the initiator requested the notify and the responder
	// echoed it (RFC 5998 section 3).
	ErrEAPOnlyAsymmetric = fmt.Errorf("%w: EAP_ONLY_AUTHENTICATION notify not present in both request and response", ErrServerAuthFailed)

	// ErrNoEAPMethod indicates that EAP-only mode was negotiated but the
	// response did not carry a negotiable EAP method payload.
	ErrNoEAPMethod = fmt.Errorf("%w: EAP-only mode negotiated without an EAP payload", ErrServerAuthFailed)

	// ErrEAPMethodNotAllowed indicates that the negotiated EAP method is not
	// on the caller's allow list. RFC 5998 section 4 requires a method that
	// mutually authenticates the server; weak password-only methods must end
	// the exchange instead of being accepted.
	ErrEAPMethodNotAllowed = fmt.Errorf("%w: negotiated EAP method is not allowed", ErrServerAuthFailed)
)

// forbiddenEAPMethods can never be accepted in EAP-only mode per
// RFC 5998 section 4, regardless of caller configuration.
var forbiddenEAPMethods = map[EAPType]struct{}{
	EAPTypeGTC:          {},
	EAPTypeMSCHAPv2:     {},
	EAPTypeMD5Challenge: {},
}

// DefaultAllowedEAPMethods returns the EAP methods permitted by the default
// policy: EAP-TLS (13) and EAP-AKA' (50). EAP-GTC (6) and EAP-MSCHAPv2 (26)
// are intentionally absent and are additionally rejected outright.
func DefaultAllowedEAPMethods() []EAPType {
	return []EAPType{EAPTypeTLS, EAPTypeAKAPrime}
}

// ServerAuthPolicy decides whether the server side of an IKE_AUTH exchange is
// authenticated. It is safe for concurrent use: the configuration is fixed at
// construction time.
type ServerAuthPolicy struct {
	allowedEAPMethods map[EAPType]struct{}
}

// NewDefaultServerAuthPolicy builds a policy whose EAP allow list is
// DefaultAllowedEAPMethods.
func NewDefaultServerAuthPolicy() *ServerAuthPolicy {
	p, err := NewServerAuthPolicy(DefaultAllowedEAPMethods())
	if err != nil {
		panic(fmt.Sprintf("netsec: default policy is always valid: %v", err))
	}
	return p
}

// NewServerAuthPolicy builds a policy with a caller-supplied EAP method allow
// list. The list must be non-empty and must not contain an RFC 5998 section 4
// forbidden method (EAP-GTC 6, EAP-MSCHAPv2 26, EAP-MD5-Challenge 4).
func NewServerAuthPolicy(allowedMethods []EAPType) (*ServerAuthPolicy, error) {
	if len(allowedMethods) == 0 {
		return nil, fmt.Errorf("netsec: EAP method allow list must not be empty")
	}

	allowed := make(map[EAPType]struct{}, len(allowedMethods))
	for _, method := range allowedMethods {
		if _, banned := forbiddenEAPMethods[method]; banned {
			return nil, fmt.Errorf("netsec: EAP method %d is forbidden by RFC 5998 section 4", method)
		}
		allowed[method] = struct{}{}
	}

	return &ServerAuthPolicy{allowedEAPMethods: allowed}, nil
}

// EAPMethodAllowed reports whether method may be used in EAP-only mode under
// this policy.
func (p *ServerAuthPolicy) EAPMethodAllowed(method EAPType) bool {
	_, allowed := p.allowedEAPMethods[method]
	return allowed
}

// IKEAuthServerAuth is the subset of an IKE_AUTH response (plus the local
// request state) that the policy needs. The IKEv2 message parser populates it.
type IKEAuthServerAuth struct {
	// RequestHadEAPOnlyNotify records whether the IKE_AUTH request sent by
	// this initiator included EAP_ONLY_AUTHENTICATION (16417).
	RequestHadEAPOnlyNotify bool
	// ResponseHasEAPOnlyNotify records whether the IKE_AUTH response
	// included EAP_ONLY_AUTHENTICATION (16417).
	ResponseHasEAPOnlyNotify bool
	// ResponseHasAUTH records whether the IKE_AUTH response carried an AUTH
	// payload.
	ResponseHasAUTH bool
	// EAPMethod is the EAP method negotiated on this exchange. Zero means no
	// EAP method payload was present; EAP-Identity/Notification/Nak control
	// packets must not be reported as a negotiated method.
	EAPMethod EAPType
}

// VerifyServerAuth enforces the RFC 5998 EAP-only authentication rules for an
// IKE_AUTH response.
//
// A response carrying an AUTH payload is authenticated through the normal
// RFC 7296 path and is accepted here. A response without AUTH is accepted only
// when (1) the initiator requested EAP-only authentication with the
// EAP_ONLY_AUTHENTICATION notify (16417), (2) the responder echoed the same
// notify, and (3) the negotiated EAP method is on the policy allow list.
//
// Every other outcome returns an error wrapping ErrServerAuthFailed. The
// caller must tear down the IKE SA on error; there is deliberately no fallback
// to username/password-only verification.
func (p *ServerAuthPolicy) VerifyServerAuth(auth IKEAuthServerAuth) error {
	if auth.ResponseHasAUTH {
		return nil
	}

	// RFC 5998 section 3: without an AUTH payload the responder is relying on
	// EAP-only authentication, which is valid only when the notify was sent in
	// both the request and the response.
	if !auth.RequestHadEAPOnlyNotify && !auth.ResponseHasEAPOnlyNotify {
		return ErrAuthPayloadMissing
	}
	if !auth.RequestHadEAPOnlyNotify {
		return fmt.Errorf("%w: initiator did not request EAP-only mode", ErrEAPOnlyAsymmetric)
	}
	if !auth.ResponseHasEAPOnlyNotify {
		return fmt.Errorf("%w: responder did not echo the notify", ErrEAPOnlyAsymmetric)
	}

	if auth.EAPMethod == 0 {
		return fmt.Errorf("%w: no EAP method negotiated", ErrNoEAPMethod)
	}

	if _, banned := forbiddenEAPMethods[auth.EAPMethod]; banned {
		return fmt.Errorf("reject EAP method %d: %w", auth.EAPMethod, ErrEAPMethodNotAllowed)
	}

	if !p.EAPMethodAllowed(auth.EAPMethod) {
		return fmt.Errorf("reject EAP method %d: %w", auth.EAPMethod, ErrEAPMethodNotAllowed)
	}

	return nil
}
