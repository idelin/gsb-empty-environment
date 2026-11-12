package netsec

import (
	"crypto/hmac"
	"crypto/md5"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"strings"
)

// ValidationOptions controls AUTHENTICATE validation. The SessionBaseKey
// is the key derived by the server during the NTLM exchange; it must not
// be confused with the raw NTOWF or the client's session key.
type ValidationOptions struct {
	SessionBaseKey []byte
	// SessionKey is accepted as an alias for SessionBaseKey.
	SessionKey []byte
	// ServiceSPN is the SPN registered for this service, for example
	// "HTTP/server.example.com". Comparison is case-insensitive.
	ServiceSPN string
	// TargetSPN is accepted as an alias for ServiceSPN.
	TargetSPN string
	// ServiceSPNs allows a service to accept more than one registered
	// SPN (for example an alias). Every target name present in the
	// handshake must match one of the configured values.
	ServiceSPNs []string
	// RequireMIC forces a MIC even when the challenge does not advertise
	// MsvAvFlags.MIC_PRESENT or MsvAvTimestamp.
	RequireMIC bool
	// AllowMissingSPN permits handshakes with no target name. It is
	// false by default because a missing target name cannot be bound to
	// this service.
	AllowMissingSPN bool
}

// AuthenticateValidation is an alias for ValidationOptions.
type AuthenticateValidation = ValidationOptions

// AuthResult contains the parsed and verified handshake. It is returned
// to access layers that need the authenticated user's identity.
type AuthResult struct {
	Negotiate    *NegotiateMessage
	Challenge    *ChallengeMessage
	Authenticate *AuthenticateMessage
	UserName     string
	DomainName   string
	Workstation  string
	MICPresent   bool
	MICVerified  bool
	MICRequired  bool
	TargetSPNs   []string
}

// ValidateAuthenticate validates a complete NTLM handshake:
//
//   - The NEGOTIATE, CHALLENGE and AUTHENTICATE messages are parsed.
//   - If MsvAvFlags advertises MIC_PRESENT or MsvAvTimestamp exists, a
//     MIC is mandatory.
//   - A present MIC must equal HMAC-MD5(SessionBaseKey,
//     NEGOTIATE || CHALLENGE || AUTHENTICATE), with the 16 MIC bytes in
//     the AUTHENTICATE message replaced by zeroes.
//   - Every MsvAvTargetName must match the configured service SPN.
//
// Raw message bytes are used for the HMAC; they are never re-serialized.
// The function is independent of the transport and can be called by
// HTTP or SMB access layers.
func ValidateAuthenticate(negotiate, challenge, authenticate, sessionBaseKey []byte, serviceSPN string) error {
	negotiate, challenge, authenticate, sessionBaseKey, err := separateHandshakeKey(negotiate, challenge, authenticate, sessionBaseKey)
	if err != nil {
		return err
	}
	_, err = ValidateAuthenticateDetailed(negotiate, challenge, authenticate, ValidationOptions{
		SessionBaseKey: sessionBaseKey,
		ServiceSPN:     serviceSPN,
	})
	return err
}

// Validate is an alias for ValidateAuthenticate.
func Validate(negotiate, challenge, authenticate, sessionBaseKey []byte, serviceSPN string) error {
	return ValidateAuthenticate(negotiate, challenge, authenticate, sessionBaseKey, serviceSPN)
}

// ValidateAuthenticateWithOptions is the configurable form of
// ValidateAuthenticate.
func ValidateAuthenticateWithOptions(negotiate, challenge, authenticate []byte, options ValidationOptions) error {
	_, err := ValidateAuthenticateDetailed(negotiate, challenge, authenticate, options)
	return err
}

// ValidateAuthenticateMessage is an alias for ValidateAuthenticate.
func ValidateAuthenticateMessage(negotiate, challenge, authenticate, sessionBaseKey []byte, serviceSPN string) error {
	return ValidateAuthenticate(negotiate, challenge, authenticate, sessionBaseKey, serviceSPN)
}

// ValidateAuthMessage is an alias for ValidateAuthenticate.
func ValidateAuthMessage(negotiate, challenge, authenticate, sessionBaseKey []byte, serviceSPN string) error {
	return ValidateAuthenticate(negotiate, challenge, authenticate, sessionBaseKey, serviceSPN)
}

// VerifyAuthenticate is an alias for ValidateAuthenticate.
func VerifyAuthenticate(negotiate, challenge, authenticate, sessionBaseKey []byte, serviceSPN string) error {
	return ValidateAuthenticate(negotiate, challenge, authenticate, sessionBaseKey, serviceSPN)
}

// ValidateHandshake validates a complete NTLM handshake.
func ValidateHandshake(negotiate, challenge, authenticate, sessionBaseKey []byte, serviceSPN string) error {
	return ValidateAuthenticate(negotiate, challenge, authenticate, sessionBaseKey, serviceSPN)
}

// ValidateAuthenticateDetailed performs validation and returns the parsed
// handshake on success.
func ValidateAuthenticateDetailed(negotiate, challenge, authenticate []byte, options ValidationOptions) (*AuthResult, error) {
	sessionKey := options.SessionBaseKey
	if len(sessionKey) == 0 {
		sessionKey = options.SessionKey
	}
	if len(sessionKey) == 0 {
		return nil, validationError(ErrInvalidConfig, "SessionBaseKey is empty")
	}

	var err error
	negotiate, challenge, authenticate, err = orderHandshakeMessages(negotiate, challenge, authenticate)
	if err != nil {
		return nil, err
	}

	neg, err := ParseNegotiateMessage(negotiate)
	if err != nil {
		return nil, err
	}
	chal, err := ParseChallengeMessage(challenge)
	if err != nil {
		return nil, err
	}
	auth, err := ParseAuthenticateMessage(authenticate)
	if err != nil {
		return nil, err
	}

	result := &AuthResult{
		Negotiate:    neg,
		Challenge:    chal,
		Authenticate: auth,
		UserName:     auth.User(),
		DomainName:   auth.Domain(),
		Workstation:  auth.WorkstationName(),
		MICPresent:   auth.MICPresent,
	}

	micRequired, err := micRequiredByTargetInfo(chal.TargetInfo, auth.NTLMv2TargetInfo)
	if err != nil {
		return nil, err
	}
	result.MICRequired = options.RequireMIC || micRequired

	if result.MICRequired && !auth.MICPresent {
		return nil, validationError(ErrMICMissing, "challenge advertises MsvAvFlags.MIC_PRESENT or MsvAvTimestamp")
	}
	if auth.MICPresent {
		verified := false
		for _, offset := range auth.micOffsets {
			expected, err := ComputeMIC(negotiate, challenge, authenticate, offset, sessionKey)
			if err != nil {
				continue
			}
			if subtle.ConstantTimeCompare(authenticate[offset:offset+16], expected) == 1 {
				verified = true
				break
			}
		}
		if !verified {
			return nil, validationError(ErrMICMismatch, "computed HMAC-MD5 does not match the AUTHENTICATE MIC")
		}
		result.MICVerified = true
	}

	expectedSPNs := configuredSPNs(options)
	names, err := targetSPNs(chal, auth)
	if err != nil {
		return nil, err
	}
	result.TargetSPNs = names

	if len(names) == 0 {
		if options.AllowMissingSPN {
			return result, nil
		}
		return nil, validationError(ErrSPNMissing, "no MsvAvTargetName found in CHALLENGE or AUTHENTICATE")
	}
	if len(expectedSPNs) == 0 {
		return nil, validationError(ErrInvalidConfig, "service SPN is empty")
	}
	for _, name := range names {
		if !spnMatches(name, expectedSPNs) {
			return nil, validationError(ErrSPNMismatch, "target SPN %q does not match this service", name)
		}
	}

	return result, nil
}

func orderHandshakeMessages(first, second, third []byte) ([]byte, []byte, []byte, error) {
	var negotiate, challenge, authenticate []byte
	for _, raw := range [][]byte{first, second, third} {
		if len(raw) < 12 {
			return nil, nil, nil, validationError(ErrMalformedMessage, "message is only %d bytes", len(raw))
		}
		if string(raw[:8]) != ntlmSignature {
			return nil, nil, nil, validationError(ErrMalformedMessage, "missing NTLMSSP signature")
		}
		switch binary.LittleEndian.Uint32(raw[8:12]) {
		case MessageTypeNegotiate:
			if negotiate != nil {
				return nil, nil, nil, validationError(ErrMalformedMessage, "duplicate NEGOTIATE message")
			}
			negotiate = raw
		case MessageTypeChallenge:
			if challenge != nil {
				return nil, nil, nil, validationError(ErrMalformedMessage, "duplicate CHALLENGE message")
			}
			challenge = raw
		case MessageTypeAuthenticate:
			if authenticate != nil {
				return nil, nil, nil, validationError(ErrMalformedMessage, "duplicate AUTHENTICATE message")
			}
			authenticate = raw
		default:
			return nil, nil, nil, validationError(ErrUnsupportedMessage, "unknown NTLM message type %d", binary.LittleEndian.Uint32(raw[8:12]))
		}
	}
	if negotiate == nil || challenge == nil || authenticate == nil {
		return nil, nil, nil, validationError(ErrMalformedMessage, "handshake is missing NEGOTIATE, CHALLENGE, or AUTHENTICATE")
	}
	return negotiate, challenge, authenticate, nil
}

func separateHandshakeKey(first, second, third, fourth []byte) ([]byte, []byte, []byte, []byte, error) {
	var messages [][]byte
	var key []byte
	for _, raw := range [][]byte{first, second, third, fourth} {
		if len(raw) >= 12 && string(raw[:8]) == ntlmSignature {
			messages = append(messages, raw)
			continue
		}
		if key != nil {
			return nil, nil, nil, nil, validationError(ErrMalformedMessage, "more than one non-message value in handshake")
		}
		key = raw
	}
	if len(messages) != 3 {
		return nil, nil, nil, nil, validationError(ErrMalformedMessage, "handshake has %d NTLM messages, want 3", len(messages))
	}
	negotiate, challenge, authenticate, err := orderHandshakeMessages(messages[0], messages[1], messages[2])
	if err != nil {
		return nil, nil, nil, nil, err
	}
	if len(key) == 0 {
		return nil, nil, nil, nil, validationError(ErrInvalidConfig, "SessionBaseKey is empty")
	}
	return negotiate, challenge, authenticate, key, nil
}

// ComputeMIC computes the AUTHENTICATE MIC over the exact message bytes.
// micOffset is the offset of the 16-byte MIC field in authenticate.
func ComputeMIC(negotiate, challenge, authenticate []byte, micOffset int, sessionBaseKey []byte) ([]byte, error) {
	if micOffset < 0 || micOffset+16 > len(authenticate) {
		return nil, validationError(ErrMICMissing, "MIC field at offset %d is outside AUTHENTICATE", micOffset)
	}
	if len(sessionBaseKey) == 0 {
		return nil, validationError(ErrInvalidConfig, "SessionBaseKey is empty")
	}

	tampered := append([]byte(nil), authenticate...)
	for i := micOffset; i < micOffset+16; i++ {
		tampered[i] = 0
	}

	hash := hmac.New(md5.New, sessionBaseKey)
	hash.Write(negotiate)
	hash.Write(challenge)
	hash.Write(tampered)
	return hash.Sum(nil), nil
}

// VerifyMIC verifies a MIC supplied separately from the message. It is
// useful to access layers that already parsed the AUTHENTICATE fields.
func VerifyMIC(negotiate, challenge, authenticate, mic, sessionBaseKey []byte, micOffset int) error {
	expected, err := ComputeMIC(negotiate, challenge, authenticate, micOffset, sessionBaseKey)
	if err != nil {
		return err
	}
	if len(mic) != md5.Size || subtle.ConstantTimeCompare(mic, expected) != 1 {
		return validationError(ErrMICMismatch, "computed HMAC-MD5 does not match the AUTHENTICATE MIC")
	}
	return nil
}

func micRequiredByTargetInfo(infos ...AVList) (bool, error) {
	required := false
	for _, info := range infos {
		flags, err := info.MsvAvFlags()
		if err != nil {
			return false, err
		}
		if flags&MsvAvFlagMICPresent != 0 {
			required = true
		}
		for _, value := range info.Values(MsvAvIDTimestamp) {
			if len(value) != 8 {
				return false, validationError(ErrMalformedMessage, "MsvAvTimestamp has length %d, want 8", len(value))
			}
			required = true
		}
	}
	return required, nil
}

func configuredSPNs(options ValidationOptions) []string {
	var spns []string
	add := func(spn string) {
		spn = strings.TrimSpace(trimNUL(spn))
		if spn == "" {
			return
		}
		for _, existing := range spns {
			if strings.EqualFold(existing, spn) {
				return
			}
		}
		spns = append(spns, spn)
	}
	add(options.ServiceSPN)
	add(options.TargetSPN)
	for _, spn := range options.ServiceSPNs {
		add(spn)
	}
	return spns
}

func targetSPNs(chal *ChallengeMessage, auth *AuthenticateMessage) ([]string, error) {
	var names []string
	add := func(name string) {
		for _, existing := range names {
			if strings.EqualFold(existing, name) {
				return
			}
		}
		names = append(names, name)
	}
	for _, info := range []AVList{chal.TargetInfo, auth.NTLMv2TargetInfo} {
		infoNames, err := info.TargetNames()
		if err != nil {
			return nil, err
		}
		for _, name := range infoNames {
			add(strings.TrimSpace(trimNUL(name)))
		}
	}
	if len(names) == 0 {
		// Older/simplified challenge messages put the target name in
		// the TargetName security buffer rather than in AV_PAIRs.
		if name := strings.TrimSpace(trimNUL(chal.TargetName)); name != "" {
			add(name)
		}
	}
	return names, nil
}

func spnMatches(name string, expected []string) bool {
	name = strings.TrimSpace(trimNUL(name))
	if name == "" {
		return false
	}
	for _, want := range expected {
		if strings.EqualFold(name, want) {
			return true
		}
	}
	return false
}

// Authenticator is a reusable validator for an HTTP or SMB access layer.
// It is safe to reuse across concurrent requests; its fields should be
// treated as immutable after construction.
type Authenticator struct {
	SessionBaseKey  []byte
	SessionKey      []byte
	ServiceSPN      string
	TargetSPN       string
	ServiceSPNs     []string
	RequireMIC      bool
	AllowMissingSPN bool
}

// NewAuthenticator constructs an Authenticator for one service SPN.
func NewAuthenticator(sessionBaseKey []byte, serviceSPN string) *Authenticator {
	return &Authenticator{
		SessionBaseKey: append([]byte(nil), sessionBaseKey...),
		ServiceSPN:     serviceSPN,
	}
}

// Validate checks a complete handshake and returns only an error.
func (a *Authenticator) Validate(negotiate, challenge, authenticate []byte) error {
	_, err := a.ValidateHandshake(negotiate, challenge, authenticate)
	return err
}

// ValidateHandshake checks a complete handshake and returns identity
// information on success.
func (a *Authenticator) ValidateHandshake(negotiate, challenge, authenticate []byte) (*AuthResult, error) {
	if a == nil {
		return nil, validationError(ErrInvalidConfig, "nil Authenticator")
	}
	sessionKey := a.SessionBaseKey
	if len(sessionKey) == 0 {
		sessionKey = a.SessionKey
	}
	serviceSPN := a.ServiceSPN
	if serviceSPN == "" {
		serviceSPN = a.TargetSPN
	}
	return ValidateAuthenticateDetailed(negotiate, challenge, authenticate, ValidationOptions{
		SessionBaseKey:  sessionKey,
		ServiceSPN:      serviceSPN,
		ServiceSPNs:     a.ServiceSPNs,
		RequireMIC:      a.RequireMIC,
		AllowMissingSPN: a.AllowMissingSPN,
	})
}

// Validator is a descriptive alias for Authenticator.
type Validator = Authenticator

// NewValidator constructs a reusable validator.
func NewValidator(sessionBaseKey []byte, serviceSPN string) *Validator {
	return NewAuthenticator(sessionBaseKey, serviceSPN)
}

// Handshake groups the three raw NTLM messages and the server key.
type Handshake struct {
	Negotiate      []byte
	Challenge      []byte
	Authenticate   []byte
	SessionBaseKey []byte
	ServiceSPN     string
	RequireMIC     bool
}

// Validate validates the grouped handshake.
func (h Handshake) Validate() error {
	return ValidateAuthenticateWithOptions(h.Negotiate, h.Challenge, h.Authenticate, ValidationOptions{
		SessionBaseKey: h.SessionBaseKey,
		ServiceSPN:     h.ServiceSPN,
		RequireMIC:     h.RequireMIC,
	})
}

// DecodeNTLMHeader decodes an HTTP Authorization value such as
// "NTLM TlRMTVNTUAAD..." into raw NTLM message bytes. It also accepts a
// bare base64 value. A non-NTLM scheme is rejected.
func DecodeNTLMHeader(value string) ([]byte, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, validationError(ErrMalformedMessage, "empty authorization header")
	}
	fields := strings.Fields(value)
	if len(fields) == 2 {
		if !strings.EqualFold(fields[0], "NTLM") && !strings.EqualFold(fields[0], "Negotiate") {
			return nil, validationError(ErrUnsupportedMessage, "authorization scheme %q is not NTLM", fields[0])
		}
		value = fields[1]
	} else if len(fields) != 1 {
		return nil, validationError(ErrMalformedMessage, "malformed authorization header")
	}
	raw, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return nil, validationError(ErrMalformedMessage, "invalid base64: %v", err)
	}
	if len(raw) < 12 || string(raw[:8]) != ntlmSignature {
		return nil, validationError(ErrMalformedMessage, "decoded header is not an NTLM message")
	}
	return raw, nil
}

// ValidateAuthorizationHeader validates a Type 3 from an HTTP
// Authorization header against the previously sent Type 1 and Type 2
// messages.
func ValidateAuthorizationHeader(header string, negotiate, challenge, sessionBaseKey []byte, serviceSPN string) error {
	authenticate, err := DecodeNTLMHeader(header)
	if err != nil {
		return err
	}
	return ValidateAuthenticate(negotiate, challenge, authenticate, sessionBaseKey, serviceSPN)
}

// AVFlagsString formats MsvAvFlags for diagnostics.
func AVFlagsString(flags uint32) string {
	var parts []string
	if flags&MsvAvFlagAccountConstrained != 0 {
		parts = append(parts, "ACCOUNT_CONSTRAINED")
	}
	if flags&MsvAvFlagMICPresent != 0 {
		parts = append(parts, "MIC_PRESENT")
	}
	if flags&MsvAvFlagSPNUNTrusted != 0 {
		parts = append(parts, "SPN_UNTRUSTED")
	}
	if len(parts) == 0 {
		return "0"
	}
	return strings.Join(parts, "|")
}

// TimestampValue returns the first MsvAvTimestamp as little-endian
// FILETIME bytes.
func (l AVList) TimestampValue() ([]byte, bool) {
	value, ok := l.First(MsvAvIDTimestamp)
	return value, ok
}

// FlagsValue returns the combined MsvAvFlags value.
func (l AVList) FlagsValue() (uint32, error) {
	return l.MsvAvFlags()
}

// TimestampString is a diagnostic helper that does not attempt to
// interpret the FILETIME epoch.
func TimestampString(value []byte) string {
	if len(value) != 8 {
		return fmt.Sprintf("invalid(%d bytes)", len(value))
	}
	return fmt.Sprintf("0x%016x", binary.LittleEndian.Uint64(value))
}
