package grpcweb

import (
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
)

const (
	// StatusTrailer is the canonical gRPC status trailer.
	StatusTrailer = "grpc-status"

	// MessageTrailer is the canonical gRPC status message trailer.
	MessageTrailer = "grpc-message"

	// reservedPrefix is owned by the gRPC wire protocol. Upstream metadata
	// using it is rejected so a client cannot inject protocol trailers.
	reservedPrefix = "grpc-"
)

var (
	// ErrControlChar means a key or value contained a C0 control character
	// (including CR and LF) or DEL, which would allow trailer line splitting.
	ErrControlChar = errors.New("grpcweb: key or value contains a control character")

	// ErrReservedKey means custom metadata used the reserved "grpc-" prefix.
	ErrReservedKey = errors.New("grpcweb: custom metadata must not use the reserved grpc- prefix")

	// ErrInvalidKey means a metadata key is not a legal lowercase token.
	ErrInvalidKey = errors.New("grpcweb: invalid metadata key")

	// ErrDuplicateTrailer means the canonical status or message trailer had
	// more than one value.
	ErrDuplicateTrailer = errors.New("grpcweb: duplicate canonical trailer value")

	// ErrMissingStatus means the authoritative grpc-status trailer is absent.
	ErrMissingStatus = errors.New("grpcweb: missing grpc-status trailer")

	// ErrInvalidStatus means grpc-status is not a decimal integer.
	ErrInvalidStatus = errors.New("grpcweb: grpc-status must be a decimal integer")

	// ErrMalformedTrailer means a trailer block line is not "key: value\r\n".
	ErrMalformedTrailer = errors.New("grpcweb: malformed trailer block line")
)

// TrailerBlock is the parsed contents of a FlagTrailer frame.
type TrailerBlock struct {
	// Status is the grpc-status value; HasStatus reports whether it was set.
	Status int

	// Message is the grpc-message value; HasMessage reports whether it was set.
	Message string

	// Metadata holds non-canonical, non-reserved custom trailers keyed by
	// their validated lowercase names.
	Metadata map[string]string

	HasStatus  bool
	HasMessage bool
}

// isForbiddenControl reports whether b is an ASCII control character that
// must never appear in a validated key or value. CR and LF are covered here,
// which blocks CRLF line injection into the serialized trailer block.
func isForbiddenControl(b byte) bool {
	return b < 0x20 || b == 0x7f
}

// validateValue rejects values containing control characters.
func validateValue(value string) error {
	for i := 0; i < len(value); i++ {
		if isForbiddenControl(value[i]) {
			return ErrControlChar
		}
	}
	return nil
}

// validateKeyShape enforces the gRPC-Web trailer key grammar: lowercase
// ASCII letters, digits, '-', '_', '.' and percent-encoded characters.
func validateKeyShape(key string) error {
	if key == "" {
		return ErrInvalidKey
	}
	for i := 0; i < len(key); i++ {
		c := key[i]
		if isForbiddenControl(c) {
			return ErrControlChar
		}
		switch {
		case c >= 'a' && c <= 'z':
		case c >= '0' && c <= '9':
		case c == '-' || c == '_' || c == '.' || c == '%':
		default:
			return ErrInvalidKey
		}
	}
	return nil
}

// validateCustomKey applies the key grammar and the reserved-prefix rule to
// attacker-controlled metadata.
func validateCustomKey(key string) error {
	if err := validateKeyShape(key); err != nil {
		return err
	}
	if strings.HasPrefix(key, reservedPrefix) {
		return ErrReservedKey
	}
	return nil
}

// appendLine writes a validated "key: value\r\n" line to dst.
func appendLine(dst []byte, key, value string) []byte {
	dst = append(dst, key...)
	dst = append(dst, ':', ' ')
	dst = append(dst, value...)
	return append(dst, '\r', '\n')
}

// StatusFromHTTP2Trailers extracts the authoritative grpc-status from real
// HTTP/2 response trailers. It never consults gRPC metadata, so a status
// smuggled in an upstream header cannot be accepted.
func StatusFromHTTP2Trailers(trailers http.Header) (int, error) {
	if trailers == nil {
		return 0, ErrMissingStatus
	}
	values, ok := trailers[http.CanonicalHeaderKey(StatusTrailer)]
	if !ok || len(values) == 0 {
		return 0, ErrMissingStatus
	}
	if len(values) != 1 {
		return 0, ErrDuplicateTrailer
	}
	status, err := strconv.Atoi(values[0])
	if err != nil {
		return 0, fmt.Errorf("%w: %q", ErrInvalidStatus, values[0])
	}
	return status, nil
}

// MarshalTrailerFrame builds the single FlagTrailer frame for an upstream
// response. The grpc-status and grpc-message lines are derived exclusively
// from the HTTP/2 trailers; custom metadata is validated and is structurally
// unable to override them.
func MarshalTrailerFrame(trailers http.Header) ([]byte, error) {
	if trailers == nil {
		return nil, ErrMissingStatus
	}

	status, err := StatusFromHTTP2Trailers(trailers)
	if err != nil {
		return nil, err
	}

	messageValues := trailers[http.CanonicalHeaderKey(MessageTrailer)]
	switch len(messageValues) {
	case 0:
	case 1:
		if err := validateValue(messageValues[0]); err != nil {
			return nil, err
		}
	default:
		return nil, ErrDuplicateTrailer
	}

	var custom []string
	seen := make(map[string]struct{})
	for rawKey, values := range trailers {
		key := strings.ToLower(rawKey)
		if key == StatusTrailer || key == MessageTrailer {
			continue
		}
		if err := validateCustomKey(key); err != nil {
			return nil, err
		}
		if _, dup := seen[key]; dup {
			return nil, fmt.Errorf("grpcweb: duplicate metadata key %q", key)
		}
		seen[key] = struct{}{}
		for _, value := range values {
			if err := validateValue(value); err != nil {
				return nil, err
			}
			custom = append(custom, key+"\x00"+value)
		}
	}
	sort.Strings(custom)

	payload := make([]byte, 0, FrameHeaderSize)
	for _, entry := range custom {
		key, value, _ := strings.Cut(entry, "\x00")
		payload = appendLine(payload, key, value)
	}
	if len(messageValues) == 1 {
		payload = appendLine(payload, MessageTrailer, messageValues[0])
	}
	payload = appendLine(payload, StatusTrailer, strconv.Itoa(status))

	frame := make([]byte, FrameHeaderSize+len(payload))
	PutFrameHeader(frame, FlagTrailer, len(payload))
	copy(frame[FrameHeaderSize:], payload)
	return frame, nil
}

// ParseTrailerBlock parses and validates a FlagTrailer frame payload. Every
// line must be "key: value\r\n" and every byte passes the same validation
// used when marshaling, so a trailer produced by an attacker cannot smuggle
// extra lines or claim reserved names. A block without grpc-status parses
// successfully with HasStatus == false; callers must then reject it through
// VerifyStatus or StatusFromHTTP2Trailers.
func ParseTrailerBlock(payload []byte) (TrailerBlock, error) {
	block := TrailerBlock{Metadata: make(map[string]string)}

	lines := strings.Split(string(payload), "\r\n")
	// A well-formed block ends with CRLF, so Split yields a final empty
	// element. Any other empty element means a blank line was injected.
	if len(lines) == 0 || lines[len(lines)-1] != "" {
		return TrailerBlock{}, ErrMalformedTrailer
	}
	lines = lines[:len(lines)-1]

	for _, line := range lines {
		if line == "" {
			return TrailerBlock{}, ErrMalformedTrailer
		}
		key, value, found := strings.Cut(line, ":")
		if !found {
			return TrailerBlock{}, ErrMalformedTrailer
		}
		// gRPC-Web requires exactly one separating space after the colon.
		if !strings.HasPrefix(value, " ") {
			return TrailerBlock{}, ErrMalformedTrailer
		}
		value = value[1:]

		switch key {
		case StatusTrailer:
			if block.HasStatus {
				return TrailerBlock{}, ErrDuplicateTrailer
			}
			if err := validateValue(value); err != nil {
				return TrailerBlock{}, err
			}
			status, err := strconv.Atoi(value)
			if err != nil {
				return TrailerBlock{}, fmt.Errorf("%w: %q", ErrInvalidStatus, value)
			}
			block.Status = status
			block.HasStatus = true
		case MessageTrailer:
			if block.HasMessage {
				return TrailerBlock{}, ErrDuplicateTrailer
			}
			if err := validateValue(value); err != nil {
				return TrailerBlock{}, err
			}
			block.Message = value
			block.HasMessage = true
		default:
			if err := validateCustomKey(key); err != nil {
				return TrailerBlock{}, err
			}
			if err := validateValue(value); err != nil {
				return TrailerBlock{}, err
			}
			if _, exists := block.Metadata[key]; exists {
				return TrailerBlock{}, fmt.Errorf("grpcweb: duplicate metadata key %q", key)
			}
			block.Metadata[key] = value
		}
	}

	return block, nil
}

// VerifyStatus checks that a parsed trailer block carries a grpc-status and
// that it matches the authoritative status derived from the real HTTP/2
// trailers. A mismatch indicates a forged gRPC-Web trailer frame.
func VerifyStatus(block TrailerBlock, trailers http.Header) error {
	if !block.HasStatus {
		return ErrMissingStatus
	}
	authoritative, err := StatusFromHTTP2Trailers(trailers)
	if err != nil {
		return err
	}
	if block.Status != authoritative {
		return fmt.Errorf("%w: frame=%d http2=%d", ErrStatusMismatch, block.Status, authoritative)
	}
	return nil
}
