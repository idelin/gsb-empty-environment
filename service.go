package netsec

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
)

const (
	// DefaultTTL is how long an issued pre-authorized code remains valid.
	DefaultTTL = 10 * time.Minute

	// DefaultCodeBytes is the amount of entropy in a generated
	// pre-authorized code before base64url encoding.
	DefaultCodeBytes = 32

	// DefaultTxCodeDigits is the length of a generated numeric transaction
	// code.
	DefaultTxCodeDigits = 6
)

var (
	// ErrInvalidGrant is wrapped by every redemption failure. Callers that
	// expose errors to wallets should map anything matching ErrInvalidGrant
	// to a generic OAuth invalid_grant response so that code existence,
	// expiry, and transaction-code mismatches are not distinguishable.
	ErrInvalidGrant = errors.New("netsec: invalid grant")

	ErrCodeNotFound        = errors.New("netsec: pre-authorized code not found")
	ErrCodeExpired         = errors.New("netsec: pre-authorized code expired")
	ErrCodeAlreadyRedeemed = errors.New("netsec: pre-authorized code already redeemed")
	ErrCodeExists          = errors.New("netsec: pre-authorized code already exists")

	ErrTxCodeRequired    = errors.New("netsec: transaction code required")
	ErrTxCodeMismatch    = errors.New("netsec: transaction code does not match")
	ErrTxCodeNotExpected = errors.New("netsec: transaction code supplied for a code that does not require one")
	ErrNoTxCodeBound     = errors.New("netsec: pre-authorized code has no bound transaction code")

	ErrRandomSource = errors.New("netsec: random source failed to produce usable values")
)

const (
	maxCodeGenerationAttempts = 32
	maxRandomReadsPerDigit    = 1024
)

// Config controls a Service. The zero value uses DefaultTTL, crypto/rand,
// time.Now, 32 bytes of pre-authorized-code entropy, and six transaction-code
// digits.
type Config struct {
	// TTL is the default lifetime of issued codes. Zero selects
	// DefaultTTL; a negative value disables expiry.
	TTL time.Duration

	// Now is the clock used for expiry checks. Nil selects time.Now.
	Now func() time.Time

	// Random is the entropy source used to generate codes. Nil selects
	// crypto/rand.Reader.
	Random io.Reader

	// CodeLength is the number of random bytes in a generated
	// pre-authorized code. Values less than or equal to zero select
	// DefaultCodeBytes.
	CodeLength int

	// TxCodeLength is the number of digits in a generated transaction
	// code. Values less than or equal to zero select DefaultTxCodeDigits.
	TxCodeLength int
}

// IssueOptions describes a pre-authorized code to issue.
type IssueOptions struct {
	// Code optionally supplies the pre-authorized code value. When empty, a
	// cryptographically random code is generated. Supplying a code is
	// intended for integrations that already own code generation.
	Code string

	// RequireTxCode makes redemption require the transaction code bound to
	// this pre-authorized code.
	RequireTxCode bool

	// TxCode is the transaction code bound to this pre-authorized code.
	// A non-empty value implies RequireTxCode. When RequireTxCode is true
	// and TxCode is empty, a numeric code is generated and returned in
	// IssuedCode.TxCode.
	TxCode string

	// TTL overrides the Service default. Zero uses the service default; a
	// negative value disables expiry for this code.
	TTL time.Duration
}

// IssuedCode is the result of issuing a pre-authorized code.
type IssuedCode struct {
	// Code is the opaque pre-authorized code delivered to the wallet.
	Code string

	// TxCode is the transaction code that must be delivered to the end user
	// over a separate channel. It is empty when TxCodeRequired is false.
	TxCode string

	// TxCodeRequired reports whether redemption requires TxCode.
	TxCodeRequired bool

	// ExpiresAt is the expiry time, or the zero time when the code does not
	// expire.
	ExpiresAt time.Time
}

// Service issues, redeems, and administers pre-authorized codes. Its zero
// value is ready for use, but a Service must not be copied after first use.
type Service struct {
	mu sync.Mutex

	codes map[string]*codeRecord

	ttl          time.Duration
	now          func() time.Time
	random       io.Reader
	codeLength   int
	txCodeLength int
}

// Issuer and Store are aliases for Service, matching the terminology used by
// issuance services and their persistence layers.
type (
	Issuer = Service
	Store  = Service
)

type codeRecord struct {
	txCode         string
	requiresTxCode bool
	redeemed       bool
	expiresAt      time.Time
}

// NewService creates an empty Service.
func NewService(configs ...Config) *Service {
	s := &Service{
		codes:        make(map[string]*codeRecord),
		ttl:          DefaultTTL,
		now:          time.Now,
		random:       rand.Reader,
		codeLength:   DefaultCodeBytes,
		txCodeLength: DefaultTxCodeDigits,
	}

	if len(configs) > 0 {
		cfg := configs[0]
		if cfg.TTL != 0 {
			s.ttl = cfg.TTL
		}
		if cfg.Now != nil {
			s.now = cfg.Now
		}
		if cfg.Random != nil {
			s.random = cfg.Random
		}
		if cfg.CodeLength > 0 {
			s.codeLength = cfg.CodeLength
		}
		if cfg.TxCodeLength > 0 {
			s.txCodeLength = cfg.TxCodeLength
		}
	}

	return s
}

// NewIssuer is an alias for NewService.
func NewIssuer(configs ...Config) *Issuer {
	return NewService(configs...)
}

// NewStore is an alias for NewService.
func NewStore(configs ...Config) *Store {
	return NewService(configs...)
}

// New is an alias for NewService.
func New(configs ...Config) *Service {
	return NewService(configs...)
}

// Issue creates a new single-use pre-authorized code.
func (s *Service) Issue(opts IssueOptions) (*IssuedCode, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.initLocked()

	if opts.TxCode != "" {
		// A bound value is meaningless unless redemption checks it. Treat
		// a non-empty TxCode as requiring the transaction code.
		opts.RequireTxCode = true
	}

	txCode := opts.TxCode
	if opts.RequireTxCode && txCode == "" {
		generated, err := generateDigits(s.random, s.txCodeLength)
		if err != nil {
			return nil, fmt.Errorf("netsec: generate transaction code: %w", err)
		}
		txCode = generated
	}

	ttl := opts.TTL
	if ttl == 0 {
		ttl = s.ttl
	}

	var expiresAt time.Time
	if ttl > 0 {
		expiresAt = s.now().Add(ttl)
	}

	code := opts.Code
	if code == "" {
		for attempt := 0; attempt < maxCodeGenerationAttempts; attempt++ {
			generated, err := generateCode(s.random, s.codeLength)
			if err != nil {
				return nil, fmt.Errorf("netsec: generate pre-authorized code: %w", err)
			}
			if _, exists := s.codes[generated]; !exists {
				code = generated
				break
			}
		}
		if code == "" {
			return nil, ErrRandomSource
		}
	} else if _, exists := s.codes[code]; exists {
		return nil, ErrCodeExists
	}

	s.codes[code] = &codeRecord{
		txCode:         txCode,
		requiresTxCode: opts.RequireTxCode,
		expiresAt:      expiresAt,
	}

	return &IssuedCode{
		Code:           code,
		TxCode:         txCode,
		TxCodeRequired: opts.RequireTxCode,
		ExpiresAt:      expiresAt,
	}, nil
}

// IssueWithTxCode issues a code and returns only its pre-authorized code. A
// non-empty txCode makes the transaction code mandatory; an empty txCode
// creates a code that can be redeemed directly. Because this method does not
// return a generated transaction code, tx_code generation is not performed:
// pass the value that was delivered to the end user.
func (s *Service) IssueWithTxCode(txCode string) (string, error) {
	issued, err := s.Issue(IssueOptions{
		RequireTxCode: txCode != "",
		TxCode:        txCode,
	})
	if err != nil {
		return "", err
	}

	return issued.Code, nil
}

// IssueCode is an alias for IssueWithTxCode.
func (s *Service) IssueCode(txCode string) (string, error) {
	return s.IssueWithTxCode(txCode)
}

// IssuePreAuthorizedCode issues a code using explicit requirement semantics.
// A required transaction code must be supplied by the caller so that it can be
// delivered over a separate channel.
func (s *Service) IssuePreAuthorizedCode(requireTxCode bool, txCode string) (string, error) {
	if requireTxCode && txCode == "" {
		return "", ErrTxCodeRequired
	}
	if !requireTxCode && txCode != "" {
		return "", ErrTxCodeNotExpected
	}

	return s.IssueWithTxCode(txCode)
}

// IssuePreAuthCode is an alias for IssuePreAuthorizedCode.
func (s *Service) IssuePreAuthCode(requireTxCode bool, txCode string) (string, error) {
	return s.IssuePreAuthorizedCode(requireTxCode, txCode)
}

// Redeem validates and consumes a pre-authorized code. A code that requires a
// transaction code is redeemed only when presented with the exact value bound
// to that code. A code that does not require one can be redeemed directly; any
// supplied transaction code is ignored for such codes.
//
// Validation failures do not consume the code, so the legitimate holder may
// retry. The successful redemption is committed atomically, which guarantees
// that concurrent callers cannot redeem the same code twice.
func (s *Service) Redeem(code, txCode string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.initLocked()

	record, ok := s.codes[code]
	if !ok {
		return invalidGrant(ErrCodeNotFound)
	}
	if record.redeemed {
		return invalidGrant(ErrCodeAlreadyRedeemed)
	}
	if record.expired(s.now()) {
		return invalidGrant(ErrCodeExpired)
	}
	if record.requiresTxCode {
		if err := verifyBoundTxCode(record.txCode, txCode); err != nil {
			return invalidGrant(err)
		}
	}

	record.redeemed = true
	return nil
}

// RedeemPreAuthorizedCode is an alias for Redeem.
func (s *Service) RedeemPreAuthorizedCode(code, txCode string) error {
	return s.Redeem(code, txCode)
}

// RedeemPreAuthCode is an alias for Redeem.
func (s *Service) RedeemPreAuthCode(code, txCode string) error {
	return s.Redeem(code, txCode)
}

// SwapTxCodes atomically exchanges the transaction codes bound to two active
// pre-authorized codes. Both codes must exist, be unused, be unexpired, and
// require a transaction code. If any check fails, neither binding is changed.
//
// This is an administrative issuance-service operation. It is not part of the
// wallet-facing redemption flow.
func (s *Service) SwapTxCodes(codeA, codeB string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.initLocked()

	a, ok := s.codes[codeA]
	if !ok {
		return ErrCodeNotFound
	}
	b, ok := s.codes[codeB]
	if !ok {
		return ErrCodeNotFound
	}

	now := s.now()
	if a.redeemed || b.redeemed {
		return ErrCodeAlreadyRedeemed
	}
	if a.expired(now) || b.expired(now) {
		return ErrCodeExpired
	}
	if !a.requiresTxCode || !b.requiresTxCode {
		return ErrNoTxCodeBound
	}
	if codeA == codeB {
		return nil
	}

	a.txCode, b.txCode = b.txCode, a.txCode
	return nil
}

// SwapTxCode is an alias for SwapTxCodes.
func (s *Service) SwapTxCode(codeA, codeB string) error {
	return s.SwapTxCodes(codeA, codeB)
}

// VerifyTxCode checks a presented transaction code against the value bound to
// a pre-authorized code. It is useful when an issuance service stores bindings
// outside this package. When required is false, the function succeeds.
func VerifyTxCode(required bool, boundTxCode, presentedTxCode string) error {
	if !required {
		return nil
	}

	return verifyBoundTxCode(boundTxCode, presentedTxCode)
}

// GenerateTxCode returns a cryptographically random numeric transaction code.
// Values less than or equal to zero select DefaultTxCodeDigits.
func GenerateTxCode(digits int) (string, error) {
	if digits <= 0 {
		digits = DefaultTxCodeDigits
	}

	return generateDigits(rand.Reader, digits)
}

func (s *Service) initLocked() {
	if s.codes == nil {
		s.codes = make(map[string]*codeRecord)
	}
	if s.ttl == 0 {
		s.ttl = DefaultTTL
	}
	if s.now == nil {
		s.now = time.Now
	}
	if s.random == nil {
		s.random = rand.Reader
	}
	if s.codeLength <= 0 {
		s.codeLength = DefaultCodeBytes
	}
	if s.txCodeLength <= 0 {
		s.txCodeLength = DefaultTxCodeDigits
	}
}

func (r *codeRecord) expired(now time.Time) bool {
	return !r.expiresAt.IsZero() && !now.Before(r.expiresAt)
}

func verifyBoundTxCode(bound, presented string) error {
	if bound == "" {
		return ErrNoTxCodeBound
	}
	if presented == "" {
		return ErrTxCodeRequired
	}

	// Hash both values so the comparison takes the same time regardless of
	// the two input lengths.
	boundHash := sha256.Sum256([]byte(bound))
	presentedHash := sha256.Sum256([]byte(presented))
	if subtle.ConstantTimeCompare(boundHash[:], presentedHash[:]) != 1 {
		return ErrTxCodeMismatch
	}

	return nil
}

func invalidGrant(err error) error {
	return fmt.Errorf("%w: %w", ErrInvalidGrant, err)
}

func generateCode(r io.Reader, length int) (string, error) {
	if length <= 0 {
		length = DefaultCodeBytes
	}

	buf := make([]byte, length)
	if _, err := io.ReadFull(r, buf); err != nil {
		return "", err
	}

	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func generateDigits(r io.Reader, digits int) (string, error) {
	if digits <= 0 {
		digits = DefaultTxCodeDigits
	}

	out := make([]byte, digits)
	var b [1]byte
	attempts := 0
	for i := 0; i < digits; {
		if attempts >= digits*maxRandomReadsPerDigit {
			return "", ErrRandomSource
		}
		attempts++

		if _, err := io.ReadFull(r, b[:]); err != nil {
			return "", err
		}
		// Reject the top six byte values so every digit is uniformly
		// distributed (250 is a multiple of 10).
		if b[0] >= 250 {
			continue
		}
		out[i] = '0' + b[0]%10
		i++
	}

	return string(out), nil
}

var (
	defaultMu      sync.RWMutex
	defaultService = NewService()
)

// Default returns the process-wide Service used by the package-level
// convenience functions.
func Default() *Service {
	defaultMu.RLock()
	defer defaultMu.RUnlock()

	return defaultService
}

// Reset replaces the process-wide Service with an empty one. It is intended
// for tests and for services that need to discard all in-memory state.
func Reset() {
	defaultMu.Lock()
	defaultService = NewService()
	defaultMu.Unlock()
}

// Issue is a package-level convenience wrapper around
// Default().IssueWithTxCode.
func Issue(txCode string) (string, error) {
	return Default().IssueWithTxCode(txCode)
}

// IssueWithTxCode is a package-level convenience wrapper.
func IssueWithTxCode(txCode string) (string, error) {
	return Default().IssueWithTxCode(txCode)
}

// IssuePreAuthorizedCode is a package-level convenience wrapper.
func IssuePreAuthorizedCode(requireTxCode bool, txCode string) (string, error) {
	return Default().IssuePreAuthorizedCode(requireTxCode, txCode)
}

// IssuePreAuthCode is a package-level convenience wrapper.
func IssuePreAuthCode(requireTxCode bool, txCode string) (string, error) {
	return Default().IssuePreAuthorizedCode(requireTxCode, txCode)
}

// Redeem is a package-level convenience wrapper.
func Redeem(code, txCode string) error {
	return Default().Redeem(code, txCode)
}

// RedeemPreAuthorizedCode is a package-level convenience wrapper.
func RedeemPreAuthorizedCode(code, txCode string) error {
	return Default().Redeem(code, txCode)
}

// RedeemPreAuthCode is a package-level convenience wrapper.
func RedeemPreAuthCode(code, txCode string) error {
	return Default().Redeem(code, txCode)
}

// SwapTxCodes is a package-level convenience wrapper.
func SwapTxCodes(codeA, codeB string) error {
	return Default().SwapTxCodes(codeA, codeB)
}

// SwapTxCode is a package-level convenience wrapper.
func SwapTxCode(codeA, codeB string) error {
	return Default().SwapTxCodes(codeA, codeB)
}
