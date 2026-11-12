package netsec

import (
	"context"
	"errors"
	"fmt"
)

// Certificate compression algorithm identifiers from RFC 8879 section 3.
const (
	AlgZlib   uint16 = 1
	AlgBrotli uint16 = 2
	AlgZstd   uint16 = 3
)

var (
	// ErrInvalidConfig is returned when a Wrapper cannot be built with the
	// supplied limits or decompressor set.
	ErrInvalidConfig = errors.New("certdecompress: invalid configuration")

	// ErrAlgorithmNotNegotiated is returned when the peer selects an
	// algorithm that was not part of the locally negotiated set.
	ErrAlgorithmNotNegotiated = errors.New("certdecompress: algorithm not negotiated")

	// ErrDeclaredLengthExceeded is returned when the peer-declared
	// uncompressed length is not positive or exceeds MaxDeclaredLength.
	ErrDeclaredLengthExceeded = errors.New("certdecompress: declared length exceeds limit")

	// ErrLengthMismatch is returned when the produced output length differs
	// from the peer-declared uncompressed length.
	ErrLengthMismatch = errors.New("certdecompress: output length does not match declared length")

	// ErrOutputLimitExceeded is returned when decompression would produce
	// more than MaxOutput bytes.
	ErrOutputLimitExceeded = errors.New("certdecompress: output limit exceeded")

	// ErrStepLimitExceeded is returned when the engine cannot finish the
	// stream within MaxSteps work steps, which also stops decompression bombs
	// that emit very little output per step.
	ErrStepLimitExceeded = errors.New("certdecompress: step limit exceeded")

	// ErrNoProgress is returned when a decompression step neither produced
	// output nor finished nor failed, which protects against engines that
	// would otherwise spin until the step budget is consumed.
	ErrNoProgress = errors.New("certdecompress: decompressor made no progress")

	// ErrTrailingData is returned when compressed bytes remain after the end
	// of the first stream, for example a second concatenated frame.
	ErrTrailingData = errors.New("certdecompress: trailing compressed frame")

	// ErrDecompressor is returned when the selected engine rejects the
	// compressed payload as malformed.
	ErrDecompressor = errors.New("certdecompress: decompressor failure")
)

// Limits caps every resource an attacker can influence with a small
// compressed payload. All fields must be positive.
type Limits struct {
	// MaxDeclaredLength bounds the uncompressed length the peer is allowed to
	// announce in the certificate message.
	MaxDeclaredLength int

	// MaxOutput bounds the number of bytes that are ever materialized, even if
	// the declared length lies and the real expansion is larger.
	MaxOutput int

	// MaxSteps bounds the total number of Step calls. It limits CPU work
	// independently of output size.
	MaxSteps int
}

// ParseCertChain receives the certificate chain only after the whole stream
// has been decompressed and every length check has passed. The byte slice is
// only valid for the duration of the callback and must not be retained.
type ParseCertChain func(algorithm uint16, chain []byte) error

// Wrapper is the resource-isolating entry point used by the TLS handshake
// host. It is safe for concurrent use by multiple handshakes.
type Wrapper struct {
	limits  Limits
	engines map[uint16]Decompressor
}

// NewWrapper builds a Wrapper. The negotiated set contains exactly the
// algorithms accepted for the connection; every Decompressor must advertise a
// distinct algorithm id.
func NewWrapper(limits Limits, negotiated ...Decompressor) (*Wrapper, error) {
	if limits.MaxDeclaredLength <= 0 || limits.MaxOutput <= 0 || limits.MaxSteps <= 0 {
		return nil, fmt.Errorf("%w: all limits must be positive", ErrInvalidConfig)
	}
	if len(negotiated) == 0 {
		return nil, fmt.Errorf("%w: at least one algorithm is required", ErrInvalidConfig)
	}

	engines := make(map[uint16]Decompressor, len(negotiated))
	for _, d := range negotiated {
		if d == nil {
			return nil, fmt.Errorf("%w: nil decompressor", ErrInvalidConfig)
		}
		id := d.Algorithm()
		if _, exists := engines[id]; exists {
			return nil, fmt.Errorf("%w: duplicate algorithm id %d", ErrInvalidConfig, id)
		}
		engines[id] = d
	}

	return &Wrapper{limits: limits, engines: engines}, nil
}

// NegotiatedAlgorithms reports the accepted algorithm ids in ascending order.
func (w *Wrapper) NegotiatedAlgorithms() []uint16 {
	ids := make([]uint16, 0, len(w.engines))
	for id := range w.engines {
		ids = append(ids, id)
	}
	for i := 1; i < len(ids); i++ {
		for j := i; j > 0 && ids[j-1] > ids[j]; j-- {
			ids[j-1], ids[j] = ids[j], ids[j-1]
		}
	}
	return ids
}

// Decompress validates and expands one compressed certificate message.

// algorithm must come from the peer's certificate_compression_algorithms list,
// declaredLen is the uncompressed length announced by the peer, and compressed
// is the payload. The parse callback is invoked exactly once, and only after
// the full stream has been expanded with no trailing frame and with an output
// length equal to declaredLen; on any failure it is never invoked, so a
// partially decompressed chain cannot escape the wrapper.
//
// The host wires this into its handshake by mapping each negotiated
// crypto/tls certificate compression algorithm to its Decompressor factory
// and calling Decompress inside the handshake's decompression hook.
func (w *Wrapper) Decompress(ctx context.Context, algorithm uint16, declaredLen int, compressed []byte, parse ParseCertChain) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if parse == nil {
		return fmt.Errorf("%w: nil certificate parse callback", ErrInvalidConfig)
	}

	engine, ok := w.engines[algorithm]
	if !ok {
		return fmt.Errorf("%w: algorithm %d", ErrAlgorithmNotNegotiated, algorithm)
	}
	if declaredLen <= 0 || declaredLen > w.limits.MaxDeclaredLength {
		return fmt.Errorf("%w: declared %d, limit %d", ErrDeclaredLengthExceeded, declaredLen, w.limits.MaxDeclaredLength)
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	step, err := engine.New(compressed)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrDecompressor, err)
	}

	outputCap := w.limits.MaxOutput
	if declaredLen < outputCap {
		outputCap = declaredLen
	}
	output := make([]byte, 0, outputCap)

	for steps := 0; steps < w.limits.MaxSteps; {
		if err := ctx.Err(); err != nil {
			return err
		}

		chunk, done, err := step.Step(ctx)
		steps++
		if err != nil {
			return fmt.Errorf("%w: %v", ErrDecompressor, err)
		}

		if len(chunk) > 0 {
			if len(output)+len(chunk) > w.limits.MaxOutput {
				return fmt.Errorf("%w: produced at least %d, limit %d",
					ErrOutputLimitExceeded, len(output)+len(chunk), w.limits.MaxOutput)
			}
			if len(output)+len(chunk) > declaredLen {
				return fmt.Errorf("%w: produced %d, declared %d",
					ErrLengthMismatch, len(output)+len(chunk), declaredLen)
			}
			output = append(output, chunk...)
		}

		if done {
			if step.Trailing() {
				return ErrTrailingData
			}
			if len(output) != declaredLen {
				return fmt.Errorf("%w: produced %d, declared %d", ErrLengthMismatch, len(output), declaredLen)
			}
			return parse(algorithm, output)
		}

		if len(chunk) == 0 {
			return ErrNoProgress
		}
	}

	return fmt.Errorf("%w: more than %d steps", ErrStepLimitExceeded, w.limits.MaxSteps)
}
