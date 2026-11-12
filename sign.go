package netsec

import (
	"bytes"
	"fmt"
)

// Exported protocol names for callers that build or inspect agent messages.
const (
	SessionBindExtension         = sessionBindExtension
	RestrictDestinationExtension = restrictDestinationExtension
	PublicKeyHostBoundMethod     = publicKeyHostBoundMethod
	PublicKeyMethod              = publicKeyMethod
	SSHConnectionService         = sshConnectionService
)

// SignRequest is a decoded SSH_AGENTC_SIGN_REQUEST message.
type SignRequest struct {
	KeyBlob []byte
	Data    []byte
	Flags   uint32
}

// ParseSignRequest strictly decodes an SSH agent sign request. The request
// must contain exactly: type, key blob, data, and flags. Accepting a
// length-prefixed agent packet as well as a bare request body is convenient
// for callers that read packets at the socket layer.
func ParseSignRequest(raw []byte) (*SignRequest, error) {
	body, err := stripAgentFrame(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformedMessage, err)
	}
	r := newWireReader(body)
	typ, err := r.readByte()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformedMessage, err)
	}
	if typ != AgentcSignRequest {
		return nil, fmt.Errorf("%w: message type %d is not SSH_AGENTC_SIGN_REQUEST", ErrMalformedMessage, typ)
	}
	keyBlob, err := r.readString()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformedMessage, err)
	}
	data, err := r.readString()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformedMessage, err)
	}
	flags, err := r.readUint32()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformedMessage, err)
	}
	if err := r.expectEnd(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformedMessage, err)
	}
	if len(keyBlob) == 0 {
		return nil, fmt.Errorf("%w: empty key blob", ErrMalformedMessage)
	}
	if err := ValidatePublicKeyBlob(keyBlob); err != nil {
		return nil, fmt.Errorf("%w: invalid key blob: %v", ErrMalformedMessage, err)
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("%w: empty signature data", ErrMalformedMessage)
	}
	return &SignRequest{
		KeyBlob: copyBytes(keyBlob),
		Data:    copyBytes(data),
		Flags:   flags,
	}, nil
}

// UserAuthData is the parsed, host-bound public-key authentication payload
// that an agent is asked to sign. OpenSSH's signed data begins with the SSH
// session identifier followed by SSH2_MSG_USERAUTH_REQUEST. The ordinary
// publickey method omits ServerHostKey; the host-bound method includes it.
type UserAuthData struct {
	SessionID          []byte
	MessageType        byte
	Username           string
	Service            string
	Method             string
	HasSignature       bool
	PublicKeyAlgorithm string
	PublicKeyBlob      []byte
	ServerHostKey      []byte
}

// ParseSignatureData parses the data field of SSH_AGENTC_SIGN_REQUEST. The
// entire value has SSH binary-string framing; textual splitting is never used.
// Only actual user-authentication requests are accepted, so arbitrary
// challenge bytes cannot be passed off as an authentication signature.
func ParseSignatureData(data []byte) (*UserAuthData, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("%w: empty signature data", ErrMalformedMessage)
	}
	r := newWireReader(data)
	sessionID, err := r.readString()
	if err != nil {
		return nil, fmt.Errorf("%w: session identifier: %v", ErrMalformedMessage, err)
	}
	if len(sessionID) == 0 {
		return nil, fmt.Errorf("%w: missing session identifier", ErrMissingSessionBinding)
	}
	auth, err := parseUserAuthFields(r, sessionID)
	if err != nil {
		return nil, err
	}
	return auth, nil
}

// ParseUserAuthRequest parses an SSH2_MSG_USERAUTH_REQUEST without the
// preceding session-identifier string. It is useful for validating the
// structure of the request portion; destination-constrained signing uses
// ParseSignatureData so that the session identifier is mandatory.
func ParseUserAuthRequest(data []byte) (*UserAuthData, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("%w: empty userauth request", ErrMalformedMessage)
	}
	r := newWireReader(data)
	auth, err := parseUserAuthFields(r, nil)
	if err != nil {
		return nil, err
	}
	return auth, nil
}

func parseUserAuthFields(r *wireReader, sessionID []byte) (*UserAuthData, error) {
	msgType, err := r.readByte()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformedMessage, err)
	}
	if msgType != SSH2MsgUserAuthRequest {
		return nil, fmt.Errorf("%w: message type %d is not SSH2_MSG_USERAUTH_REQUEST", ErrNotUserAuthRequest, msgType)
	}
	username, err := r.readStringValue()
	if err != nil {
		return nil, fmt.Errorf("%w: username: %v", ErrMalformedMessage, err)
	}
	if username == "" {
		return nil, fmt.Errorf("%w: empty username", ErrNotUserAuthRequest)
	}
	service, err := r.readStringValue()
	if err != nil {
		return nil, fmt.Errorf("%w: service: %v", ErrMalformedMessage, err)
	}
	if service != sshConnectionService {
		return nil, fmt.Errorf("%w: service %q", ErrNotUserAuthRequest, service)
	}
	method, err := r.readStringValue()
	if err != nil {
		return nil, fmt.Errorf("%w: method: %v", ErrMalformedMessage, err)
	}
	switch method {
	case publicKeyMethod, publicKeyHostBoundMethod:
	default:
		return nil, fmt.Errorf("%w: method %q", ErrUnsupportedAuthMethod, method)
	}
	hasSignature, err := r.readBool()
	if err != nil {
		return nil, fmt.Errorf("%w: has-signature: %v", ErrMalformedMessage, err)
	}
	if !hasSignature {
		return nil, fmt.Errorf("%w: request is a public-key query, not a signature request", ErrNotUserAuthRequest)
	}
	algorithm, err := r.readStringValue()
	if err != nil {
		return nil, fmt.Errorf("%w: public key algorithm: %v", ErrMalformedMessage, err)
	}
	if algorithm == "" {
		return nil, fmt.Errorf("%w: empty public key algorithm", ErrNotUserAuthRequest)
	}
	publicKey, err := r.readString()
	if err != nil {
		return nil, fmt.Errorf("%w: public key: %v", ErrMalformedMessage, err)
	}
	if len(publicKey) == 0 {
		return nil, fmt.Errorf("%w: empty public key", ErrNotUserAuthRequest)
	}
	keyType, _, err := parseSSHKeyType(publicKey)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid public key: %v", ErrMalformedMessage, err)
	}
	if algorithm != keyType {
		return nil, fmt.Errorf("%w: public key algorithm %q does not match key blob type %q",
			ErrMalformedMessage, algorithm, keyType)
	}

	auth := &UserAuthData{
		SessionID:          copyBytes(sessionID),
		MessageType:        msgType,
		Username:           username,
		Service:            service,
		Method:             method,
		HasSignature:       hasSignature,
		PublicKeyAlgorithm: algorithm,
		PublicKeyBlob:      copyBytes(publicKey),
	}
	if method == publicKeyHostBoundMethod {
		hostKey, err := r.readString()
		if err != nil {
			return nil, fmt.Errorf("%w: server host key: %v", ErrMalformedMessage, err)
		}
		if len(hostKey) == 0 {
			return nil, fmt.Errorf("%w: host-bound request has no server host key", ErrDestinationMismatch)
		}
		if err := ValidatePublicKeyBlob(hostKey); err != nil {
			return nil, fmt.Errorf("%w: invalid server host key: %v", ErrMalformedMessage, err)
		}
		auth.ServerHostKey = copyBytes(hostKey)
	}
	if err := r.expectEnd(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformedMessage, err)
	}
	return auth, nil
}

// SignCallback performs the actual private-key operation. It is invoked only
// after the request key, complete signed forwarding path, session binding,
// username, and final destination have all been authorized.
type SignCallback func(keyBlob, data []byte, flags uint32) ([]byte, error)

// Agent is a destination-constrained SSH agent host.
type Agent struct {
	Keys *KeyRegistry
}

// ProxyHost is an alias for Agent.
type ProxyHost = Agent

func NewAgent() *Agent {
	return &Agent{Keys: NewKeyRegistry()}
}

// NewHost is an alias for NewAgent.
func NewHost() *Agent {
	return NewAgent()
}

// NewProxyHost is an alias for NewAgent.
func NewProxyHost() *Agent {
	return NewAgent()
}

// NewAgentHost is an alias for NewAgent.
func NewAgentHost() *Agent {
	return NewAgent()
}

// AuthorizeSign validates a decoded sign request against the session's
// forwarding chain. It returns the parsed authentication payload on success.
func (a *Agent) AuthorizeSign(sess *Session, req *SignRequest) (*UserAuthData, error) {
	if a == nil || a.Keys == nil {
		return nil, fmt.Errorf("%w: agent has no key registry", ErrMalformedMessage)
	}
	if req == nil {
		return nil, fmt.Errorf("%w: nil sign request", ErrMalformedMessage)
	}
	if err := ValidatePublicKeyBlob(req.KeyBlob); err != nil {
		return nil, fmt.Errorf("%w: invalid key blob: %v", ErrMalformedMessage, err)
	}

	entry, err := a.Keys.lookup(req.KeyBlob)
	if err != nil {
		return nil, err
	}
	auth, err := ParseSignatureData(req.Data)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(auth.PublicKeyBlob, req.KeyBlob) {
		return nil, fmt.Errorf("%w: signed userauth request names a different public key", ErrMalformedMessage)
	}
	if sess == nil {
		return nil, fmt.Errorf("%w: no session", ErrMissingSessionBinding)
	}
	bindings := sess.Bindings()
	if len(bindings) == 0 {
		return nil, fmt.Errorf("%w: no signed hops recorded", ErrMissingSessionBinding)
	}

	observed := Chain{}
	for i, b := range bindings {
		if len(b.SessionID) == 0 {
			return nil, fmt.Errorf("%w: hop %d has no session identifier", ErrMissingSessionBinding, i)
		}
		observed = append(observed, Hop{
			Hostname: b.Hostname,
			Username: b.Username,
			HostKey:  b.HostKey,
		})
	}
	if err := observed.Validate(); err != nil {
		return nil, err
	}

	last := bindings[len(bindings)-1]
	if !bytes.Equal(auth.SessionID, last.SessionID) {
		return nil, fmt.Errorf("%w: signed session identifier does not match the final hop", ErrSessionMismatch)
	}

	switch auth.Method {
	case publicKeyHostBoundMethod:
		if !bytes.Equal(auth.ServerHostKey, last.HostKey) {
			return nil, fmt.Errorf("%w: host key in signed request does not match the final hop", ErrDestinationMismatch)
		}
	case publicKeyMethod:
		// A plain publickey request carries no final host identity. It can
		// only be accepted for a genuinely direct, single-hop binding.
		if len(bindings) != 1 {
			return nil, fmt.Errorf("%w: forwarded connection requires host-bound publickey authentication", ErrSessionMismatch)
		}
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedAuthMethod, auth.Method)
	}

	if last.Username != "" && last.Username != auth.Username {
		return nil, fmt.Errorf("%w: bound username %q does not match signed username %q",
			ErrDestinationMismatch, last.Username, auth.Username)
	}

	_, ok, err := matchAllowed(observed, auth.Username, entry.chains)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("%w", ErrDestinationMismatch)
	}
	return auth, nil
}

// HandleSignRequest parses a raw agent message, authorizes it, and only then
// invokes sign. Any parse, binding, duplicate-node, session, username, or
// destination failure returns without calling sign.
func (a *Agent) HandleSignRequest(sess *Session, raw []byte, sign SignCallback) ([]byte, error) {
	req, err := ParseSignRequest(raw)
	if err != nil {
		return nil, err
	}
	if _, err := a.AuthorizeSign(sess, req); err != nil {
		return nil, err
	}
	if sign == nil {
		return nil, ErrNoSigner
	}
	return sign(copyBytes(req.KeyBlob), copyBytes(req.Data), req.Flags)
}

// Sign is an alias for HandleSignRequest.
func (a *Agent) Sign(sess *Session, raw []byte, sign SignCallback) ([]byte, error) {
	return a.HandleSignRequest(sess, raw, sign)
}

// RegisterKey registers a key and its allowed chains on the agent's registry.
func (a *Agent) RegisterKey(keyBlob []byte, chains ...Chain) error {
	if a == nil {
		return fmt.Errorf("%w: nil agent", ErrMalformedMessage)
	}
	if a.Keys == nil {
		a.Keys = NewKeyRegistry()
	}
	return a.Keys.RegisterKey(keyBlob, chains...)
}

// Register is an alias for RegisterKey.
func (a *Agent) Register(keyBlob []byte, chains ...Chain) error {
	return a.RegisterKey(keyBlob, chains...)
}

// ValidateSignRequest parses and authorizes a raw sign request without invoking
// a signing callback.
func (a *Agent) ValidateSignRequest(sess *Session, raw []byte) (*SignRequest, *UserAuthData, error) {
	req, err := ParseSignRequest(raw)
	if err != nil {
		return nil, nil, err
	}
	auth, err := a.AuthorizeSign(sess, req)
	if err != nil {
		return nil, nil, err
	}
	return req, auth, nil
}
