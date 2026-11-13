package netsec

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
)

// RFC 8446, Section 4.4.3, defines the content signed by a TLS 1.3
// CertificateVerify message as:
//
//	64 octets of 0x20 (the ASCII space character)
//	the context string
//	one 0x00 octet
//	the transcript hash
//
// The 64-space prefix is part of the signed content. It must not be
// omitted, shortened, or normalised away.
const (
	// ContextPrefixLength is the number of 0x20 octets that precede the
	// context string in a TLS 1.3 CertificateVerify message.
	ContextPrefixLength = 64

	// ServerContextLabel is the context string for a CertificateVerify
	// message sent by a server.
	ServerContextLabel = "TLS 1.3, server CertificateVerify"

	// ClientContextLabel is the context string for a CertificateVerify
	// message sent by a client.
	ClientContextLabel = "TLS 1.3, client CertificateVerify"

	// Long-form label aliases.
	ServerCertificateVerifyLabel = ServerContextLabel
	ClientCertificateVerifyLabel = ClientContextLabel
)

// Four concatenated 16-byte runs keep the literal easy to audit.
const contextPrefix = "" +
	"                " + // 16
	"                " + // 32
	"                " + // 48
	"                " // 64

// ContextPrefix is the required 64-octet prefix (0x20 repeated 64 times).
const ContextPrefix = contextPrefix

// Full contexts, including the required 64-space prefix.
const (
	ServerContext = contextPrefix + ServerContextLabel
	ClientContext = contextPrefix + ClientContextLabel

	// Long-form aliases for callers that prefer explicit names.
	ServerCertificateVerifyContext = ServerContext
	ClientCertificateVerifyContext = ClientContext
)

// Role identifies the endpoint that produced a CertificateVerify message.
// It is an alias for string so callers can pass either a Role value or one
// of the string constants below.
type Role = string

const (
	RoleServer = "server"
	RoleClient = "client"

	// Short aliases for callers that prefer them.
	Server = RoleServer
	Client = RoleClient
)

var (
	// ErrInvalidRole reports an unknown CertificateVerify role.
	ErrInvalidRole = errors.New("netsec: invalid CertificateVerify role")

	// ErrInvalidContext reports a context that is not exactly the RFC 8446
	// context for a server or client CertificateVerify message.
	ErrInvalidContext = errors.New("netsec: invalid CertificateVerify context")

	// ErrInvalidSignedMessage reports a malformed CertificateVerify signed
	// message.
	ErrInvalidSignedMessage = errors.New("netsec: invalid CertificateVerify signed message")
)

// ParseRole normalises a role name. Both short names ("server" and "client")
// and the RFC 8446 context labels are accepted. Matching is case-insensitive
// and surrounding whitespace is ignored.
func ParseRole(role string) (Role, error) {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case RoleServer, "role_server", "server certificateverify",
		strings.ToLower(ServerContextLabel), strings.ToLower(strings.TrimSpace(ServerContext)):
		return RoleServer, nil
	case RoleClient, "role_client", "client certificateverify",
		strings.ToLower(ClientContextLabel), strings.ToLower(strings.TrimSpace(ClientContext)):
		return RoleClient, nil
	default:
		return "", fmt.Errorf("%w: %q", ErrInvalidRole, role)
	}
}

// ContextLabel returns the RFC 8446 context label for role, without the
// 64-space prefix.
func ContextLabel(role Role) (string, error) {
	normalized, err := ParseRole(role)
	if err != nil {
		return "", err
	}
	if normalized == RoleServer {
		return ServerContextLabel, nil
	}
	return ClientContextLabel, nil
}

// Context returns the complete RFC 8446 context for role, including the 64
// leading 0x20 octets.
func Context(role Role) ([]byte, error) {
	label, err := ContextLabel(role)
	if err != nil {
		return nil, err
	}
	return []byte(contextPrefix + label), nil
}

// CertificateVerifyContext is an alias for Context.
func CertificateVerifyContext(role Role) ([]byte, error) {
	return Context(role)
}

// ContextString is the string form of Context.
func ContextString(role Role) (string, error) {
	label, err := ContextLabel(role)
	if err != nil {
		return "", err
	}
	return contextPrefix + label, nil
}

// ValidateContext checks that context is exactly the RFC 8446 context for
// either a server or a client CertificateVerify message. In particular, it
// rejects a context that is missing even one of the 64 leading spaces.
func ValidateContext(context []byte) error {
	_, err := contextRole(context)
	return err
}

func contextRole(context []byte) (Role, error) {
	switch {
	case bytes.Equal(context, []byte(ServerContext)):
		return RoleServer, nil
	case bytes.Equal(context, []byte(ClientContext)):
		return RoleClient, nil
	case bytes.Equal(context, []byte(ServerContextLabel)):
		return "", fmt.Errorf("%w: missing %d-space prefix", ErrInvalidContext, ContextPrefixLength)
	case bytes.Equal(context, []byte(ClientContextLabel)):
		return "", fmt.Errorf("%w: missing %d-space prefix", ErrInvalidContext, ContextPrefixLength)
	default:
		spaces := leadingSpaces(context)
		if spaces != ContextPrefixLength {
			return "", fmt.Errorf("%w: got %d leading spaces, want %d", ErrInvalidContext, spaces, ContextPrefixLength)
		}
		return "", fmt.Errorf("%w: unknown context", ErrInvalidContext)
	}
}

func leadingSpaces(b []byte) int {
	n := 0
	for n < len(b) && b[n] == 0x20 {
		n++
	}
	return n
}

// SignedMessage builds the complete byte string covered by a TLS 1.3
// CertificateVerify signature:
//
//	64 spaces || context label || 0x00 || transcriptHash
func SignedMessage(role Role, transcriptHash []byte) ([]byte, error) {
	context, err := Context(role)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, len(context)+1+len(transcriptHash))
	out = append(out, context...)
	out = append(out, 0)
	out = append(out, transcriptHash...)
	return out, nil
}

// BuildSignedMessage is an alias for SignedMessage.
func BuildSignedMessage(role Role, transcriptHash []byte) ([]byte, error) {
	return SignedMessage(role, transcriptHash)
}

// CertificateVerifyInput is an alias for SignedMessage.
func CertificateVerifyInput(role Role, transcriptHash []byte) ([]byte, error) {
	return SignedMessage(role, transcriptHash)
}

// ServerSignedMessage builds the CertificateVerify input for a server.
func ServerSignedMessage(transcriptHash []byte) ([]byte, error) {
	return SignedMessage(RoleServer, transcriptHash)
}

// ClientSignedMessage builds the CertificateVerify input for a client.
func ClientSignedMessage(transcriptHash []byte) ([]byte, error) {
	return SignedMessage(RoleClient, transcriptHash)
}

// SplitSignedMessage validates the context portion of a complete TLS 1.3
// CertificateVerify signed message and returns the role and transcript hash.
// A message with a missing or altered 0x20 prefix is rejected.
func SplitSignedMessage(message []byte) (Role, []byte, error) {
	if len(message) < ContextPrefixLength {
		return "", nil, fmt.Errorf("%w: message is shorter than the %d-space prefix", ErrInvalidSignedMessage, ContextPrefixLength)
	}

	spaces := leadingSpaces(message)
	if spaces != ContextPrefixLength {
		return "", nil, fmt.Errorf("%w: got %d leading spaces, want %d", ErrInvalidSignedMessage, spaces, ContextPrefixLength)
	}

	rest := message[ContextPrefixLength:]
	var label string
	var role Role
	switch {
	case bytes.HasPrefix(rest, []byte(ServerContextLabel+"\x00")):
		label, role = ServerContextLabel, RoleServer
	case bytes.HasPrefix(rest, []byte(ClientContextLabel+"\x00")):
		label, role = ClientContextLabel, RoleClient
	default:
		return "", nil, fmt.Errorf("%w: missing server/client context label and separator", ErrInvalidSignedMessage)
	}

	transcript := rest[len(label)+1:]
	return role, transcript, nil
}

// ValidateSignedMessage checks that message is a complete TLS 1.3
// CertificateVerify signed message with an intact 64-space prefix.
func ValidateSignedMessage(message []byte) error {
	_, _, err := SplitSignedMessage(message)
	return err
}
