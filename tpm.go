package netsec

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
)

// Handle is a TPM handle as it appears on the wire.
type Handle uint32

// CommandCode is a TPM_CC value.
type CommandCode uint32

// Algorithm is a TPM_ALG_ID value.
type Algorithm uint16

const (
	AlgSHA1   Algorithm = 0x0004
	AlgSHA256 Algorithm = 0x000b
	AlgSHA384 Algorithm = 0x000c
	AlgSHA512 Algorithm = 0x000d
)

const (
	TagNoSessions uint16 = 0x8001
	TagSessions   uint16 = 0x8002
)

const (
	CCContextSave     CommandCode = 0x00000162
	CCContextLoad     CommandCode = 0x00000161
	CCFlushContext    CommandCode = 0x00000165
	CCPolicyCpHash    CommandCode = 0x0000016c
	CCPolicyNameHash  CommandCode = 0x0000016d
	CCPolicyGetDigest CommandCode = 0x00000173
	CCPolicyOR        CommandCode = 0x00000171
	CCNVWrite         CommandCode = 0x00000137
	CCQuote           CommandCode = 0x00000158
)

const (
	AttrContinueSession uint8 = 1 << 0
	AttrAuditExclusive  uint8 = 1 << 1
	AttrAuditReset      uint8 = 1 << 2
	AttrDecrypt         uint8 = 1 << 5
	AttrEncrypt         uint8 = 1 << 6
)

// Transport is the only TPM-facing dependency. Execute receives one complete
// TPM command packet and returns one complete TPM response packet.
//
// Implementations must not retry a command internally: retrying an
// authorization-protected command without an audit decision is unsafe.
type Transport interface {
	Execute(ctx context.Context, command []byte) ([]byte, error)
}

// TPM and CommandExecutor are descriptive aliases for a host-provided TPM
// command transport.
type TPM = Transport
type CommandExecutor = Transport

// TransportFunc adapts a function to Transport.
type TransportFunc func(context.Context, []byte) ([]byte, error)

func (f TransportFunc) Execute(ctx context.Context, command []byte) ([]byte, error) {
	return f(ctx, command)
}

// Size returns the digest size of a supported TPM hash algorithm.
func (a Algorithm) Size() (int, error) {
	switch a {
	case AlgSHA1:
		return sha1.Size, nil
	case AlgSHA256:
		return sha256.Size, nil
	case AlgSHA384:
		return sha512.Size384, nil
	case AlgSHA512:
		return sha512.Size, nil
	default:
		return 0, fmt.Errorf("%w: %#04x", ErrUnsupportedAlgorithm, uint16(a))
	}
}

func (a Algorithm) newHash() (hash.Hash, error) {
	switch a {
	case AlgSHA1:
		return sha1.New(), nil
	case AlgSHA256:
		return sha256.New(), nil
	case AlgSHA384:
		return sha512.New384(), nil
	case AlgSHA512:
		return sha512.New(), nil
	default:
		return nil, fmt.Errorf("%w: %#04x", ErrUnsupportedAlgorithm, uint16(a))
	}
}

func hashParts(a Algorithm, parts ...[]byte) ([]byte, error) {
	h, err := a.newHash()
	if err != nil {
		return nil, err
	}
	for _, part := range parts {
		_, _ = h.Write(part)
	}
	return h.Sum(nil), nil
}

// AuthValues contains only the mutable command authorization fields. Handles
// and parameters are kept in the frozen command and cannot be supplied here.
type AuthValues struct {
	SessionHandle Handle
	NonceCaller   []byte
	Attributes    uint8
	HMAC          []byte
}

type ResponseAuth struct {
	NonceTPM   []byte
	Attributes uint8
	HMAC       []byte
}

// Response is a parsed TPM response packet.
type Response struct {
	Tag        uint16
	Code       uint32
	Parameters []byte
	Auth       []ResponseAuth
	Raw        []byte
}

// TPMError is a non-success response code returned by the TPM.
type TPMError struct {
	Code      uint32
	Operation string
}

func (e *TPMError) Error() string {
	if e.Operation == "" {
		return fmt.Sprintf("tpm: response code %#08x", e.Code)
	}
	return fmt.Sprintf("tpm: %s failed with response code %#08x", e.Operation, e.Code)
}

// MarshalCommand encodes a TPM command. If auth is empty, the command uses
// TPM_ST_NO_SESSIONS; otherwise it uses TPM_ST_SESSIONS and places all mutable
// authorization values in the auth area after the parameters.
func MarshalCommand(code CommandCode, handles []Handle, parameters []byte, auth []AuthValues) ([]byte, error) {
	if code == 0 {
		return nil, errors.New("tpm: command code is zero")
	}
	if len(handles) > 8 {
		return nil, errors.New("tpm: too many command handles")
	}

	tag := TagNoSessions
	authSize := 0
	if len(auth) != 0 {
		tag = TagSessions
		authSize = 0
		for _, a := range auth {
			if a.SessionHandle == 0 {
				return nil, errors.New("tpm: authorization session handle is zero")
			}
			if len(a.NonceCaller) > 0xffff || len(a.HMAC) > 0xffff {
				return nil, errors.New("tpm: authorization value is too large")
			}
			authSize += 4 + 2 + len(a.NonceCaller) + 1 + 2 + len(a.HMAC)
		}
	}
	if len(parameters) > 0xffff-4 {
		return nil, errors.New("tpm: parameter area is too large")
	}

	total := 10 + len(handles)*4 + len(parameters)
	if len(auth) != 0 {
		total += 4 + authSize
	}
	if total > 0xffffffff {
		return nil, errors.New("tpm: command is too large")
	}

	out := make([]byte, 0, total)
	out = appendU16(out, tag)
	out = appendU32(out, uint32(total))
	out = appendU32(out, uint32(code))
	for _, h := range handles {
		out = appendU32(out, uint32(h))
	}
	if len(auth) != 0 {
		out = appendU32(out, uint32(authSize))
		for _, a := range auth {
			out = appendU32(out, uint32(a.SessionHandle))
			out = appendTPM2B(out, a.NonceCaller)
			out = append(out, a.Attributes)
			out = appendTPM2B(out, a.HMAC)
		}
	}
	out = append(out, parameters...)
	return out, nil
}

// ParseResponse validates the framing of a TPM response and splits its
// parameter and authorization areas.
func ParseResponse(raw []byte) (Response, error) {
	if len(raw) < 10 {
		return Response{}, fmt.Errorf("%w: short response", ErrInvalidResponse)
	}
	tag := binary.BigEndian.Uint16(raw[0:2])
	size := int(binary.BigEndian.Uint32(raw[2:6]))
	code := binary.BigEndian.Uint32(raw[6:10])
	if tag != TagNoSessions && tag != TagSessions {
		return Response{}, fmt.Errorf("%w: tag %#04x", ErrInvalidResponse, tag)
	}
	if size != len(raw) || size < 10 {
		return Response{}, fmt.Errorf("%w: size %d does not match packet %d", ErrInvalidResponse, size, len(raw))
	}

	resp := Response{Tag: tag, Code: code, Raw: append([]byte(nil), raw...)}
	if tag == TagNoSessions {
		resp.Parameters = append([]byte(nil), raw[10:]...)
		return resp, nil
	}

	if len(raw) < 14 {
		return Response{}, fmt.Errorf("%w: missing parameter size", ErrInvalidResponse)
	}
	paramSize := int(binary.BigEndian.Uint32(raw[10:14]))
	paramStart := 14
	authStart := paramStart + paramSize
	if paramSize < 0 || authStart < paramStart || authStart > len(raw) {
		return Response{}, fmt.Errorf("%w: invalid parameter size", ErrInvalidResponse)
	}
	resp.Parameters = append([]byte(nil), raw[paramStart:authStart]...)

	off := authStart
	for off < len(raw) {
		nonce, next, err := readTPM2B(raw, off)
		if err != nil {
			return Response{}, err
		}
		off = next
		if off >= len(raw) {
			return Response{}, fmt.Errorf("%w: missing session attributes", ErrInvalidResponse)
		}
		attrs := raw[off]
		off++
		hmac, next, err := readTPM2B(raw, off)
		if err != nil {
			return Response{}, err
		}
		off = next
		resp.Auth = append(resp.Auth, ResponseAuth{
			NonceTPM:   append([]byte(nil), nonce...),
			Attributes: attrs,
			HMAC:       append([]byte(nil), hmac...),
		})
	}
	return resp, nil
}

func appendU16(dst []byte, v uint16) []byte {
	var b [2]byte
	binary.BigEndian.PutUint16(b[:], v)
	return append(dst, b[:]...)
}

func appendU32(dst []byte, v uint32) []byte {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], v)
	return append(dst, b[:]...)
}

func appendTPM2B(dst, value []byte) []byte {
	dst = appendU16(dst, uint16(len(value)))
	return append(dst, value...)
}

func readU16(src []byte, off int) (uint16, int, error) {
	if off < 0 || off+2 > len(src) {
		return 0, off, fmt.Errorf("%w: truncated uint16", ErrInvalidResponse)
	}
	return binary.BigEndian.Uint16(src[off : off+2]), off + 2, nil
}

func readU32(src []byte, off int) (uint32, int, error) {
	if off < 0 || off+4 > len(src) {
		return 0, off, fmt.Errorf("%w: truncated uint32", ErrInvalidResponse)
	}
	return binary.BigEndian.Uint32(src[off : off+4]), off + 4, nil
}

func readTPM2B(src []byte, off int) ([]byte, int, error) {
	n, off, err := readU16(src, off)
	if err != nil {
		return nil, off, err
	}
	end := off + int(n)
	if end < off || end > len(src) {
		return nil, off, fmt.Errorf("%w: truncated TPM2B", ErrInvalidResponse)
	}
	return src[off:end], end, nil
}
