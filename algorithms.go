package netsec

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"io"
	"sync"
)

// Algorithm is a CertificateCompressionAlgorithm as defined by RFC 8879.
type Algorithm uint16

// Algorithms assigned by RFC 8879.
const (
	AlgorithmZlib   Algorithm = 1
	AlgorithmBrotli Algorithm = 2
	AlgorithmZstd   Algorithm = 3
)

// Short aliases for callers that prefer them.
const (
	Zlib   = AlgorithmZlib
	Brotli = AlgorithmBrotli
	Zstd   = AlgorithmZstd
)

// MaxUncompressedLength is the largest value representable by the uint24
// uncompressed_length field of CompressedCertificate.
const MaxUncompressedLength = 1<<24 - 1

// Compressor is the algorithm-specific codec used by the certificate
// compression machinery. Implementations must not retain source or dictionary
// slices after returning.
//
// Decompress must read at most maximum bytes, return exactly maximum bytes when
// the stream is well formed, and fail if the stream expands beyond maximum.
type Compressor interface {
	Compress(source, dictionary []byte) ([]byte, error)
	Decompress(source, dictionary []byte, maximum int) ([]byte, error)
}

type zlibCompressor struct{}

func (zlibCompressor) Compress(source, dictionary []byte) ([]byte, error) {
	var output bytes.Buffer

	var writer *zlib.Writer
	var err error
	if len(dictionary) == 0 {
		writer, err = zlib.NewWriterLevel(&output, zlib.DefaultCompression)
	} else {
		writer, err = zlib.NewWriterLevelDict(&output, zlib.DefaultCompression, dictionary)
	}
	if err != nil {
		return nil, fmt.Errorf("zlib: create compressor: %w", err)
	}

	if _, err := writer.Write(source); err != nil {
		_ = writer.Close()
		return nil, fmt.Errorf("zlib: compress: %w", err)
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("zlib: finish compression: %w", err)
	}
	return output.Bytes(), nil
}

func (zlibCompressor) Decompress(source, dictionary []byte, maximum int) ([]byte, error) {
	if maximum < 0 {
		return nil, ErrUncompressedLengthMismatch
	}
	if maximum > MaxUncompressedLength {
		return nil, ErrUncompressedLengthTooLarge
	}

	var reader io.ReadCloser
	var err error
	if len(dictionary) == 0 {
		reader, err = zlib.NewReader(bytes.NewReader(source))
	} else {
		reader, err = zlib.NewReaderDict(bytes.NewReader(source), dictionary)
	}
	if err != nil {
		return nil, fmt.Errorf("zlib: create decompressor: %w", err)
	}
	defer reader.Close()

	var output bytes.Buffer
	written, err := io.Copy(&output, io.LimitReader(reader, int64(maximum)+1))
	if err != nil {
		return nil, fmt.Errorf("zlib: decompress: %w", err)
	}
	if written != int64(maximum) {
		return nil, fmt.Errorf("%w: decompressed %d bytes, declared %d", ErrUncompressedLengthMismatch, written, maximum)
	}
	return output.Bytes(), nil
}

// CodecRegistry maps RFC 8879 algorithm identifiers to implementations.
// The zero value is not usable; use NewCodecRegistry.
type CodecRegistry struct {
	mutex  sync.RWMutex
	codecs map[Algorithm]Compressor
}

// NewCodecRegistry returns a registry containing the mandatory zlib codec.
// Brotli and zstd are assigned identifiers but are not implemented by the Go
// standard library; applications may register implementations explicitly.
func NewCodecRegistry() *CodecRegistry {
	registry := &CodecRegistry{codecs: make(map[Algorithm]Compressor)}
	registry.codecs[AlgorithmZlib] = zlibCompressor{}
	return registry
}

// NewDefaultCodecRegistry is an alias for NewCodecRegistry.
func NewDefaultCodecRegistry() *CodecRegistry {
	return NewCodecRegistry()
}

// Register installs a codec for algorithm. A nil codec removes the algorithm.
func (registry *CodecRegistry) Register(algorithm Algorithm, codec Compressor) error {
	if registry == nil {
		return ErrUnsupportedAlgorithm
	}
	registry.mutex.Lock()
	defer registry.mutex.Unlock()
	if registry.codecs == nil {
		registry.codecs = make(map[Algorithm]Compressor)
	}
	if codec == nil {
		delete(registry.codecs, algorithm)
		return nil
	}
	registry.codecs[algorithm] = codec
	return nil
}

// Lookup returns the codec registered for algorithm.
func (registry *CodecRegistry) Lookup(algorithm Algorithm) (Compressor, bool) {
	if registry == nil {
		return nil, false
	}
	registry.mutex.RLock()
	defer registry.mutex.RUnlock()
	codec, ok := registry.codecs[algorithm]
	return codec, ok
}

// Supports reports whether a codec is installed for algorithm.
func (registry *CodecRegistry) Supports(algorithm Algorithm) bool {
	_, ok := registry.Lookup(algorithm)
	return ok
}

// Select chooses an algorithm that is both offered by the client and
// implemented by the server. If preference is non-empty, server preference is
// honored; otherwise client order is used.
func (registry *CodecRegistry) Select(offered []Algorithm, preference []Algorithm) (Algorithm, bool) {
	if len(offered) == 0 {
		return 0, false
	}

	if len(preference) == 0 {
		for _, algorithm := range offered {
			if registry.Supports(algorithm) {
				return algorithm, true
			}
		}
		return 0, false
	}

	offeredSet := make(map[Algorithm]struct{}, len(offered))
	for _, algorithm := range offered {
		offeredSet[algorithm] = struct{}{}
	}
	for _, algorithm := range preference {
		if _, ok := offeredSet[algorithm]; !ok {
			continue
		}
		if registry.Supports(algorithm) {
			return algorithm, true
		}
	}
	return 0, false
}

// NegotiateAlgorithm returns the server-preferred algorithm that also appears
// in the client's list. If serverPreference is empty, the first listed client
// algorithm is returned. It does not check codec availability.
func NegotiateAlgorithm(offered []Algorithm, serverPreference []Algorithm) (Algorithm, bool) {
	if len(offered) == 0 {
		return 0, false
	}
	if len(serverPreference) == 0 {
		return offered[0], true
	}
	for _, preferred := range serverPreference {
		for _, candidate := range offered {
			if candidate == preferred {
				return candidate, true
			}
		}
	}
	return 0, false
}

var defaultCodecs = NewCodecRegistry()

// DefaultCodecRegistry returns the process-wide registry used by the
// package-level decompression helpers. Servers created by NewServer use their
// own registry unless one is supplied.
func DefaultCodecRegistry() *CodecRegistry {
	return defaultCodecs
}
