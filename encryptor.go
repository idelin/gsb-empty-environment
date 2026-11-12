package netsec

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"sync"
	"time"
)

const (
	// DefaultMaxMessages is the number of messages a data key may encrypt.
	DefaultMaxMessages uint64 = 1000

	// DefaultMaxAge is the maximum wall-clock lifetime of a data key.
	DefaultMaxAge = 4 * time.Hour
)

// EncryptorConfig configures a topic Encryptor. Zero values select secure
// defaults for Cipher, KeyWrapper, Now, Random, MaxMessagesPerKey, and MaxAge.
type EncryptorConfig struct {
	Topic             string
	Registry          *Registry
	Cipher            Cipher
	KeyWrapper        KeyWrapper
	Now               func() time.Time
	Random            io.Reader
	MaxMessagesPerKey uint64
	MaxAge            time.Duration

	// NonceCounterStart starts each data key's 8-byte counter at this value.
	// It is normally zero. Supplying math.MaxUint64 is useful for exercising
	// counter exhaustion without generating 2^64 messages.
	NonceCounterStart uint64
}

type activeDataKey struct {
	id               string
	key              []byte
	noncePrefix      [noncePrefixSize]byte
	nextCounter      uint64
	counterExhausted bool
	createdAt        time.Time
	messageCount     uint64
	wrappedKeys      []WrappedDataKey
}

// Encryptor owns the rotating data-key state for one topic. It is safe for
// concurrent use.
type Encryptor struct {
	topic             string
	registry          *Registry
	cipher            Cipher
	keyWrapper        KeyWrapper
	now               func() time.Time
	random            io.Reader
	maxMessages       uint64
	maxAge            time.Duration
	nonceCounterStart uint64

	mu         sync.Mutex
	generation uint64
	active     *activeDataKey
}

// NewEncryptor constructs a topic Encryptor.
func NewEncryptor(config EncryptorConfig) (*Encryptor, error) {
	if config.Registry == nil {
		return nil, fmt.Errorf("%w: nil recipient registry", ErrInvalidConfig)
	}
	if len(config.Registry.names) == 0 {
		return nil, fmt.Errorf("%w: empty recipient registry", ErrInvalidConfig)
	}
	if config.MaxAge < 0 {
		return nil, fmt.Errorf("%w: negative max age", ErrInvalidConfig)
	}

	encryptor := &Encryptor{
		topic:             config.Topic,
		registry:          config.Registry,
		cipher:            config.Cipher,
		keyWrapper:        config.KeyWrapper,
		now:               config.Now,
		random:            config.Random,
		maxMessages:       config.MaxMessagesPerKey,
		maxAge:            config.MaxAge,
		nonceCounterStart: config.NonceCounterStart,
	}
	if encryptor.cipher == nil {
		encryptor.cipher = NewAESGCM()
	}
	if encryptor.keyWrapper == nil {
		encryptor.keyWrapper = NewRSAOAEPWrapper()
	}
	if encryptor.now == nil {
		encryptor.now = time.Now
	}
	if encryptor.random == nil {
		encryptor.random = rand.Reader
	}
	if encryptor.maxMessages == 0 {
		encryptor.maxMessages = DefaultMaxMessages
	}
	if encryptor.maxAge == 0 {
		encryptor.maxAge = DefaultMaxAge
	}
	return encryptor, nil
}

// Encrypt encrypts plaintext under the topic's current data key. A new data
// key is generated before the first message and whenever the message-count,
// age, or nonce-counter limit has been reached.
func (e *Encryptor) Encrypt(plaintext []byte) (*Message, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	now := e.now()
	if e.needsRotation(now) {
		if err := e.rotate(now); err != nil {
			return nil, err
		}
	}

	nonce, err := e.active.nextNonce()
	if err != nil {
		return nil, err
	}

	header := &Message{
		Version:       messageVersion,
		DataKeyID:     e.active.id,
		Nonce:         nonce,
		EncryptedKeys: e.active.wrappedKeys,
	}
	additionalData, err := buildAdditionalData(e.topic, header)
	if err != nil {
		return nil, err
	}
	ciphertext, err := e.cipher.Encrypt(e.active.key, nonce, plaintext, additionalData)
	if err != nil {
		return nil, fmt.Errorf("encrypt message: %w", err)
	}
	e.active.messageCount++

	return &Message{
		Version:       messageVersion,
		DataKeyID:     e.active.id,
		Nonce:         append([]byte(nil), nonce...),
		EncryptedKeys: cloneWrappedKeys(e.active.wrappedKeys),
		Ciphertext:    ciphertext,
	}, nil
}

func (e *Encryptor) needsRotation(now time.Time) bool {
	if e.active == nil {
		return true
	}
	if e.active.counterExhausted {
		return true
	}
	if e.active.messageCount >= e.maxMessages {
		return true
	}
	return !now.Before(e.active.createdAt.Add(e.maxAge))
}

func (e *Encryptor) rotate(now time.Time) error {
	dataKey, err := readRandom(e.random, payloadKeySize)
	if err != nil {
		return fmt.Errorf("generate data key: %w", err)
	}
	prefix, err := readRandom(e.random, noncePrefixSize)
	if err != nil {
		clear(dataKey)
		return fmt.Errorf("generate nonce prefix: %w", err)
	}
	identifier, err := readRandom(e.random, 8)
	if err != nil {
		clear(dataKey)
		return fmt.Errorf("generate data key id: %w", err)
	}

	generation := e.generation + 1
	dataKeyID := fmt.Sprintf("v%d-%x", generation, identifier)
	wrappedKeys := make([]WrappedDataKey, 0, len(e.registry.names))
	for _, keyName := range e.registry.names {
		publicKey, ok := e.registry.publicKey(keyName)
		if !ok {
			clear(dataKey)
			return fmt.Errorf("%w: registered key %q disappeared", ErrInvalidConfig, keyName)
		}
		wrapped, err := e.keyWrapper.WrapDataKey(KeyWrapContext{
			Topic:     e.topic,
			DataKeyID: dataKeyID,
			KeyName:   keyName,
		}, dataKey, publicKey)
		if err != nil {
			clear(dataKey)
			return fmt.Errorf("wrap data key for %q: %w", keyName, err)
		}
		wrappedKeys = append(wrappedKeys, WrappedDataKey{
			KeyName:    keyName,
			WrappedKey: append([]byte(nil), wrapped...),
		})
	}

	if e.active != nil {
		clear(e.active.key)
	}
	e.generation = generation
	e.active = &activeDataKey{
		id:          dataKeyID,
		key:         dataKey,
		noncePrefix: [noncePrefixSize]byte(prefix),
		nextCounter: e.nonceCounterStart,
		createdAt:   now,
		wrappedKeys: wrappedKeys,
	}
	return nil
}

func (key *activeDataKey) nextNonce() ([]byte, error) {
	if key.counterExhausted {
		return nil, ErrNonceExhausted
	}

	nonce := make([]byte, nonceSize)
	copy(nonce[:noncePrefixSize], key.noncePrefix[:])
	binary.BigEndian.PutUint64(nonce[noncePrefixSize:], key.nextCounter)

	if key.nextCounter == math.MaxUint64 {
		key.counterExhausted = true
	} else {
		key.nextCounter++
	}
	return nonce, nil
}

func readRandom(reader io.Reader, size int) ([]byte, error) {
	data := make([]byte, size)
	if _, err := io.ReadFull(reader, data); err != nil {
		return nil, err
	}
	return data, nil
}
