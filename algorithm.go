package netsec

import (
	"errors"
	"fmt"
	"sync"
)

// Algorithm is an RFC 8879 CertificateCompressionAlgorithm value.
type Algorithm uint16

const (
	// AlgorithmZlib is the zlib algorithm from RFC 8879.
	AlgorithmZlib Algorithm = 1
	// AlgorithmBrotli is the Brotli algorithm from RFC 8879. It is defined
	// for wire compatibility but is not registered by DefaultRegistry.
	AlgorithmBrotli Algorithm = 2
	// AlgorithmZstd is the zstd algorithm from RFC 8879. It is defined for
	// wire compatibility but is not registered by DefaultRegistry.
	AlgorithmZstd Algorithm = 3
)

// Codec compresses and decompresses one certificate message. The dictionary
// is selected by the server from the tenant identified by the SNI; it is not
// supplied by the client.
type Codec interface {
	Compress(src, dictionary []byte) ([]byte, error)

	// Decompress must not produce more than maxLen bytes. Any output in excess
	// of maxLen should be rejected without buffering it.
	Decompress(src, dictionary []byte, maxLen int) ([]byte, error)
}

// CodecRegistry maps wire algorithm identifiers to implementations. A
// registry is safe for concurrent use after construction.
type CodecRegistry struct {
	mu     sync.RWMutex
	codecs map[Algorithm]Codec
}

// NewRegistry returns an empty codec registry.
func NewRegistry() *CodecRegistry {
	return &CodecRegistry{codecs: make(map[Algorithm]Codec)}
}

// DefaultRegistry returns a registry containing the algorithms implemented by
// this package. Only zlib is built in because it is available in the Go
// standard library; applications can register additional RFC 8879 codecs.
func DefaultRegistry() *CodecRegistry {
	r := NewRegistry()
	if err := r.Register(AlgorithmZlib, zlibCodec{}); err != nil {
		panic(err)
	}
	return r
}

// Register adds a codec. An existing algorithm registration may only be
// replaced with a codec of the same concrete type, which keeps dictionary
// semantics stable for cached entries.
func (r *CodecRegistry) Register(algorithm Algorithm, codec Codec) error {
	if algorithm == 0 {
		return errors.New("netsec: algorithm zero is reserved")
	}
	if codec == nil {
		return errors.New("netsec: nil codec")
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.codecs == nil {
		r.codecs = make(map[Algorithm]Codec)
	}
	if existing, ok := r.codecs[algorithm]; ok {
		if fmt.Sprintf("%T", existing) != fmt.Sprintf("%T", codec) {
			return fmt.Errorf("netsec: algorithm %d already registered with a different codec type", algorithm)
		}
	}
	r.codecs[algorithm] = codec
	return nil
}

// Lookup returns the codec registered for algorithm.
func (r *CodecRegistry) Lookup(algorithm Algorithm) (Codec, bool) {
	if r == nil {
		return nil, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	codec, ok := r.codecs[algorithm]
	return codec, ok
}

// Select chooses the first algorithm in serverPreference that is both offered
// by the client and implemented by the registry.
func (r *CodecRegistry) Select(serverPreference, offered []Algorithm) (Algorithm, Codec, bool) {
	offeredSet := make(map[Algorithm]struct{}, len(offered))
	for _, algorithm := range offered {
		offeredSet[algorithm] = struct{}{}
	}
	for _, algorithm := range serverPreference {
		if _, ok := offeredSet[algorithm]; !ok {
			continue
		}
		if codec, ok := r.Lookup(algorithm); ok {
			return algorithm, codec, true
		}
	}
	return 0, nil, false
}
