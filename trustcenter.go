package netsec

import (
	"bytes"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
)

// EUI64 is the numeric form of an IEEE EUI-64/Zigbee extended address.
type EUI64 = uint64

// HashFunc derives a link key from an install-code payload. The payload
// contains the 6, 8, 12, or 16 install-code bytes, excluding the CRC.
type HashFunc func(installCode []byte) []byte

// HashFuncWithError is the error-returning variant of HashFunc.
type HashFuncWithError func(installCode []byte) ([]byte, error)

// HashFunc16 and HashFunc32 accept hash functions that return fixed-size
// arrays, such as crypto/sha256.Sum256.
type HashFunc16 func(installCode []byte) [16]byte
type HashFunc32 func(installCode []byte) [32]byte

// LinkKeyDeriver is an alias for HashFunc.
type LinkKeyDeriver = HashFunc

var (
	// ErrDeviceNotRegistered indicates that no install code has been
	// registered for the EUI64.
	ErrDeviceNotRegistered = errors.New("EUI64 is not registered")

	// ErrInstallCodeMismatch indicates that the supplied install code,
	// although well-formed, differs from the code registered for the EUI64.
	ErrInstallCodeMismatch = errors.New("install code does not match the registered device")

	// ErrDefaultGlobalLinkKey indicates an attempt to use the public default
	// global link key for a registered device.
	ErrDefaultGlobalLinkKey = errors.New("default global link key is not allowed")

	// ErrDeviceLocked indicates that consecutive join failures reached the
	// caller-configured limit.
	ErrDeviceLocked = errors.New("EUI64 is locked after consecutive join failures")

	// ErrLinkKeyMismatch indicates that the link key presented with a join
	// request does not match the key derived from the registered install code.
	ErrLinkKeyMismatch = errors.New("link key does not match derived install-code link key")

	// ErrInvalidEUI64 indicates that an EUI64 value could not be parsed.
	ErrInvalidEUI64 = errors.New("invalid EUI64")

	// ErrNoHashFunction indicates that the trust center has no usable
	// caller-supplied install-code hash function.
	ErrNoHashFunction = errors.New("install-code hash function is not configured")

	// ErrInvalidLinkKey indicates an empty or otherwise unusable derived
	// link key.
	ErrInvalidLinkKey = errors.New("derived link key is invalid")

	// ErrInvalidConfiguration indicates an unusable trust-center setting.
	ErrInvalidConfiguration = errors.New("invalid trust center configuration")

	// Compatibility aliases for common names used by callers.
	ErrNotRegistered       = ErrDeviceNotRegistered
	ErrUnknownDevice       = ErrDeviceNotRegistered
	ErrCodeMismatch        = ErrInstallCodeMismatch
	ErrDefaultGlobalKey    = ErrDefaultGlobalLinkKey
	ErrDefaultLinkKey      = ErrDefaultGlobalLinkKey
	ErrGlobalKeyNotAllowed = ErrDefaultGlobalLinkKey
	ErrLocked              = ErrDeviceLocked
	ErrDeviceLockout       = ErrDeviceLocked
	ErrLockout             = ErrDeviceLocked
	ErrKeyMismatch         = ErrLinkKeyMismatch
	ErrBadCRC              = ErrInvalidInstallCodeCRC
)

const (
	// DefaultMaxFailures is used when the caller does not specify a lockout
	// limit.
	DefaultMaxFailures = 3

	// DefaultFailureLimit is an alias for DefaultMaxFailures.
	DefaultFailureLimit = DefaultMaxFailures

	// DefaultGlobalLinkKeyHex is the well-known Zigbee Alliance default
	// global trust-center link key.
	DefaultGlobalLinkKeyHex = "5A6967426565416C6C69616E63653031"
)

// DefaultGlobalLinkKey is the public Zigbee default global link key. The
// trust center never accepts it for a registered EUI64.
var DefaultGlobalLinkKey = []byte{
	0x5A, 0x69, 0x67, 0x42, 0x65, 0x65, 0x41, 0x6C,
	0x6C, 0x69, 0x61, 0x6E, 0x63, 0x65, 0x30, 0x31,
}

// DefaultGlobalKey is an alias for DefaultGlobalLinkKey.
var DefaultGlobalKey = DefaultGlobalLinkKey

// ZigbeeDefaultGlobalLinkKey is an alias for DefaultGlobalLinkKey.
var ZigbeeDefaultGlobalLinkKey = DefaultGlobalLinkKey

// IsDefaultGlobalLinkKey reports whether key is the built-in public Zigbee
// default global link key.
func IsDefaultGlobalLinkKey(key []byte) bool {
	return keysEqual(key, DefaultGlobalLinkKey)
}

// Config contains trust-center settings accepted by NewTrustCenter. It is
// provided for callers that prefer a named configuration over positional
// arguments.
type Config struct {
	Hash                   any
	HashFunc               any
	MaxFailures            int
	FailureLimit           int
	MaxConsecutiveFailures int
	DefaultGlobalLinkKey   []byte
}

// TrustCenterConfig is an alias for Config.
type TrustCenterConfig = Config

// JoinRecord is an alias for JoinRequest.
type JoinRecord = JoinRequest

// Option configures a TrustCenter created by NewTrustCenter.
type Option func(*TrustCenter) error

// WithMaxFailures sets the number of consecutive failures after which an
// EUI64 is locked.
func WithMaxFailures(n int) Option {
	return func(tc *TrustCenter) error {
		return tc.SetMaxFailures(n)
	}
}

// WithMaxConsecutiveFailures is an alias for WithMaxFailures.
func WithMaxConsecutiveFailures(n int) Option {
	return WithMaxFailures(n)
}

// WithFailureLimit is an alias for WithMaxFailures.
func WithFailureLimit(n int) Option {
	return WithMaxFailures(n)
}

// WithDefaultGlobalLinkKey replaces the default public global key used for
// rejection checks. It does not make that key acceptable.
func WithDefaultGlobalLinkKey(key []byte) Option {
	return func(tc *TrustCenter) error {
		tc.SetDefaultGlobalLinkKey(key)
		return nil
	}
}

// WithDefaultGlobalKey is an alias for WithDefaultGlobalLinkKey.
func WithDefaultGlobalKey(key []byte) Option {
	return WithDefaultGlobalLinkKey(key)
}

// WithHash sets the caller-supplied install-code hash function.
func WithHash(hash any) Option {
	return func(tc *TrustCenter) error {
		return tc.SetHash(hash)
	}
}

// WithHashFunc is an alias for WithHash.
func WithHashFunc(hash any) Option {
	return WithHash(hash)
}

// JoinRequest is the input to HandleJoin. LinkKey is optional; when it is
// non-nil, the trust center verifies it against the key derived from the
// registered install code.
type JoinRequest struct {
	EUI64       EUI64
	InstallCode []byte
	LinkKey     []byte
}

// DeviceStatus is a snapshot of a registered device's lockout state.
type DeviceStatus struct {
	EUI64               EUI64
	Registered          bool
	Locked              bool
	ConsecutiveFailures int
	FailureLimit        int
}

type deviceEntry struct {
	installCode []byte
	linkKey     []byte
	failures    int
	locked      bool
}

// TrustCenter stores install codes by EUI64 and enforces join policy.
//
// It is safe for concurrent use.
type TrustCenter struct {
	mu            sync.Mutex
	hash          func([]byte) ([]byte, error)
	maxFailures   int
	defaultKey    []byte
	devices       map[EUI64]*deviceEntry
	configuration error
}

// NewTrustCenter creates a trust center. Arguments may be supplied in any
// order and may include:
//
//   - a hash function (func([]byte) []byte or func([]byte) ([]byte, error));
//   - an int failure limit;
//   - a []byte replacement for the default global link key;
//   - a Config, *Config, or Option.
//
// If no failure limit is supplied, DefaultMaxFailures is used. If no default
// key is supplied, the built-in Zigbee default global link key is used.
func NewTrustCenter(args ...any) *TrustCenter {
	tc := &TrustCenter{
		maxFailures: DefaultMaxFailures,
		defaultKey:  bytes.Clone(DefaultGlobalLinkKey),
		devices:     make(map[EUI64]*deviceEntry),
	}

	for _, arg := range args {
		tc.applyArgument(arg)
	}

	if tc.maxFailures < 1 {
		tc.maxFailures = DefaultMaxFailures
	}
	if len(tc.defaultKey) == 0 {
		tc.defaultKey = bytes.Clone(DefaultGlobalLinkKey)
	}
	return tc
}

// New is an alias for NewTrustCenter.
func New(args ...any) *TrustCenter {
	return NewTrustCenter(args...)
}

// NewTrustCenterWithError is like NewTrustCenter but reports an invalid hash
// or option instead of deferring the error until the first join.
func NewTrustCenterWithError(args ...any) (*TrustCenter, error) {
	tc := NewTrustCenter(args...)
	if tc.configuration != nil {
		return nil, tc.configuration
	}
	return tc, nil
}

func (tc *TrustCenter) applyArgument(arg any) {
	if arg == nil {
		return
	}

	switch value := arg.(type) {
	case Config:
		tc.applyConfig(value)
	case *Config:
		if value != nil {
			tc.applyConfig(*value)
		}
	case Option:
		if err := value(tc); err != nil && tc.configuration == nil {
			tc.configuration = err
		}
	case int:
		if err := tc.SetMaxFailures(value); err != nil {
			tc.rememberConfigurationError(err)
		}
	case uint:
		tc.setMaxFailuresUint(uint64(value))
	case uint8:
		tc.setMaxFailuresUint(uint64(value))
	case uint16:
		tc.setMaxFailuresUint(uint64(value))
	case uint32:
		tc.setMaxFailuresUint(uint64(value))
	case uint64:
		tc.setMaxFailuresUint(value)
	case []byte:
		tc.SetDefaultGlobalLinkKey(value)
	case string:
		// A string in the constructor is useful for a default-key override
		// expressed as hexadecimal. It is not interpreted as a hash function.
		if decoded, ok := decodeHexString(value); ok {
			tc.SetDefaultGlobalLinkKey(decoded)
		}
	default:
		if err := tc.SetHash(arg); err != nil {
			tc.rememberConfigurationError(err)
		}
	}
}

func (tc *TrustCenter) applyConfig(cfg Config) {
	if cfg.HashFunc != nil {
		if err := tc.SetHash(cfg.HashFunc); err != nil {
			tc.rememberConfigurationError(err)
		}
	} else if cfg.Hash != nil {
		if err := tc.SetHash(cfg.Hash); err != nil {
			tc.rememberConfigurationError(err)
		}
	}

	switch {
	case cfg.MaxConsecutiveFailures > 0:
		tc.setMaxFailuresUint(uint64(cfg.MaxConsecutiveFailures))
	case cfg.FailureLimit > 0:
		tc.setMaxFailuresUint(uint64(cfg.FailureLimit))
	case cfg.MaxFailures > 0:
		tc.setMaxFailuresUint(uint64(cfg.MaxFailures))
	}

	if cfg.DefaultGlobalLinkKey != nil {
		tc.SetDefaultGlobalLinkKey(cfg.DefaultGlobalLinkKey)
	}
}

func (tc *TrustCenter) setMaxFailuresUint(n uint64) {
	if n == 0 {
		tc.maxFailures = 1
		return
	}
	if n > uint64(^uint(0)>>1) {
		tc.rememberConfigurationError(fmt.Errorf("%w: failure limit is too large", ErrInvalidConfiguration))
		return
	}
	tc.maxFailures = int(n)
}

func (tc *TrustCenter) rememberConfigurationError(err error) {
	if tc.configuration == nil {
		tc.configuration = err
	}
}

// SetHash configures the caller-supplied hash function. Supported forms are
// func([]byte) []byte, func([]byte) ([]byte, error), HashFunc,
// HashFuncWithError, and matching Hash/ HashInstallCode/ DeriveLinkKey
// methods.
func (tc *TrustCenter) SetHash(hash any) error {
	fn, err := normalizeHash(hash)
	if err != nil {
		return err
	}

	tc.mu.Lock()
	tc.hash = fn
	tc.mu.Unlock()
	return nil
}

func normalizeHash(hash any) (func([]byte) ([]byte, error), error) {
	switch fn := hash.(type) {
	case func([]byte) []byte:
		if fn == nil {
			return nil, ErrNoHashFunction
		}
		return func(code []byte) ([]byte, error) { return fn(code), nil }, nil
	case HashFunc:
		if fn == nil {
			return nil, ErrNoHashFunction
		}
		return func(code []byte) ([]byte, error) { return fn(code), nil }, nil
	case func([]byte) ([]byte, error):
		if fn == nil {
			return nil, ErrNoHashFunction
		}
		return fn, nil
	case HashFuncWithError:
		if fn == nil {
			return nil, ErrNoHashFunction
		}
		return fn, nil
	case func([]byte) [16]byte:
		if fn == nil {
			return nil, ErrNoHashFunction
		}
		return func(code []byte) ([]byte, error) {
			sum := fn(code)
			return bytes.Clone(sum[:]), nil
		}, nil
	case HashFunc16:
		if fn == nil {
			return nil, ErrNoHashFunction
		}
		return func(code []byte) ([]byte, error) {
			sum := fn(code)
			return bytes.Clone(sum[:]), nil
		}, nil
	case func([]byte) [32]byte:
		if fn == nil {
			return nil, ErrNoHashFunction
		}
		return func(code []byte) ([]byte, error) {
			sum := fn(code)
			return bytes.Clone(sum[:]), nil
		}, nil
	case HashFunc32:
		if fn == nil {
			return nil, ErrNoHashFunction
		}
		return func(code []byte) ([]byte, error) {
			sum := fn(code)
			return bytes.Clone(sum[:]), nil
		}, nil
	case interface{ Hash([]byte) []byte }:
		return func(code []byte) ([]byte, error) { return fn.Hash(code), nil }, nil
	case interface{ HashInstallCode([]byte) []byte }:
		return func(code []byte) ([]byte, error) { return fn.HashInstallCode(code), nil }, nil
	case interface{ HashInstallCode([]byte) ([]byte, error) }:
		return fn.HashInstallCode, nil
	case interface{ DeriveLinkKey([]byte) ([]byte, error) }:
		return fn.DeriveLinkKey, nil
	default:
		return nil, fmt.Errorf("%w: unsupported type %T", ErrNoHashFunction, hash)
	}
}

// DeriveLinkKey derives a link key from an install code with a
// caller-supplied hash function. installCode may be either wire format with
// its CRC suffix or a bare 6-, 8-, 12-, or 16-byte payload.
func DeriveLinkKey(installCode []byte, hash any) ([]byte, error) {
	fn, err := normalizeHash(hash)
	if err != nil {
		return nil, err
	}
	return deriveLinkKey(fn, installCode)
}

// DeriveLinkKey derives a link key using the trust center's configured hash.
func (tc *TrustCenter) DeriveLinkKey(installCode []byte) ([]byte, error) {
	tc.mu.Lock()
	hash := tc.hash
	tc.mu.Unlock()
	if hash == nil {
		return nil, ErrNoHashFunction
	}
	return deriveLinkKey(hash, installCode)
}

func deriveLinkKey(hash func([]byte) ([]byte, error), installCode []byte) ([]byte, error) {
	code, err := ParseInstallCode(installCode)
	if err != nil {
		// A bare payload of 8 bytes is indistinguishable from a six-byte
		// payload with a bad CRC. Treat it as wire format so corrupt CRCs
		// are never silently accepted.
		possibleWireLength := false
		for _, payloadLength := range []int{
			InstallCodeLength6,
			InstallCodeLength8,
			InstallCodeLength12,
			InstallCodeLength16,
		} {
			if len(installCode) == payloadLength+InstallCodeCRC16Length {
				possibleWireLength = true
				break
			}
		}
		if !ValidInstallCodeLength(len(installCode)) || possibleWireLength {
			return nil, err
		}
		code = bytes.Clone(installCode)
	}

	key, err := hash(code)
	if err != nil {
		return nil, fmt.Errorf("derive install-code link key: %w", err)
	}
	key = bytes.Clone(key)
	if len(key) == 0 {
		return nil, ErrInvalidLinkKey
	}
	return key, nil
}

// SetMaxFailures sets the consecutive-failure limit. A limit of zero is
// treated as one, so the first failure locks the EUI64.
func (tc *TrustCenter) SetMaxFailures(n int) error {
	if n < 0 {
		return fmt.Errorf("%w: failure limit must not be negative", ErrInvalidConfiguration)
	}
	tc.mu.Lock()
	if n == 0 {
		n = 1
	}
	tc.maxFailures = n
	tc.mu.Unlock()
	return nil
}

// MaxFailures returns the configured consecutive-failure limit.
func (tc *TrustCenter) MaxFailures() int {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	return tc.maxFailures
}

// FailureLimit is an alias for MaxFailures.
func (tc *TrustCenter) FailureLimit() int {
	return tc.MaxFailures()
}

// SetFailureLimit is an alias for SetMaxFailures.
func (tc *TrustCenter) SetFailureLimit(n int) error {
	return tc.SetMaxFailures(n)
}

// SetDefaultGlobalLinkKey replaces the key treated as the forbidden default.
// The key is copied, and it is still forbidden rather than trusted.
func (tc *TrustCenter) SetDefaultGlobalLinkKey(key []byte) {
	tc.mu.Lock()
	tc.defaultKey = bytes.Clone(key)
	tc.mu.Unlock()
}

// DefaultGlobalLinkKeyValue returns a copy of the configured forbidden
// default global link key.
func (tc *TrustCenter) DefaultGlobalLinkKeyValue() []byte {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	return bytes.Clone(tc.defaultKey)
}

// DefaultGlobalLinkKey returns a copy of the configured forbidden default
// global link key.
func (tc *TrustCenter) DefaultGlobalLinkKey() []byte {
	return tc.DefaultGlobalLinkKeyValue()
}

// DefaultGlobalKey returns a copy of the configured forbidden default global
// link key.
func (tc *TrustCenter) DefaultGlobalKey() []byte {
	return tc.DefaultGlobalLinkKeyValue()
}

// Register validates and stores a wire-format install code for eui.
// eui may be a uint64, [8]byte, []byte, an EUI64 string, or another unsigned
// integer type. installCode may be []byte or a hexadecimal string.
//
// An optional third argument can supply a one-off hash function for this
// registration. Otherwise the trust center's configured hash is used.
//
// Registering an EUI64 again replaces the old code and clears its failure
// count and lockout state.
func (tc *TrustCenter) Register(eui any, installCode any, hashes ...any) error {
	eui64, err := normalizeEUI64(eui)
	if err != nil {
		return err
	}
	raw, err := normalizeInstallCode(installCode)
	if err != nil {
		return err
	}

	tc.mu.Lock()
	configuredHash := tc.hash
	forbidden := bytes.Clone(tc.defaultKey)
	tc.mu.Unlock()

	if keysEqual(raw, DefaultGlobalLinkKey) || keysEqual(raw, forbidden) {
		return ErrDefaultGlobalLinkKey
	}
	code, err := ParseInstallCode(raw)
	if err != nil {
		return err
	}

	var hash func([]byte) ([]byte, error)
	if len(hashes) > 1 {
		return fmt.Errorf("%w: Register accepts at most one hash function", ErrInvalidConfiguration)
	}
	if len(hashes) == 1 {
		hash, err = normalizeHash(hashes[0])
		if err != nil {
			return err
		}
	}
	if hash == nil {
		hash = configuredHash
	}

	if hash == nil {
		return ErrNoHashFunction
	}
	linkKey, err := hash(code)
	if err != nil {
		return fmt.Errorf("derive install-code link key: %w", err)
	}
	linkKey = bytes.Clone(linkKey)
	if len(linkKey) == 0 {
		return ErrInvalidLinkKey
	}
	if keysEqual(linkKey, forbidden) || IsDefaultGlobalLinkKey(linkKey) {
		return ErrDefaultGlobalLinkKey
	}

	tc.mu.Lock()
	tc.devices[eui64] = &deviceEntry{
		installCode: bytes.Clone(code),
		linkKey:     linkKey,
	}
	tc.mu.Unlock()
	return nil
}

// RegisterInstallCode is an alias for Register.
func (tc *TrustCenter) RegisterInstallCode(eui any, installCode any, hashes ...any) error {
	return tc.Register(eui, installCode, hashes...)
}

// RegisterDevice is an alias for Register.
func (tc *TrustCenter) RegisterDevice(eui any, installCode any, hashes ...any) error {
	return tc.Register(eui, installCode, hashes...)
}

// RegisterEUI64 is an alias for Register.
func (tc *TrustCenter) RegisterEUI64(eui any, installCode any, hashes ...any) error {
	return tc.Register(eui, installCode, hashes...)
}

// RegisterCode is an alias for Register.
func (tc *TrustCenter) RegisterCode(eui any, installCode any, hashes ...any) error {
	return tc.Register(eui, installCode, hashes...)
}

// AddDevice is an alias for Register.
func (tc *TrustCenter) AddDevice(eui any, installCode any, hashes ...any) error {
	return tc.Register(eui, installCode, hashes...)
}

// Enroll is an alias for Register.
func (tc *TrustCenter) Enroll(eui any, installCode any, hashes ...any) error {
	return tc.Register(eui, installCode, hashes...)
}

// EnrollDevice is an alias for Register.
func (tc *TrustCenter) EnrollDevice(eui any, installCode any, hashes ...any) error {
	return tc.Register(eui, installCode, hashes...)
}

// Join handles a join request. It accepts these forms:
//
//	Join(eui, installCode)
//	Join(eui, installCode, presentedLinkKey)
//	Join(JoinRequest)
//
// The first form returns the link key derived from the registered code.
// When a presented link key is supplied, it must match that derived key. A
// registered EUI64 can never authenticate with the default global key.
func (tc *TrustCenter) Join(args ...any) ([]byte, error) {
	req, hasLinkKey, err := parseJoinArguments(args)
	if err != nil {
		return nil, err
	}
	return tc.join(req.EUI64, req.InstallCode, req.LinkKey, hasLinkKey)
}

// AttemptJoin is a typed alias for the three-argument form of Join.
func (tc *TrustCenter) AttemptJoin(eui any, installCode any, linkKey []byte) ([]byte, error) {
	return tc.Join(eui, installCode, linkKey)
}

// JoinWithLinkKey is a typed alias for the three-argument form of Join.
func (tc *TrustCenter) JoinWithLinkKey(eui any, installCode any, linkKey []byte) ([]byte, error) {
	return tc.Join(eui, installCode, linkKey)
}

// RequestJoin is an alias for Join.
func (tc *TrustCenter) RequestJoin(args ...any) ([]byte, error) {
	return tc.Join(args...)
}

// HandleJoin processes a JoinRequest.
func (tc *TrustCenter) HandleJoin(req JoinRequest) ([]byte, error) {
	hasLinkKey := req.LinkKey != nil
	return tc.join(req.EUI64, req.InstallCode, req.LinkKey, hasLinkKey)
}

// ProcessJoinRequest is an alias for HandleJoin.
func (tc *TrustCenter) ProcessJoinRequest(req JoinRequest) ([]byte, error) {
	return tc.HandleJoin(req)
}

func parseJoinArguments(args []any) (JoinRequest, bool, error) {
	if len(args) == 0 {
		return JoinRequest{}, false, fmt.Errorf("%w: missing join request", ErrInvalidEUI64)
	}

	switch req := args[0].(type) {
	case JoinRequest:
		if len(args) != 1 {
			return JoinRequest{}, false, fmt.Errorf("%w: unexpected arguments after JoinRequest", ErrInvalidConfiguration)
		}
		return req, req.LinkKey != nil, nil
	case *JoinRequest:
		if req == nil {
			return JoinRequest{}, false, fmt.Errorf("%w: nil JoinRequest", ErrInvalidConfiguration)
		}
		if len(args) != 1 {
			return JoinRequest{}, false, fmt.Errorf("%w: unexpected arguments after JoinRequest", ErrInvalidConfiguration)
		}
		return *req, req.LinkKey != nil, nil
	}

	eui, err := normalizeEUI64(args[0])
	if err != nil {
		return JoinRequest{}, false, err
	}
	if len(args) < 2 || len(args) > 3 {
		return JoinRequest{}, false, fmt.Errorf("%w: Join expects EUI64, install code, and optional link key", ErrInvalidConfiguration)
	}

	raw, err := normalizeInstallCode(args[1])
	if err != nil {
		return JoinRequest{}, false, err
	}

	req := JoinRequest{EUI64: eui, InstallCode: raw}
	hasLinkKey := len(args) == 3 && args[2] != nil
	if hasLinkKey {
		key, err := normalizeLinkKey(args[2])
		if err != nil {
			return JoinRequest{}, false, err
		}
		req.LinkKey = key
	}
	return req, hasLinkKey, nil
}

func normalizeLinkKey(value any) ([]byte, error) {
	switch key := value.(type) {
	case nil:
		return nil, nil
	case []byte:
		return bytes.Clone(key), nil
	case [16]byte:
		return bytes.Clone(key[:]), nil
	case string:
		decoded, ok := decodeHexString(key)
		if !ok {
			return nil, ErrInvalidLinkKey
		}
		return decoded, nil
	default:
		return nil, fmt.Errorf("%w: unsupported link-key type %T", ErrInvalidLinkKey, value)
	}
}

func (tc *TrustCenter) join(eui EUI64, rawCode, presentedKey []byte, hasPresentedKey bool) ([]byte, error) {
	tc.mu.Lock()
	defer tc.mu.Unlock()

	entry := tc.devices[eui]
	if entry != nil && entry.locked {
		return nil, ErrDeviceLocked
	}

	if entry != nil && tc.isForbiddenDefaultKey(rawCode) {
		return nil, tc.recordFailure(entry, ErrDefaultGlobalLinkKey)
	}

	code, err := ParseInstallCode(rawCode)
	if err != nil {
		if entry != nil {
			return nil, tc.recordFailure(entry, err)
		}
		return nil, err
	}

	if entry == nil {
		return nil, ErrDeviceNotRegistered
	}
	if subtle.ConstantTimeCompare(code, entry.installCode) != 1 {
		return nil, tc.recordFailure(entry, ErrInstallCodeMismatch)
	}

	if hasPresentedKey && tc.isForbiddenDefaultKey(presentedKey) {
		return nil, tc.recordFailure(entry, ErrDefaultGlobalLinkKey)
	}
	if tc.isForbiddenDefaultKey(entry.linkKey) {
		return nil, tc.recordFailure(entry, ErrDefaultGlobalLinkKey)
	}
	if hasPresentedKey && subtle.ConstantTimeCompare(presentedKey, entry.linkKey) != 1 {
		return nil, tc.recordFailure(entry, ErrLinkKeyMismatch)
	}

	entry.failures = 0
	return bytes.Clone(entry.linkKey), nil
}

func (tc *TrustCenter) recordFailure(entry *deviceEntry, cause error) error {
	entry.failures++
	if entry.failures >= tc.maxFailures {
		entry.locked = true
		return &LockoutError{Cause: cause}
	}
	return cause
}

func (tc *TrustCenter) isForbiddenDefaultKey(key []byte) bool {
	return keysEqual(key, tc.defaultKey) || IsDefaultGlobalLinkKey(key)
}

// LockoutError is returned on the failure that reaches the configured
// limit. It unwraps to the underlying failure and also matches
// ErrDeviceLocked with errors.Is.
type LockoutError struct {
	Cause error
}

func (e *LockoutError) Error() string {
	if e.Cause == nil {
		return ErrDeviceLocked.Error()
	}
	return fmt.Sprintf("%s: %s", ErrDeviceLocked, e.Cause)
}

func (e *LockoutError) Unwrap() error {
	return e.Cause
}

func (e *LockoutError) Is(target error) bool {
	return target == ErrDeviceLocked || (e.Cause != nil && errors.Is(e.Cause, target))
}

// LinkKey returns the link key derived when the EUI64 was registered.
func (tc *TrustCenter) LinkKey(eui any) ([]byte, error) {
	eui64, err := normalizeEUI64(eui)
	if err != nil {
		return nil, err
	}

	tc.mu.Lock()
	defer tc.mu.Unlock()
	entry := tc.devices[eui64]
	if entry == nil {
		return nil, ErrDeviceNotRegistered
	}
	return bytes.Clone(entry.linkKey), nil
}

// Registered reports whether eui has a registered install code.
func (tc *TrustCenter) Registered(eui any) bool {
	eui64, err := normalizeEUI64(eui)
	if err != nil {
		return false
	}
	tc.mu.Lock()
	defer tc.mu.Unlock()
	_, ok := tc.devices[eui64]
	return ok
}

// Locked reports whether eui is currently locked.
func (tc *TrustCenter) Locked(eui any) bool {
	eui64, err := normalizeEUI64(eui)
	if err != nil {
		return false
	}
	tc.mu.Lock()
	defer tc.mu.Unlock()
	entry := tc.devices[eui64]
	return entry != nil && entry.locked
}

// IsLocked is an alias for Locked.
func (tc *TrustCenter) IsLocked(eui any) bool {
	return tc.Locked(eui)
}

// IsLockedOut is an alias for Locked.
func (tc *TrustCenter) IsLockedOut(eui any) bool {
	return tc.Locked(eui)
}

// LockedOut is an alias for Locked.
func (tc *TrustCenter) LockedOut(eui any) bool {
	return tc.Locked(eui)
}

// ConsecutiveFailures returns the current failure count for eui.
func (tc *TrustCenter) ConsecutiveFailures(eui any) int {
	eui64, err := normalizeEUI64(eui)
	if err != nil {
		return 0
	}
	tc.mu.Lock()
	defer tc.mu.Unlock()
	entry := tc.devices[eui64]
	if entry == nil {
		return 0
	}
	return entry.failures
}

// FailureCount is an alias for ConsecutiveFailures.
func (tc *TrustCenter) FailureCount(eui any) int {
	return tc.ConsecutiveFailures(eui)
}

// Status returns a snapshot of eui's registration and lockout state.
func (tc *TrustCenter) Status(eui any) (DeviceStatus, error) {
	eui64, err := normalizeEUI64(eui)
	if err != nil {
		return DeviceStatus{}, err
	}

	tc.mu.Lock()
	defer tc.mu.Unlock()
	status := DeviceStatus{EUI64: eui64, FailureLimit: tc.maxFailures}
	entry := tc.devices[eui64]
	if entry == nil {
		return status, nil
	}
	status.Registered = true
	status.Locked = entry.locked
	status.ConsecutiveFailures = entry.failures
	return status, nil
}

// Reset clears the failure count and lockout state for eui. It does not
// remove the registered install code.
func (tc *TrustCenter) Reset(eui any) error {
	eui64, err := normalizeEUI64(eui)
	if err != nil {
		return err
	}
	tc.mu.Lock()
	defer tc.mu.Unlock()
	entry := tc.devices[eui64]
	if entry == nil {
		return ErrDeviceNotRegistered
	}
	entry.failures = 0
	entry.locked = false
	return nil
}

// Unlock is an alias for Reset.
func (tc *TrustCenter) Unlock(eui any) error {
	return tc.Reset(eui)
}

// ResetFailures is an alias for Reset.
func (tc *TrustCenter) ResetFailures(eui any) error {
	return tc.Reset(eui)
}

// ResetLockout is an alias for Reset.
func (tc *TrustCenter) ResetLockout(eui any) error {
	return tc.Reset(eui)
}

// Unregister removes eui and its install code.
func (tc *TrustCenter) Unregister(eui any) error {
	eui64, err := normalizeEUI64(eui)
	if err != nil {
		return err
	}
	tc.mu.Lock()
	defer tc.mu.Unlock()
	if _, ok := tc.devices[eui64]; !ok {
		return ErrDeviceNotRegistered
	}
	delete(tc.devices, eui64)
	return nil
}

// RemoveDevice is an alias for Unregister.
func (tc *TrustCenter) RemoveDevice(eui any) error {
	return tc.Unregister(eui)
}

// ParseEUI64 parses a colon-, hyphen-, or space-separated hexadecimal
// EUI64, or an unseparated 16-digit hexadecimal value.
func ParseEUI64(value string) (EUI64, error) {
	normalized := strings.TrimSpace(value)
	normalized = strings.TrimPrefix(normalized, "0x")
	normalized = strings.TrimPrefix(normalized, "0X")
	normalized = strings.NewReplacer(":", "", "-", "", " ", "").Replace(normalized)
	if len(normalized) != 16 {
		return 0, fmt.Errorf("%w: expected 16 hexadecimal digits", ErrInvalidEUI64)
	}
	raw, err := hex.DecodeString(normalized)
	if err != nil {
		return 0, fmt.Errorf("%w: %v", ErrInvalidEUI64, err)
	}
	return EUI64FromBytes(raw)
}

// EUI64FromBytes converts an 8-byte EUI64 into its numeric form.
func EUI64FromBytes(value []byte) (EUI64, error) {
	if len(value) != 8 {
		return 0, fmt.Errorf("%w: expected 8 bytes, got %d", ErrInvalidEUI64, len(value))
	}
	return binary.BigEndian.Uint64(value), nil
}

// EUI64Bytes converts a numeric EUI64 into its 8-byte form.
func EUI64Bytes(eui EUI64) [8]byte {
	var raw [8]byte
	binary.BigEndian.PutUint64(raw[:], uint64(eui))
	return raw
}

// FormatEUI64 formats a numeric EUI64 as colon-separated hexadecimal.
func FormatEUI64(eui EUI64) string {
	raw := EUI64Bytes(eui)
	return fmt.Sprintf("%02X:%02X:%02X:%02X:%02X:%02X:%02X:%02X",
		raw[0], raw[1], raw[2], raw[3], raw[4], raw[5], raw[6], raw[7])
}

// StringEUI64 is an alias for FormatEUI64.
func StringEUI64(eui EUI64) string {
	return FormatEUI64(eui)
}

func normalizeEUI64(value any) (EUI64, error) {
	switch eui := value.(type) {
	case uint64:
		return eui, nil
	case uint:
		return EUI64(eui), nil
	case uint32:
		return EUI64(eui), nil
	case uint16:
		return EUI64(eui), nil
	case uint8:
		return EUI64(eui), nil
	case int:
		if eui < 0 {
			return 0, fmt.Errorf("%w: negative value", ErrInvalidEUI64)
		}
		return EUI64(eui), nil
	case int64:
		if eui < 0 {
			return 0, fmt.Errorf("%w: negative value", ErrInvalidEUI64)
		}
		return EUI64(eui), nil
	case int32:
		if eui < 0 {
			return 0, fmt.Errorf("%w: negative value", ErrInvalidEUI64)
		}
		return EUI64(eui), nil
	case [8]byte:
		return binary.BigEndian.Uint64(eui[:]), nil
	case []byte:
		return EUI64FromBytes(eui)
	case string:
		return ParseEUI64(eui)
	default:
		return 0, fmt.Errorf("%w: unsupported type %T", ErrInvalidEUI64, value)
	}
}

func normalizeInstallCode(value any) ([]byte, error) {
	switch code := value.(type) {
	case []byte:
		return bytes.Clone(code), nil
	case string:
		decoded, ok := decodeHexString(code)
		if !ok {
			return nil, fmt.Errorf("%w: install code string is not hexadecimal", ErrInvalidInstallCodeLength)
		}
		return decoded, nil
	default:
		return nil, fmt.Errorf("%w: unsupported install-code type %T", ErrInvalidInstallCodeLength, value)
	}
}

func decodeHexString(value string) ([]byte, bool) {
	normalized := strings.TrimSpace(value)
	normalized = strings.TrimPrefix(normalized, "0x")
	normalized = strings.TrimPrefix(normalized, "0X")
	normalized = strings.NewReplacer(":", "", "-", "", " ", "").Replace(normalized)
	if len(normalized) == 0 || len(normalized)%2 != 0 {
		return nil, false
	}
	decoded, err := hex.DecodeString(normalized)
	if err != nil {
		return nil, false
	}
	return decoded, true
}

func keysEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare(a, b) == 1
}
