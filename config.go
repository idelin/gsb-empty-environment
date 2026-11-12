package netsec

import (
	"bytes"
	"fmt"
	"strconv"
)

const (
	// MaxProtocolKeyLength is the Memcached wire limit on key size.
	MaxProtocolKeyLength = 250

	defaultMaxCommandLine = 64 << 10
	defaultMaxValue       = 8 << 20
	defaultMaxResponse    = 64 << 20
	defaultMaxKeys        = 1024
)

// Config controls resource limits and protocol support. The zero value uses
// the package defaults.
type Config struct {
	// MaxKeyLength is the maximum length of a key after the tenant prefix is
	// added. It may not exceed MaxProtocolKeyLength. Zero means 250.
	MaxKeyLength int
	// MaxCommandLineLength bounds text command/response line content
	// (excluding CRLF), before values are counted. Zero means 64 KiB.
	MaxCommandLineLength int
	// MaxValueLength bounds a text storage data block or a binary value.
	// Zero means 8 MiB.
	MaxValueLength int
	// MaxResponseLength bounds the filtered response emitted for one request.
	// Zero means 64 MiB.
	MaxResponseLength int
	// MaxKeysPerRequest bounds the number of keys in a text retrieval
	// command. Zero means 1024.
	MaxKeysPerRequest int
	// AllowBinary enables normalization and forwarding of the binary protocol.
	// It is off by default because this proxy forwards text requests.
	AllowBinary bool
}

func (c Config) withDefaults() (Config, error) {
	if c.MaxKeyLength == 0 {
		c.MaxKeyLength = MaxProtocolKeyLength
	}
	if c.MaxCommandLineLength == 0 {
		c.MaxCommandLineLength = defaultMaxCommandLine
	}
	if c.MaxValueLength == 0 {
		c.MaxValueLength = defaultMaxValue
	}
	if c.MaxResponseLength == 0 {
		c.MaxResponseLength = defaultMaxResponse
	}
	if c.MaxKeysPerRequest == 0 {
		c.MaxKeysPerRequest = defaultMaxKeys
	}
	switch {
	case c.MaxKeyLength < 1 || c.MaxKeyLength > MaxProtocolKeyLength:
		return Config{}, fmt.Errorf("%w: max key length must be between 1 and %d",
			ErrInvalidConfig, MaxProtocolKeyLength)
	case c.MaxCommandLineLength < 0:
		return Config{}, fmt.Errorf("%w: negative command line limit", ErrTooLarge)
	case c.MaxValueLength < 0:
		return Config{}, fmt.Errorf("%w: negative value limit", ErrTooLarge)
	case c.MaxResponseLength < 0:
		return Config{}, fmt.Errorf("%w: negative response limit", ErrTooLarge)
	case c.MaxKeysPerRequest < 0:
		return Config{}, fmt.Errorf("%w: negative key count limit", ErrTooLarge)
	}
	return c, nil
}

// Normalizer holds immutable tenant configuration and creates a Session for
// each client connection. It is safe for concurrent use.
type Normalizer struct {
	prefix []byte
	cfg    Config
}

// NewNormalizer validates the tenant prefix and limits. Omit the config or
// pass nil to use the package defaults.
func NewNormalizer(prefix string, cfgs ...*Config) (*Normalizer, error) {
	return NewNormalizerBytes([]byte(prefix), cfgs...)
}

// NewNormalizerBytes is NewNormalizer for callers that already hold a prefix
// as bytes.
func NewNormalizerBytes(prefix []byte, cfgs ...*Config) (*Normalizer, error) {
	if len(cfgs) > 1 {
		return nil, fmt.Errorf("%w: multiple configs supplied", ErrInvalidConfig)
	}
	if len(prefix) == 0 {
		return nil, fmt.Errorf("%w: prefix is empty", ErrInvalidPrefix)
	}
	if len(prefix) > MaxProtocolKeyLength {
		return nil, fmt.Errorf("%w: prefix is %d bytes", ErrInvalidPrefix, len(prefix))
	}
	if err := validateKeyBytes(prefix); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidPrefix, err)
	}
	var effective Config
	if len(cfgs) == 1 && cfgs[0] != nil {
		effective = *cfgs[0]
	}
	var err error
	effective, err = effective.withDefaults()
	if err != nil {
		return nil, err
	}
	if len(prefix) >= effective.MaxKeyLength {
		return nil, fmt.Errorf("%w: prefix leaves no room for a key", ErrInvalidPrefix)
	}
	return &Normalizer{
		prefix: append([]byte(nil), prefix...),
		cfg:    effective,
	}, nil
}

// Prefix returns a copy of the configured tenant prefix.
func (n *Normalizer) Prefix() []byte {
	return append([]byte(nil), n.prefix...)
}

// ValidateKey checks a key body without applying the tenant prefix.
func ValidateKey(key []byte) error {
	if len(key) == 0 {
		return fmt.Errorf("%w: empty key", ErrInvalidKey)
	}
	return validateKeyBytes(key)
}

func validateKeyBytes(key []byte) error {
	for i, b := range key {
		if b <= 0x20 || b == 0x7f {
			return fmt.Errorf("%w: byte %d is %#x", ErrInvalidKey, i, b)
		}
	}
	return nil
}

// PrefixKey validates an unprefixed key, adds the tenant prefix, and enforces
// the configured maximum length.
func (n *Normalizer) PrefixKey(key []byte) ([]byte, error) {
	if err := ValidateKey(key); err != nil {
		return nil, err
	}
	if len(n.prefix)+len(key) > n.cfg.MaxKeyLength {
		return nil, fmt.Errorf("%w: %d + %d > %d",
			ErrKeyTooLong, len(n.prefix), len(key), n.cfg.MaxKeyLength)
	}
	out := make([]byte, len(n.prefix)+len(key))
	copy(out, n.prefix)
	copy(out[len(n.prefix):], key)
	return out, nil
}

// StripKey removes the tenant prefix from a response key. It reports false
// for keys belonging to another tenant or for an invalid remainder.
func (n *Normalizer) StripKey(key []byte) ([]byte, bool) {
	if !bytes.HasPrefix(key, n.prefix) {
		return nil, false
	}
	rest := key[len(n.prefix):]
	if len(rest) == 0 || len(rest) > n.cfg.MaxKeyLength-len(n.prefix) {
		return nil, false
	}
	if err := validateKeyBytes(rest); err != nil {
		return nil, false
	}
	return append([]byte(nil), rest...), true
}

func parseUintToken(tok []byte) (uint64, error) {
	if len(tok) == 0 || tok[0] < '0' || tok[0] > '9' {
		return 0, fmt.Errorf("%w: invalid unsigned integer %q", ErrMalformedRequest, tok)
	}
	v, err := strconv.ParseUint(string(tok), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: invalid unsigned integer %q", ErrMalformedRequest, tok)
	}
	return v, nil
}

func parseUint32Token(tok []byte) (uint32, error) {
	v, err := parseUintToken(tok)
	if err != nil || v > 1<<32-1 {
		return 0, fmt.Errorf("%w: unsigned 32-bit value out of range %q", ErrMalformedRequest, tok)
	}
	return uint32(v), nil
}

func parseExptimeToken(tok []byte) (int64, error) {
	if len(tok) == 0 {
		return 0, fmt.Errorf("%w: empty expiration", ErrMalformedRequest)
	}
	if tok[0] == '-' {
		if len(tok) == 1 {
			return 0, fmt.Errorf("%w: invalid expiration %q", ErrMalformedRequest, tok)
		}
		for _, b := range tok[1:] {
			if b < '0' || b > '9' {
				return 0, fmt.Errorf("%w: invalid expiration %q", ErrMalformedRequest, tok)
			}
		}
		v, err := strconv.ParseInt(string(tok), 10, 32)
		if err != nil {
			return 0, fmt.Errorf("%w: expiration out of range %q", ErrMalformedRequest, tok)
		}
		return v, nil
	}
	for _, b := range tok {
		if b < '0' || b > '9' {
			return 0, fmt.Errorf("%w: invalid expiration %q", ErrMalformedRequest, tok)
		}
	}
	v, err := strconv.ParseUint(string(tok), 10, 32)
	if err != nil {
		return 0, fmt.Errorf("%w: expiration out of range %q", ErrMalformedRequest, tok)
	}
	// Memcached stores positive expirations as signed 32-bit values.
	if v > 1<<31-1 {
		return 0, fmt.Errorf("%w: expiration out of range %q", ErrMalformedRequest, tok)
	}
	return int64(v), nil
}

func isNoReply(tok []byte) bool {
	return bytes.EqualFold(tok, []byte("noreply"))
}
