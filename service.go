package netsec

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"
)

// Config tunes the verification code policy. Zero values fall back to the
// defaults documented on each field.
type Config struct {
	StepSeconds        int64         // time-step size for code validity (default 300)
	StepSkew           int64         // extra past steps accepted (default 1)
	ResendInterval     time.Duration // minimal interval between sends (default 60s)
	SendWindow         time.Duration // rate-limit window for sends (default 1h)
	SendMaxPerWindow   int           // max sends per phone+purpose per window (default 5)
	MaxVerifyAttempts  int           // wrong-code attempts before invalidation (default 5)
	RebindWindow       time.Duration // rate-limit window for phone rebinds (default 24h)
	RebindMaxPerWindow int           // max rebinds per account per window (default 3)
}

func (c Config) withDefaults() Config {
	if c.StepSeconds <= 0 {
		c.StepSeconds = 300
	}
	if c.StepSkew <= 0 {
		c.StepSkew = 1
	}
	if c.ResendInterval <= 0 {
		c.ResendInterval = time.Minute
	}
	if c.SendWindow <= 0 {
		c.SendWindow = time.Hour
	}
	if c.SendMaxPerWindow <= 0 {
		c.SendMaxPerWindow = 5
	}
	if c.MaxVerifyAttempts <= 0 {
		c.MaxVerifyAttempts = 5
	}
	if c.RebindWindow <= 0 {
		c.RebindWindow = 24 * time.Hour
	}
	if c.RebindMaxPerWindow <= 0 {
		c.RebindMaxPerWindow = 3
	}
	return c
}

// SendResult describes an issued code. Code is returned so the caller (the
// SMS dispatch layer) can deliver it; it is never stored in plaintext.
type SendResult struct {
	BatchID   string
	Step      int64
	Code      string
	ExpiresAt time.Time
}

// Service issues and consumes verification codes. All gateways of a
// deployment must share one Service (or at least one Store) so consumption
// is atomic across gateways.
type Service struct {
	cfg     Config
	store   Store
	now     func() time.Time
	genCode func() (string, error)
}

type Option func(*Service)

// WithClock overrides the time source, mainly for tests.
func WithClock(now func() time.Time) Option {
	return func(s *Service) { s.now = now }
}

// WithCodeGenerator overrides the code generator, mainly for tests.
func WithCodeGenerator(gen func() (string, error)) Option {
	return func(s *Service) { s.genCode = gen }
}

func NewService(store Store, cfg Config, opts ...Option) *Service {
	s := &Service{
		cfg:     cfg.withDefaults(),
		store:   store,
		now:     time.Now,
		genCode: randomCode,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Gateway returns a named gateway facade over this service. Gateways share
// the underlying store, so a code consumed via one gateway is consumed for
// all of them.
func (s *Service) Gateway(name string) *Gateway {
	return &Gateway{name: name, svc: s}
}

func (s *Service) stepOf(t time.Time) int64 {
	return t.Unix() / s.cfg.StepSeconds
}

func (s *Service) sendWindowOf(t time.Time) int64 {
	return t.Unix() / int64(s.cfg.SendWindow/time.Second)
}

func (s *Service) rebindWindowOf(t time.Time) int64 {
	return t.Unix() / int64(s.cfg.RebindWindow/time.Second)
}

func hashCode(phone, purpose, code string) string {
	sum := sha256.Sum256([]byte(phone + "\x00" + purpose + "\x00" + code))
	return hex.EncodeToString(sum[:])
}

func randomCode() (string, error) {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	n := int(b[0])<<24 | int(b[1])<<16 | int(b[2])<<8 | int(b[3])
	return fmt.Sprintf("%06d", n%1000000), nil
}

func randomBatchID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// Send issues a new code bound to (phone, purpose), the current send batch
// and the current time step. It enforces the resend interval and the
// per-window send limit atomically in the shared store. A new send supersedes
// any previously issued code for the same (phone, purpose).
func (s *Service) Send(phone, purpose string) (SendResult, error) {
	now := s.now()
	key := "send\x00" + phone + "\x00" + purpose
	status, err := s.store.AcquireSendSlot(key, now, s.cfg.ResendInterval, s.sendWindowOf(now), s.cfg.SendMaxPerWindow)
	if err != nil {
		return SendResult{}, err
	}
	switch status {
	case SendTooFrequent:
		return SendResult{}, ErrSendTooFrequent
	case SendLimited:
		return SendResult{}, ErrSendLimitExceeded
	}
	code, err := s.genCode()
	if err != nil {
		return SendResult{}, err
	}
	batchID, err := randomBatchID()
	if err != nil {
		return SendResult{}, err
	}
	step := s.stepOf(now)
	rec := CodeRecord{
		Phone:    phone,
		Purpose:  purpose,
		BatchID:  batchID,
		CodeHash: hashCode(phone, purpose, code),
		Step:     step,
	}
	if err := s.store.PutCode(rec); err != nil {
		return SendResult{}, err
	}
	return SendResult{
		BatchID:   batchID,
		Step:      step,
		Code:      code,
		ExpiresAt: time.Unix((step+s.cfg.StepSkew+1)*s.cfg.StepSeconds, 0),
	}, nil
}

// Verify consumes the code bound to (phone, purpose). The check-and-consume
// is one atomic store operation: across gateways and goroutines exactly one
// caller can succeed for a given code.
func (s *Service) Verify(phone, purpose, code string) error {
	status, err := s.store.ConsumeCode(phone, purpose, hashCode(phone, purpose, code),
		s.stepOf(s.now()), s.cfg.StepSkew, s.cfg.MaxVerifyAttempts)
	if err != nil {
		return err
	}
	switch status {
	case ConsumeOK:
		return nil
	case ConsumeNotFound:
		return ErrCodeNotFound
	case ConsumeAlreadyConsumed:
		return ErrCodeConsumed
	case ConsumeExpired:
		return ErrCodeExpired
	case ConsumeMismatch:
		return ErrCodeMismatch
	case ConsumeLocked:
		return ErrTooManyAttempts
	default:
		return ErrCodeNotFound
	}
}

// Rebind changes the phone bound to an account. It is rate-limited per
// account and invalidates every outstanding code issued to the old phone, so
// codes captured before the rebind cannot be replayed afterwards.
func (s *Service) Rebind(account, oldPhone, newPhone string) error {
	ok, err := s.store.AcquireRebindSlot(account, s.rebindWindowOf(s.now()), s.cfg.RebindMaxPerWindow)
	if err != nil {
		return err
	}
	if !ok {
		return ErrRebindLimitExceeded
	}
	if err := s.store.SetBinding(account, newPhone); err != nil {
		return err
	}
	return s.store.DeleteCodesForPhone(oldPhone)
}

// Gateway is a named facade over a shared Service. Deploy one Gateway value
// per SMS gateway; consumption stays atomic because all gateways share the
// same Store.
type Gateway struct {
	name string
	svc  *Service
}

func (g *Gateway) Name() string { return g.name }

func (g *Gateway) Send(phone, purpose string) (SendResult, error) {
	return g.svc.Send(phone, purpose)
}

func (g *Gateway) Verify(phone, purpose, code string) error {
	return g.svc.Verify(phone, purpose, code)
}

func (g *Gateway) Rebind(account, oldPhone, newPhone string) error {
	return g.svc.Rebind(account, oldPhone, newPhone)
}
