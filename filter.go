package netsec

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
)

const (
	// DefaultThreshold is the maximum number of incomplete transactions
	// admitted without an anti-clogging token.
	DefaultThreshold = 32
	// DefaultKeyRotation is how often the anti-clogging HMAC key rotates.
	DefaultKeyRotation = 30 * time.Second
	// DefaultTokenTTL is how long a token remains usable.
	DefaultTokenTTL = 2 * time.Minute
	// DefaultTransactionTimeout is how long an incomplete transaction holds
	// an admission slot.
	DefaultTransactionTimeout = 2 * time.Minute

	defaultMaxTransactions = 1024
	maxRetainedKeys        = 16
	maxUsedTokens          = 1 << 16
)

// Config controls the SAE commit pre-filter. The zero value is usable.
type Config struct {
	// Threshold is the maximum number of incomplete SAE transactions
	// admitted without a token. Once it is reached, new commits receive an
	// anti-clogging challenge.
	Threshold int

	// KeyRotation is the anti-clogging HMAC key rotation interval.
	KeyRotation time.Duration

	// TokenTTL is the lifetime of an issued token. Tokens issued under a
	// retained key remain valid until this deadline or rotation eviction.
	TokenTTL time.Duration

	// TransactionTimeout bounds how long an admitted but incomplete
	// transaction occupies a slot.
	TransactionTimeout time.Duration

	// MaxTransactions bounds the transaction table. Zero selects a safe
	// default based on Threshold.
	MaxTransactions int

	// Now overrides the clock, primarily for tests.
	Now func() time.Time

	// Rand supplies key and token nonce entropy. Nil means crypto/rand.
	Rand io.Reader
}

func normalizeConfig(cfg Config) (Config, error) {
	if cfg.Threshold < 0 {
		return Config{}, errors.New("netsec: threshold must not be negative")
	}
	if cfg.Threshold == 0 {
		cfg.Threshold = DefaultThreshold
	}
	if cfg.KeyRotation < 0 {
		return Config{}, errors.New("netsec: key rotation must not be negative")
	}
	if cfg.KeyRotation == 0 {
		cfg.KeyRotation = DefaultKeyRotation
	}
	if cfg.TokenTTL < 0 {
		return Config{}, errors.New("netsec: token TTL must not be negative")
	}
	if cfg.TokenTTL == 0 {
		cfg.TokenTTL = 2 * cfg.KeyRotation
		if cfg.TokenTTL < DefaultTokenTTL {
			cfg.TokenTTL = DefaultTokenTTL
		}
	}
	if cfg.TransactionTimeout < 0 {
		return Config{}, errors.New("netsec: transaction timeout must not be negative")
	}
	if cfg.TransactionTimeout == 0 {
		cfg.TransactionTimeout = DefaultTransactionTimeout
	}
	if cfg.MaxTransactions < 0 {
		return Config{}, errors.New("netsec: max transactions must not be negative")
	}
	if cfg.MaxTransactions == 0 {
		cfg.MaxTransactions = cfg.Threshold * 8
		if cfg.MaxTransactions < defaultMaxTransactions {
			cfg.MaxTransactions = defaultMaxTransactions
		}
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Rand == nil {
		cfg.Rand = rand.Reader
	}
	return cfg, nil
}

// DefaultConfig returns the default filter settings.
func DefaultConfig() Config {
	cfg, _ := normalizeConfig(Config{})
	return cfg
}

type keyEntry struct {
	key       [sha256.Size]byte
	createdAt time.Time
	expires   time.Time
}

type transaction struct {
	group   GroupID
	digest  [sha256.Size]byte
	expires time.Time
}

// Filter is an SAE commit pre-filter. It must be called before the control
// plane performs any SAE curve operation.
type Filter struct {
	mu sync.Mutex

	cfg Config
	rng io.Reader

	keys       []keyEntry
	usedTokens map[tokenID]time.Time
	tx         map[[6]byte]*transaction
	incomplete int
}

// NewFilter creates an anti-clogging filter and its initial HMAC key.
func NewFilter(cfg Config) (*Filter, error) {
	cfg, err := normalizeConfig(cfg)
	if err != nil {
		return nil, err
	}
	f := &Filter{
		cfg:        cfg,
		rng:        cfg.Rand,
		usedTokens: make(map[tokenID]time.Time),
		tx:         make(map[[6]byte]*transaction),
	}
	if err := f.addKeyLocked(cfg.Now()); err != nil {
		return nil, fmt.Errorf("netsec: generate anti-clogging key: %w", err)
	}
	return f, nil
}

func (f *Filter) addKeyLocked(now time.Time) error {
	var entry keyEntry
	if _, err := io.ReadFull(f.rng, entry.key[:]); err != nil {
		return err
	}
	entry.createdAt = now
	entry.expires = now.Add(f.cfg.TokenTTL)
	f.keys = append(f.keys, entry)
	if len(f.keys) > maxRetainedKeys {
		f.keys = append(f.keys[:0], f.keys[len(f.keys)-maxRetainedKeys:]...)
	}
	return nil
}

func (f *Filter) ensureKeyLocked(now time.Time) error {
	live := f.keys[:0]
	for _, key := range f.keys {
		if key.expires.After(now) {
			live = append(live, key)
		}
	}
	f.keys = live

	if len(f.keys) != 0 {
		last := f.keys[len(f.keys)-1]
		if now.Sub(last.createdAt) < f.cfg.KeyRotation && now.Before(last.expires) {
			return nil
		}
	}
	return f.addKeyLocked(now)
}

func (f *Filter) expireLocked(now time.Time) {
	for station, tx := range f.tx {
		if !tx.expires.After(now) {
			delete(f.tx, station)
			if f.incomplete > 0 {
				f.incomplete--
			}
		}
	}
	for id, expires := range f.usedTokens {
		if !expires.After(now) {
			delete(f.usedTokens, id)
		}
	}
}

func hashField(dst io.Writer, field []byte) {
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(field)))
	_, _ = dst.Write(length[:])
	_, _ = dst.Write(field)
}

func commitDigest(c Commit) [sha256.Size]byte {
	hash := sha256.New()
	_, _ = hash.Write([]byte("WPA3-SAE commit v1\x00"))
	hash.Write([]byte{byte(c.Group >> 8), byte(c.Group)})
	hashField(hash, c.Scalar)
	hashField(hash, c.Element)
	var digest [sha256.Size]byte
	hash.Sum(digest[:0])
	return digest
}

// Action tells the Authentication frame handler what to do next.
type Action uint8

const (
	// ActionAccept means the commit passed the cheap front-end checks. The
	// caller may now perform the SAE curve operation.
	ActionAccept Action = iota
	// ActionChallenge means no curve work may be done. The caller must send
	// only the anti-clogging token in Result.Token.
	ActionChallenge
	// ActionReject means the frame is malformed, reflected, or invalid. The
	// caller should discard it.
	ActionReject
)

// Result is the decision returned for a commit.
type Result struct {
	Action Action

	// Token is set for ActionChallenge. The peer must retry the same commit
	// with Commit.Token set to this value.
	Token []byte

	// Duplicate is true when an already admitted commit was retransmitted.
	// The caller can resend its previous commit without repeating curve
	// work.
	Duplicate bool

	// Err explains a rejection. It may also accompany ActionChallenge when
	// an invalid or replayed token was supplied.
	Err error
}

func rejected(err error) Result {
	return Result{Action: ActionReject, Err: err}
}

// HandleCommit is the Authentication frame handler's pre-filter entry point.
//
// An accepted commit creates or refreshes one incomplete transaction. The
// caller must invoke Complete when the SAE exchange finishes or is aborted.
// Under overload, the filter returns ActionChallenge without validating or
// performing any curve operation; the handler should send only the token.
func (f *Filter) HandleCommit(station net.HardwareAddr, c Commit) Result {
	var stationKey [6]byte
	if len(station) != len(stationKey) {
		return rejected(ErrInvalidStation)
	}
	copy(stationKey[:], station)

	spec, err := lookupGroup(c.Group)
	if err != nil {
		return rejected(err)
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	now := f.cfg.Now()
	f.expireLocked(now)
	if err := f.ensureKeyLocked(now); err != nil {
		return rejected(fmt.Errorf("netsec: rotate anti-clogging key: %w", err))
	}

	if len(c.Token) != 0 {
		switch f.checkTokenLocked(stationKey, c.Group, c.Token, now) {
		case tokenInvalid:
			token, err := f.issueTokenLocked(stationKey, c.Group, now)
			if err != nil {
				return rejected(fmt.Errorf("netsec: issue anti-clogging token: %w", err))
			}
			return Result{Action: ActionChallenge, Token: token, Err: ErrInvalidToken}
		case tokenReplay:
			token, err := f.issueTokenLocked(stationKey, c.Group, now)
			if err != nil {
				return rejected(fmt.Errorf("netsec: issue anti-clogging token: %w", err))
			}
			return Result{Action: ActionChallenge, Token: token, Err: ErrTokenReplay}
		}

		if err := validateCommit(spec, c); err != nil {
			return rejected(err)
		}
		if _, exists := f.tx[stationKey]; !exists && len(f.tx) >= f.cfg.MaxTransactions {
			return rejected(ErrTooManyTransactions)
		}
		if err := f.markTokenUsedLocked(c.Token, now); err != nil {
			return rejected(err)
		}
		return f.admitLocked(stationKey, c, now)
	}

	digest := commitDigest(c)
	if tx := f.tx[stationKey]; tx != nil && tx.group == c.Group && tx.digest == digest {
		tx.expires = now.Add(f.cfg.TransactionTimeout)
		return Result{Action: ActionAccept, Duplicate: true}
	}

	if f.incomplete >= f.cfg.Threshold {
		token, err := f.issueTokenLocked(stationKey, c.Group, now)
		if err != nil {
			return rejected(fmt.Errorf("netsec: issue anti-clogging token: %w", err))
		}
		return Result{Action: ActionChallenge, Token: token}
	}

	if err := validateCommit(spec, c); err != nil {
		return rejected(err)
	}
	return f.admitLocked(stationKey, c, now)
}

func (f *Filter) admitLocked(station [6]byte, c Commit, now time.Time) Result {
	if _, exists := f.tx[station]; !exists {
		if len(f.tx) >= f.cfg.MaxTransactions {
			return rejected(ErrTooManyTransactions)
		}
		f.incomplete++
	}
	f.tx[station] = &transaction{
		group:   c.Group,
		digest:  commitDigest(c),
		expires: now.Add(f.cfg.TransactionTimeout),
	}
	return Result{Action: ActionAccept}
}

// Complete releases the admission slot held by station. It returns false if
// there was no incomplete transaction.
func (f *Filter) Complete(station net.HardwareAddr) bool {
	if len(station) != 6 {
		return false
	}
	var key [6]byte
	copy(key[:], station)

	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.tx[key]; !ok {
		return false
	}
	delete(f.tx, key)
	if f.incomplete > 0 {
		f.incomplete--
	}
	return true
}

// Incomplete returns the number of admission slots currently occupied.
func (f *Filter) Incomplete() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.expireLocked(f.cfg.Now())
	return f.incomplete
}

// Threshold returns the configured admission threshold.
func (f *Filter) Threshold() int {
	return f.cfg.Threshold
}
