package netsec

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sync"
)

const (
	// PackageVersion is the wire format version authenticated by the package
	// MAC. Receivers must reject unknown versions.
	PackageVersion uint8 = 1

	// MeasurementSize is the size of an AMD SEV launch measurement.
	MeasurementSize = sha256.Size

	// MinNonceSize is the minimum accepted attestation nonce length. Sixteen
	// bytes gives a birthday bound of 2^64 for accidentally repeated nonces;
	// operating systems should normally provide 32 random bytes.
	MinNonceSize = 16

	// MaxNonceSize bounds hostile input retained in replay state.
	MaxNonceSize = 4096

	// DefaultNonceSize is the recommended random nonce size.
	DefaultNonceSize = 32

	// MinKeySize is the minimum accepted MAC/wrapping key length.
	MinKeySize = 16

	// PolicyDebugBit is the AMD SEV guest-policy DEBUG flag (bit 19). If set,
	// the hypervisor can use the debug interface to decrypt guest memory, so
	// a launch with this bit set is never eligible for key release.
	PolicyDebugBit uint64 = 1 << 19
)

const (
	measurementDomain = "q0168/sev-launch-measurement/v1"
	macDomain         = "q0168/sev-injection-package/mac/v1"
	aadDomain         = "q0168/sev-injection-package/aad/v1"
	wrapInfo          = "q0168/sev-injection-package/key-wrap/v1"
)

var (
	// ErrInvalidConfig is returned when a Gate cannot be constructed safely.
	ErrInvalidConfig = errors.New("netsec: invalid gate configuration")

	// ErrInvalidRequest is returned for malformed release requests.
	ErrInvalidRequest = errors.New("netsec: invalid release request")

	// ErrGuestMismatch is returned when the guest identity is not the one
	// pinned in the Gate.
	ErrGuestMismatch = errors.New("netsec: guest identity mismatch")

	// ErrPolicyMismatch is returned when the attested policy differs from the
	// expected policy.
	ErrPolicyMismatch = errors.New("netsec: policy mismatch")

	// ErrDebugPolicy is returned when any policy word contains the AMD SEV
	// DEBUG bit.
	ErrDebugPolicy = errors.New("netsec: policy permits debugging")

	// ErrMeasurementMismatch is returned when the attested measurement is not
	// the value derived from the expected firmware digest and policy.
	ErrMeasurementMismatch = errors.New("netsec: launch measurement mismatch")

	// ErrNonceReused is returned when an attestation nonce was already used to
	// issue or redeem a package. A nonce is single-use in both directions.
	ErrNonceReused = errors.New("netsec: nonce already used")

	// ErrInvalidNonce is returned for missing, short, or all-zero nonces.
	ErrInvalidNonce = errors.New("netsec: invalid attestation nonce")

	// ErrNonceMismatch is returned when the destination guest asserts a
	// different nonce from the one authenticated in the package.
	ErrNonceMismatch = errors.New("netsec: package nonce does not match destination")

	// ErrMACMismatch is returned when package metadata, the ciphertext, or the
	// MAC itself has been altered.
	ErrMACMismatch = errors.New("netsec: package MAC mismatch")

	// ErrUnsupportedVersion is returned for packages with an unknown format
	// version.
	ErrUnsupportedVersion = errors.New("netsec: unsupported package version")

	// ErrInvalidCiphertext is returned when a package has no usable wrapped
	// key payload.
	ErrInvalidCiphertext = errors.New("netsec: invalid ciphertext")
)

// Config pins one guest's expected launch state and the service's keys.
//
// FirmwareDigest is the expected, independently obtained firmware measurement
// (normally a 32-byte SHA-256 digest). Policy is the expected 64-bit AMD SEV
// guest policy. Both MACKey and WrapKey are required and must be independent
// secrets: MACKey authenticates package metadata; WrapKey encrypts the disk
// key.
type Config struct {
	GuestID        string
	FirmwareDigest []byte
	Policy         uint64
	MACKey         []byte
	WrapKey        []byte
	// NonceStore is optional. If nil, an in-memory store is used. A
	// multi-process or restart-surviving service must supply a durable,
	// atomic implementation.
	NonceStore NonceStore
}

// GateConfig is an alias kept for callers that prefer an explicit name.
type GateConfig = Config

// ReleaseRequest is the state asserted by the remote guest before injection.
// DiskKey is the plaintext key that will be wrapped into the returned package.
type ReleaseRequest struct {
	GuestID     string
	Measurement []byte
	Policy      uint64
	Nonce       []byte
	DiskKey     []byte
}

// Request is an alias for ReleaseRequest.
type Request = ReleaseRequest

// Attestation is the launch state asserted by the destination guest. It is
// used by OpenAttested to ensure a package is not replayed onto another
// instance that happens to present a valid-looking package.
type Attestation struct {
	GuestID     string
	Measurement []byte
	Policy      uint64
	Nonce       []byte
}

// InjectionPackage is the only artifact emitted after a successful release.
// Ciphertext contains an AES-256-GCM nonce followed by the wrapped disk key.
// MAC authenticates Version, GuestID, Measurement, Policy, Nonce, and
// Ciphertext.
type InjectionPackage struct {
	Version     uint8
	GuestID     string
	Measurement []byte
	Policy      uint64
	Nonce       []byte
	Ciphertext  []byte
	MAC         []byte
}

// Package is an alias for InjectionPackage.
type Package = InjectionPackage

// NonceStore is the replay-protection boundary used for both package
// issuance and redemption. Claim must atomically mark namespace+nonce as used
// and return false if it was already present. Implementations must be safe for
// concurrent use and should persist state for as long as a nonce could
// plausibly be replayed.
type NonceStore interface {
	Claim(namespace string, nonce []byte) (bool, error)
}

// Gate authorizes injection packages for exactly one pinned guest launch
// state. It is safe for concurrent use.
type Gate struct {
	guestID        string
	firmwareDigest []byte
	policy         uint64
	macKey         []byte
	wrapKey        []byte
	nonceStore     NonceStore
}

// NewGate validates cfg and constructs a Gate.
func NewGate(cfg Config) (*Gate, error) {
	if cfg.GuestID == "" {
		return nil, fmt.Errorf("%w: empty guest identity", ErrInvalidConfig)
	}
	if len(cfg.FirmwareDigest) == 0 {
		return nil, fmt.Errorf("%w: empty firmware digest", ErrInvalidConfig)
	}
	if cfg.Policy&PolicyDebugBit != 0 {
		return nil, fmt.Errorf("%w: expected policy contains DEBUG bit", ErrDebugPolicy)
	}
	if len(cfg.MACKey) < MinKeySize {
		return nil, fmt.Errorf("%w: MAC key must be at least %d bytes", ErrInvalidConfig, MinKeySize)
	}
	if len(cfg.WrapKey) < MinKeySize {
		return nil, fmt.Errorf("%w: wrapping key must be at least %d bytes", ErrInvalidConfig, MinKeySize)
	}

	store := cfg.NonceStore
	if store == nil {
		store = newMemoryNonceStore()
	}

	return &Gate{
		guestID:        cfg.GuestID,
		firmwareDigest: cloneBytes(cfg.FirmwareDigest),
		policy:         cfg.Policy,
		macKey:         cloneBytes(cfg.MACKey),
		wrapKey:        cloneBytes(cfg.WrapKey),
		nonceStore:     store,
	}, nil
}

// New constructs a Gate. It is an alias for NewGate.
func New(cfg Config) (*Gate, error) {
	return NewGate(cfg)
}

// HasDebugBit reports whether policy contains AMD SEV's DEBUG bit.
func HasDebugBit(policy uint64) bool {
	return policy&PolicyDebugBit != 0
}

// PolicyHasDebug is an alias for HasDebugBit.
func PolicyHasDebug(policy uint64) bool {
	return HasDebugBit(policy)
}

// GenerateNonce returns a fresh 32-byte nonce from crypto/rand.
func GenerateNonce() ([]byte, error) {
	return GenerateNonceSize(DefaultNonceSize)
}

// GenerateNonceSize returns a fresh random nonce of the requested size.
func GenerateNonceSize(size int) ([]byte, error) {
	if size < MinNonceSize || size > MaxNonceSize {
		return nil, fmt.Errorf(
			"%w: size must be between %d and %d",
			ErrInvalidNonce,
			MinNonceSize,
			MaxNonceSize,
		)
	}

	nonce := make([]byte, size)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("netsec: generate nonce: %w", err)
	}
	return nonce, nil
}

// ExpectedMeasurement derives the only launch measurement accepted for the
// supplied firmware digest and policy word. The policy is encoded as a
// little-endian 64-bit value, matching AMD's policy representation.
func ExpectedMeasurement(firmwareDigest []byte, policy uint64) []byte {
	h := sha256.New()
	h.Write([]byte(measurementDomain))
	var digestLen [4]byte
	binary.LittleEndian.PutUint32(digestLen[:], uint32(len(firmwareDigest)))
	h.Write(digestLen[:])
	h.Write(firmwareDigest)

	var encodedPolicy [8]byte
	binary.LittleEndian.PutUint64(encodedPolicy[:], policy)
	h.Write(encodedPolicy[:])

	return h.Sum(nil)
}

// DeriveMeasurement is an alias for ExpectedMeasurement.
func DeriveMeasurement(firmwareDigest []byte, policy uint64) []byte {
	return ExpectedMeasurement(firmwareDigest, policy)
}

// Release verifies the asserted launch state and, only if every check passes,
// wraps req.DiskKey into a new single-use injection package.
//
// On any error the returned package is the zero value; in particular it has no
// ciphertext and no MAC. The nonce is not recorded as used until all
// non-cryptographic checks have passed.
func (g *Gate) Release(req ReleaseRequest) (InjectionPackage, error) {
	var noPackage InjectionPackage

	if len(req.DiskKey) == 0 {
		return noPackage, fmt.Errorf("%w: empty disk key", ErrInvalidRequest)
	}
	if err := g.validateAssertion(req.GuestID, req.Measurement, req.Policy, req.Nonce); err != nil {
		return noPackage, err
	}

	// Claim the nonce before any ciphertext exists. This makes concurrent
	// releases with the same nonce atomic. If encryption fails below, the
	// nonce stays burned: a failure must never leave a reusable package.
	if err := g.claimIssuedNonce(req.Nonce); err != nil {
		return noPackage, err
	}

	pkg := InjectionPackage{
		Version:     PackageVersion,
		GuestID:     req.GuestID,
		Measurement: cloneBytes(req.Measurement),
		Policy:      req.Policy,
		Nonce:       cloneBytes(req.Nonce),
	}

	ciphertext, err := g.wrapDiskKey(req.DiskKey, pkg)
	if err != nil {
		return noPackage, fmt.Errorf("netsec: wrap disk key: %w", err)
	}
	pkg.Ciphertext = ciphertext
	pkg.MAC = g.computeMAC(pkg)

	return pkg, nil
}

// Authorize is an alias for Release that returns nil on failure.
func (g *Gate) Authorize(req ReleaseRequest) (*InjectionPackage, error) {
	pkg, err := g.Release(req)
	if err != nil {
		return nil, err
	}
	return &pkg, nil
}

// Verify checks a package's version, launch state, nonce shape, ciphertext
// presence, and MAC. It does not consume the package nonce, so it is suitable
// for dry-run validation. It never decrypts or returns key material.
func (g *Gate) Verify(pkg InjectionPackage) error {
	if pkg.Version != PackageVersion {
		return fmt.Errorf("%w: got %d", ErrUnsupportedVersion, pkg.Version)
	}
	if err := g.validateAssertion(pkg.GuestID, pkg.Measurement, pkg.Policy, pkg.Nonce); err != nil {
		return err
	}
	if len(pkg.Ciphertext) == 0 {
		return ErrInvalidCiphertext
	}
	if len(pkg.MAC) != sha256.Size {
		return ErrMACMismatch
	}

	want := g.computeMAC(pkg)
	if !hmac.Equal(want, pkg.MAC) {
		return ErrMACMismatch
	}
	return nil
}

// Open authenticates the package, consumes its nonce for redemption, and only
// then decrypts the disk key. It returns no plaintext on any failure.
//
// Opening the same package twice, or presenting a second package with the same
// nonce to this Gate, fails with ErrNonceReused.
func (g *Gate) Open(pkg InjectionPackage) ([]byte, error) {
	return g.OpenAttested(pkg, Attestation{
		GuestID:     pkg.GuestID,
		Measurement: pkg.Measurement,
		Policy:      pkg.Policy,
		Nonce:       pkg.Nonce,
	})
}

// OpenAttested authenticates the package and also compares every package
// binding with the launch state asserted by the destination guest. A package
// for instance A therefore cannot be opened by instance B even when the
// package fields themselves are intact and correctly MACed.
func (g *Gate) OpenAttested(pkg InjectionPackage, destination Attestation) ([]byte, error) {
	if err := g.Verify(pkg); err != nil {
		return nil, err
	}
	if err := g.validateAssertion(
		destination.GuestID,
		destination.Measurement,
		destination.Policy,
		destination.Nonce,
	); err != nil {
		return nil, err
	}
	if !equalStrings(pkg.GuestID, destination.GuestID) {
		return nil, ErrGuestMismatch
	}
	if pkg.Policy != destination.Policy {
		return nil, ErrPolicyMismatch
	}
	if subtle.ConstantTimeCompare(pkg.Measurement, destination.Measurement) != 1 {
		return nil, ErrMeasurementMismatch
	}
	if subtle.ConstantTimeCompare(pkg.Nonce, destination.Nonce) != 1 {
		return nil, ErrNonceMismatch
	}
	if err := g.claimRedeemedNonce(pkg.Nonce); err != nil {
		return nil, err
	}

	diskKey, err := g.unwrapDiskKey(pkg)
	if err != nil {
		return nil, fmt.Errorf("netsec: unwrap disk key: %w", err)
	}
	return diskKey, nil
}

// Unseal is an alias for Open.
func (g *Gate) Unseal(pkg InjectionPackage) ([]byte, error) {
	return g.Open(pkg)
}

func (g *Gate) validateAssertion(guestID string, measurement []byte, policy uint64, nonce []byte) error {
	// Check the DEBUG bit before any equality comparison so a debug-mode
	// policy can never match, even if the operator accidentally configured it.
	if policy&PolicyDebugBit != 0 {
		return ErrDebugPolicy
	}
	if !equalStrings(g.guestID, guestID) {
		return ErrGuestMismatch
	}
	if policy != g.policy {
		return fmt.Errorf("%w: got policy 0x%x", ErrPolicyMismatch, policy)
	}
	if err := validateNonce(nonce); err != nil {
		return err
	}

	expected := ExpectedMeasurement(g.firmwareDigest, g.policy)
	if subtle.ConstantTimeCompare(expected, measurement) != 1 {
		return ErrMeasurementMismatch
	}
	return nil
}

func validateNonce(nonce []byte) error {
	if len(nonce) < MinNonceSize {
		return fmt.Errorf("%w: need at least %d bytes", ErrInvalidNonce, MinNonceSize)
	}
	if len(nonce) > MaxNonceSize {
		return fmt.Errorf("%w: need at most %d bytes", ErrInvalidNonce, MaxNonceSize)
	}

	var anyNonZero byte
	for _, b := range nonce {
		anyNonZero |= b
	}
	if anyNonZero == 0 {
		return fmt.Errorf("%w: all-zero nonce", ErrInvalidNonce)
	}
	return nil
}

func (g *Gate) claimIssuedNonce(nonce []byte) error {
	ok, err := g.nonceStore.Claim("issue", nonce)
	if err != nil {
		return fmt.Errorf("netsec: claim issuance nonce: %w", err)
	}
	if !ok {
		return ErrNonceReused
	}
	return nil
}

func (g *Gate) claimRedeemedNonce(nonce []byte) error {
	ok, err := g.nonceStore.Claim("redeem", nonce)
	if err != nil {
		return fmt.Errorf("netsec: claim redemption nonce: %w", err)
	}
	if !ok {
		return ErrNonceReused
	}
	return nil
}

type nonceStoreKey struct {
	namespace string
	digest    [sha256.Size]byte
}

type memoryNonceStore struct {
	mu   sync.Mutex
	used map[nonceStoreKey]struct{}
}

func newMemoryNonceStore() *memoryNonceStore {
	return &memoryNonceStore{used: make(map[nonceStoreKey]struct{})}
}

func (s *memoryNonceStore) Claim(namespace string, nonce []byte) (bool, error) {
	key := nonceStoreKey{namespace: namespace, digest: sha256.Sum256(nonce)}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.used[key]; ok {
		return false, nil
	}
	s.used[key] = struct{}{}
	return true, nil
}

func (g *Gate) wrapDiskKey(diskKey []byte, pkg InjectionPackage) ([]byte, error) {
	key, err := hkdf.Key(sha256.New, g.wrapKey, pkg.Nonce, wrapInfo, 32)
	if err != nil {
		return nil, err
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	aeadNonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, aeadNonce); err != nil {
		return nil, err
	}

	sealed := aead.Seal(nil, aeadNonce, diskKey, packageAAD(pkg))
	return append(aeadNonce, sealed...), nil
}

func (g *Gate) unwrapDiskKey(pkg InjectionPackage) ([]byte, error) {
	key, err := hkdf.Key(sha256.New, g.wrapKey, pkg.Nonce, wrapInfo, 32)
	if err != nil {
		return nil, err
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	if len(pkg.Ciphertext) <= aead.NonceSize() {
		return nil, ErrInvalidCiphertext
	}
	aeadNonce := pkg.Ciphertext[:aead.NonceSize()]
	payload := pkg.Ciphertext[aead.NonceSize():]

	return aead.Open(nil, aeadNonce, payload, packageAAD(pkg))
}

func (g *Gate) computeMAC(pkg InjectionPackage) []byte {
	mac := hmac.New(sha256.New, g.macKey)
	mac.Write(packageMACInput(pkg))
	return mac.Sum(nil)
}

// packageMACInput uses length-prefixed fields so that identity, measurement,
// and nonce bytes cannot be shifted between fields to preserve a MAC.
func packageMACInput(pkg InjectionPackage) []byte {
	out := make([]byte, 0, 256)
	out = append(out, macDomain...)
	out = append(out, pkg.Version)
	out = appendString(out, pkg.GuestID)
	out = appendBytes(out, pkg.Measurement)
	out = binary.LittleEndian.AppendUint64(out, pkg.Policy)
	out = appendBytes(out, pkg.Nonce)
	out = appendBytes(out, pkg.Ciphertext)
	return out
}

// packageAAD binds the wrapped key to the same launch state as the MAC.
func packageAAD(pkg InjectionPackage) []byte {
	out := make([]byte, 0, 192)
	out = append(out, aadDomain...)
	out = append(out, pkg.Version)
	out = appendString(out, pkg.GuestID)
	out = appendBytes(out, pkg.Measurement)
	out = binary.LittleEndian.AppendUint64(out, pkg.Policy)
	out = appendBytes(out, pkg.Nonce)
	return out
}

func appendString(dst []byte, value string) []byte {
	dst = binary.LittleEndian.AppendUint32(dst, uint32(len(value)))
	return append(dst, value...)
}

func appendBytes(dst, value []byte) []byte {
	dst = binary.LittleEndian.AppendUint32(dst, uint32(len(value)))
	return append(dst, value...)
}

func equalStrings(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func cloneBytes(in []byte) []byte {
	if in == nil {
		return nil
	}
	out := make([]byte, len(in))
	copy(out, in)
	return out
}
