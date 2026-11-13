package netsec

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sync"
)

// EntropySize is the minimum number of bytes requested from an EntropySource
// for each seed. Deriving the AES-256 key from additional bytes is allowed.
const EntropySize = sha256.Size

const counterBlockDomain uint64 = 0x4e45545345432d43 // "NETSEC-C"

var (
	// ErrConfig reports an invalid Source configuration.
	ErrConfig = errors.New("netsec: invalid configuration")
	// ErrEntropy reports that no usable entropy was available. The failed
	// operation produced no output.
	ErrEntropy = errors.New("netsec: entropy source failed")
	// ErrCounterExhausted reports that the deterministic block counter ran out.
	ErrCounterExhausted = errors.New("netsec: counter exhausted")
	// ErrInvalidLength reports a negative output length.
	ErrInvalidLength = errors.New("netsec: invalid output length")
)

// GenerationID identifies the process and VM generation that own a Source.
// A fork or snapshot restore must produce a different pair in each clone.
type GenerationID struct {
	ProcessID      int
	VMGenerationID string
}

// EntropySource supplies fresh, unpredictable seed material. Entropy must
// return at least n bytes on success and must not return stale or replayed
// data across VM generations.
type EntropySource interface {
	Entropy(n int) ([]byte, error)
}

// EntropyFunc adapts a function to EntropySource.
type EntropyFunc func(n int) ([]byte, error)

// Entropy implements EntropySource.
func (f EntropyFunc) Entropy(n int) ([]byte, error) {
	return f(n)
}

// Clock supplies a monotonic time reading in nanoseconds. Implementations
// should use an epoch that cannot go backwards during normal operation,
// for example time.Since(processStart). A regression forces a reseed.
type Clock interface {
	Now() int64
}

// ClockFunc adapts a function to Clock.
type ClockFunc func() int64

// Now implements Clock.
func (f ClockFunc) Now() int64 {
	return int64(f())
}

// Config contains the external inputs of a Source. All fields are required.
//
// Generation is called before every read. It must return the generation of
// the process currently executing, not a value captured at initialization.
// Clock must be monotonic.
type Config struct {
	Entropy    EntropySource
	Generation func() GenerationID
	Clock      Clock
}

// Source is a fork-aware deterministic random source. It is safe for
// concurrent use. The zero value is not usable; create one with New.
type Source struct {
	mu         sync.Mutex
	entropy    EntropySource
	generation func() GenerationID
	clock      Clock

	key       [sha256.Size]byte
	counter   uint64
	block     cipher.Block
	buf       [aes.BlockSize]byte
	bufOff    int
	bufLen    int
	seeded    bool
	needSeed  bool
	lastGen   GenerationID
	lastClock int64
}

// New validates cfg and creates a Source. The first read obtains the initial
// seed from cfg.Entropy; New itself does not touch the entropy source.
func New(cfg Config) (*Source, error) {
	if cfg.Entropy == nil {
		return nil, fmt.Errorf("%w: nil entropy source", ErrConfig)
	}
	if cfg.Generation == nil {
		return nil, fmt.Errorf("%w: nil generation function", ErrConfig)
	}
	if cfg.Clock == nil {
		return nil, fmt.Errorf("%w: nil clock function", ErrConfig)
	}
	return &Source{
		entropy:    cfg.Entropy,
		generation: cfg.Generation,
		clock:      cfg.Clock,
		needSeed:   true,
	}, nil
}

// Read fills p with deterministic random bytes and implements io.Reader.
//
// Before producing bytes, Read reads the current generation identifier and
// monotonic clock. If the identifier differs from the one used for the
// previous seed, the clock regressed, or a previous entropy attempt failed,
// Read reseeds while holding the Source lock. Other readers block for the
// duration of the reseed. If the entropy source fails, Read returns an error
// without writing to p.
func (s *Source) Read(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(p) == 0 {
		return 0, nil
	}

	gen := s.generation()
	now := s.clock.Now()
	if s.needSeed || !s.seeded || gen != s.lastGen || now < s.lastClock {
		if err := s.reseedLocked(gen, now); err != nil {
			return 0, err
		}
	} else {
		// The regression check is against the previous clock observation, not
		// only the clock value used by the last seed.
		s.lastClock = now
	}

	n := 0
	for n < len(p) {
		if s.bufLen == 0 {
			if err := s.fillBlockLocked(); err != nil {
				return n, err
			}
		}
		c := copy(p[n:], s.buf[s.bufOff:s.bufOff+s.bufLen])
		s.bufOff += c
		s.bufLen -= c
		n += c
	}
	return n, nil
}

// Bytes returns n random bytes. It is a convenience wrapper around Read and
// returns no partial result on error.
func (s *Source) Bytes(n int) ([]byte, error) {
	if n < 0 {
		return nil, fmt.Errorf("%w: %d", ErrInvalidLength, n)
	}
	b := make([]byte, n)
	if n == 0 {
		return b, nil
	}
	if _, err := io.ReadFull(s, b); err != nil {
		return nil, err
	}
	return b, nil
}

// reseedLocked replaces the generator state. The caller must hold s.mu.
//
// Buffered, unconsumed bytes are dropped before entropy is requested. If
// entropy fails, needSeed remains set so that no later read can expose bytes
// produced from the old state.
func (s *Source) reseedLocked(gen GenerationID, now int64) error {
	s.discardBufferLocked()

	entropy, err := s.entropy.Entropy(EntropySize)
	if err != nil {
		s.needSeed = true
		return fmt.Errorf("%w: %w", ErrEntropy, err)
	}
	if len(entropy) < EntropySize {
		s.needSeed = true
		return fmt.Errorf("%w: got %d bytes, need at least %d", ErrEntropy, len(entropy), EntropySize)
	}

	var oldState [sha256.Size + 8]byte
	copy(oldState[:sha256.Size], s.key[:])
	binary.BigEndian.PutUint64(oldState[sha256.Size:], s.counter)

	h := sha256.New()
	h.Write([]byte("netsec/reseed/v1\x00"))
	h.Write(oldState[:])

	var entropyLen [8]byte
	binary.BigEndian.PutUint64(entropyLen[:], uint64(len(entropy)))
	h.Write(entropyLen[:])
	h.Write(entropy)

	var pid [8]byte
	binary.BigEndian.PutUint64(pid[:], uint64(int64(gen.ProcessID)))
	h.Write(pid[:])

	var vmLen [8]byte
	binary.BigEndian.PutUint64(vmLen[:], uint64(len(gen.VMGenerationID)))
	h.Write(vmLen[:])
	h.Write([]byte(gen.VMGenerationID))

	var clock [8]byte
	binary.BigEndian.PutUint64(clock[:], uint64(now))
	h.Write(clock[:])

	var nextKey [sha256.Size]byte
	copy(nextKey[:], h.Sum(nil))

	block, err := aes.NewCipher(nextKey[:])
	if err != nil {
		s.needSeed = true
		return fmt.Errorf("netsec: initialize generator: %w", err)
	}

	s.key = nextKey
	s.block = block
	s.counter = 0
	s.seeded = true
	s.needSeed = false
	s.lastGen = gen
	s.lastClock = now
	return nil
}

func (s *Source) discardBufferLocked() {
	clear(s.buf[:])
	s.bufOff = 0
	s.bufLen = 0
}

func (s *Source) fillBlockLocked() error {
	if s.counter == ^uint64(0) {
		s.needSeed = true
		return ErrCounterExhausted
	}

	var counterBlock [aes.BlockSize]byte
	binary.BigEndian.PutUint64(counterBlock[:8], counterBlockDomain)
	binary.BigEndian.PutUint64(counterBlock[8:], s.counter)
	s.block.Encrypt(s.buf[:], counterBlock[:])
	s.counter++
	s.bufOff = 0
	s.bufLen = aes.BlockSize
	return nil
}
