package netsec

import (
	"math"
	"sync"
)

// OpenFunc authenticates and decrypts a message with a one-time message key.
// It must return an error if authentication fails.
type OpenFunc func(messageKey, nonce, ciphertext, additionalData []byte) ([]byte, error)

// UseKeyFunc receives a one-time message key. Returning an error aborts the
// receive operation without advancing any chain state. The key is zeroed on
// return, so implementations that need to retain it must copy it.
type UseKeyFunc func(messageKey []byte) error

// ReceiveChain is the receiving side of one symmetric Double Ratchet message
// chain. It derives message keys lazily from the current chain key and keeps
// skipped keys for out-of-order delivery.
//
// All state changes are transactional: key derivation and AEAD authentication
// happen against local state, and the chain key, next sequence number, and
// skipped-key cache are committed together only after authentication
// succeeds.
type ReceiveChain struct {
	mu sync.Mutex

	ratchetPublicKey []byte
	chainKey         []byte
	nextSequence     uint64
	maxSkippedKeys   int
	skippedKeys      *SkippedKeyStore
}

type receiveChainConfig struct {
	maxSkippedKeys int
	skippedKeys    *SkippedKeyStore
}

// ReceiveChainOption configures a ReceiveChain.
type ReceiveChainOption func(*receiveChainConfig) error

// Option is an alias for ReceiveChainOption.
type Option = ReceiveChainOption

// WithMaxSkippedKeys lowers the per-chain skip limit. Values must be in
// [0, MaxSkippedKeysPerChain]; the hard maximum cannot be raised.
func WithMaxSkippedKeys(maxSkippedKeys int) ReceiveChainOption {
	return func(config *receiveChainConfig) error {
		if maxSkippedKeys < 0 || maxSkippedKeys > MaxSkippedKeysPerChain {
			return ErrInvalidMaxSkippedKeys
		}
		config.maxSkippedKeys = maxSkippedKeys
		return nil
	}
}

// WithMaxSkip is an alias for WithMaxSkippedKeys.
func WithMaxSkip(maxSkippedKeys int) ReceiveChainOption {
	return WithMaxSkippedKeys(maxSkippedKeys)
}

// WithSkippedKeyStore supplies a store shared by several ratchet chains.
// Skipped keys remain namespaced by ratchet public key and sequence number.
func WithSkippedKeyStore(store *SkippedKeyStore) ReceiveChainOption {
	return func(config *receiveChainConfig) error {
		if store == nil {
			return ErrNilStore
		}
		config.skippedKeys = store
		return nil
	}
}

// NewReceiveChain creates a receive chain starting at sequence number zero.
func NewReceiveChain(ratchetPublicKey, chainKey []byte, options ...ReceiveChainOption) (*ReceiveChain, error) {
	if len(ratchetPublicKey) == 0 {
		return nil, ErrInvalidRatchetKey
	}
	if err := ValidateChainKey(chainKey); err != nil {
		return nil, err
	}

	config := receiveChainConfig{
		maxSkippedKeys: MaxSkippedKeysPerChain,
		skippedKeys:    NewSkippedKeyStore(),
	}
	for _, option := range options {
		if option == nil {
			continue
		}
		if err := option(&config); err != nil {
			return nil, err
		}
	}

	return &ReceiveChain{
		ratchetPublicKey: cloneBytes(ratchetPublicKey),
		chainKey:         cloneBytes(chainKey),
		maxSkippedKeys:   config.maxSkippedKeys,
		skippedKeys:      config.skippedKeys,
	}, nil
}

// MustNewReceiveChain is like NewReceiveChain but panics on invalid input.
func MustNewReceiveChain(ratchetPublicKey, chainKey []byte, options ...ReceiveChainOption) *ReceiveChain {
	chain, err := NewReceiveChain(ratchetPublicKey, chainKey, options...)
	if err != nil {
		panic(err)
	}
	return chain
}

// New is a shorthand for NewReceiveChain.
func New(ratchetPublicKey, chainKey []byte, options ...ReceiveChainOption) (*ReceiveChain, error) {
	return NewReceiveChain(ratchetPublicKey, chainKey, options...)
}

// NewChain is a shorthand for NewReceiveChain.
func NewChain(ratchetPublicKey, chainKey []byte, options ...ReceiveChainOption) (*ReceiveChain, error) {
	return NewReceiveChain(ratchetPublicKey, chainKey, options...)
}

// Chain is an alias for ReceiveChain.
type Chain = ReceiveChain

// RatchetPublicKey returns a copy of the ratchet public key bound to this
// chain.
func (c *ReceiveChain) RatchetPublicKey() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return cloneBytes(c.ratchetPublicKey)
}

// NextSequence returns the sequence number of the next message that would
// consume the head of the chain.
func (c *ReceiveChain) NextSequence() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.nextSequence
}

// MaxSkippedKeys returns the configured per-chain skip limit.
func (c *ReceiveChain) MaxSkippedKeys() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.maxSkippedKeys
}

// SkippedKeyCount returns the number of retained keys for this chain's
// ratchet public key.
func (c *ReceiveChain) SkippedKeyCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.skippedKeys.count(c.ratchetPublicKey)
}

// HasSkippedKey reports whether an unconsumed key exists for sequence.
func (c *ReceiveChain) HasSkippedKey(sequence any) bool {
	normalized, err := NormalizeSequence(sequence)
	if err != nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.skippedKeys.Contains(SkippedKeyID{
		RatchetPublicKey: c.ratchetPublicKey,
		Sequence:         normalized,
	})
}

// SkippedKeys exposes the store used by this chain. It is primarily useful
// for inspection and for sharing keys with another chain object.
func (c *ReceiveChain) SkippedKeys() *SkippedKeyStore {
	return c.skippedKeys
}

// UseMessageKey obtains the one-time message key for sequence and calls use.
// Chain state is committed only if use returns nil.
//
// The operation is bounded: if advancing the chain would skip more than the
// configured limit, ErrSkipLimitExceeded is returned before any derivation
// work is performed. A sequence number below the current head is accepted
// only when its previously skipped key is still retained.
func (c *ReceiveChain) UseMessageKey(sequence any, use UseKeyFunc) error {
	if use == nil {
		return ErrNilOpener
	}
	normalized, err := NormalizeSequence(sequence)
	if err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if normalized < c.nextSequence {
		return c.skippedKeys.use(SkippedKeyID{
			RatchetPublicKey: c.ratchetPublicKey,
			Sequence:         normalized,
		}, use)
	}

	skipCount := normalized - c.nextSequence
	if skipCount > uint64(c.maxSkippedKeys) {
		return ErrSkipLimitExceeded
	}
	if normalized == math.MaxUint64 {
		return ErrSequenceOverflow
	}
	if c.skippedKeys.count(c.ratchetPublicKey)+int(skipCount) > c.maxSkippedKeys {
		return ErrSkipLimitExceeded
	}

	skipped := make([]skippedKeyEntry, 0, int(skipCount))
	deriveChainKey := cloneBytes(c.chainKey)

	for next := c.nextSequence; next < normalized; next++ {
		messageKey, nextChainKey := StepChainKey(deriveChainKey)
		clearBytes(deriveChainKey)
		skipped = append(skipped, skippedKeyEntry{
			sequence:   next,
			messageKey: messageKey,
		})
		deriveChainKey = nextChainKey
	}

	messageKey, nextChainKey := StepChainKey(deriveChainKey)
	clearBytes(deriveChainKey)

	err = use(messageKey)
	clearBytes(messageKey)
	if err != nil {
		discardSkippedKeys(skipped)
		clearBytes(nextChainKey)
		return err
	}

	if err := c.skippedKeys.putBatch(c.ratchetPublicKey, skipped, c.maxSkippedKeys); err != nil {
		discardSkippedKeys(skipped)
		clearBytes(nextChainKey)
		return err
	}
	discardSkippedKeys(skipped)

	clearBytes(c.chainKey)
	c.chainKey = nextChainKey
	c.nextSequence = normalized + 1
	return nil
}

// DecryptWith obtains the one-time message key for sequence and delegates
// authentication and decryption to open. No chain state changes if open
// returns an error.
func (c *ReceiveChain) DecryptWith(
	sequence any,
	nonce, ciphertext, additionalData []byte,
	open OpenFunc,
) ([]byte, error) {
	if open == nil {
		return nil, ErrNilOpener
	}

	var plaintext []byte
	err := c.UseMessageKey(sequence, func(messageKey []byte) error {
		var openErr error
		plaintext, openErr = open(messageKey, nonce, ciphertext, additionalData)
		return openErr
	})
	if err != nil {
		return nil, err
	}
	return plaintext, nil
}

// Open is an alias for DecryptWith with the opener placed before the message
// fields.
func (c *ReceiveChain) Open(
	sequence any,
	open OpenFunc,
	nonce, ciphertext, additionalData []byte,
) ([]byte, error) {
	return c.DecryptWith(sequence, nonce, ciphertext, additionalData, open)
}

// Decrypt authenticates and decrypts a ciphertext using the library's
// AES-256-GCM construction. The ciphertext must include the authentication
// tag, and nonce must be 12 bytes.
func (c *ReceiveChain) Decrypt(
	sequence any,
	nonce, ciphertext, additionalData []byte,
) ([]byte, error) {
	return c.DecryptWith(sequence, nonce, ciphertext, additionalData, OpenAESGCM)
}

func discardSkippedKeys(skipped []skippedKeyEntry) {
	for _, entry := range skipped {
		clearBytes(entry.messageKey)
	}
}
