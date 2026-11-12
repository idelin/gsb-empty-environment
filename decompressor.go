package netsec

import "context"

// StepDecompressor is a single-use, metered decompression engine.
//
// A StepDecompressor is created per Decompress call through a Decompressor
// factory, so implementations do not need to be safe for concurrent use.
// Every call to Step performs a bounded unit of decompression work and either
// appends more output bytes, declares stream end, or returns an error.
type StepDecompressor interface {
	// Step performs one bounded unit of decompression work.
	//
	// It returns the newly produced bytes (which may be empty), done=true when
	// the compressed stream has ended cleanly, and err non-nil when the input
	// is malformed. When done is true err must be nil and no further Step call
	// is permitted.
	Step(ctx context.Context) (out []byte, done bool, err error)

	// Trailing reports whether unconsumed compressed bytes remain after the
	// stream ended. It must only be called after Step returned done=true.
	Trailing() bool
}

// Decompressor is a factory for one negotiated compression algorithm.
//
// Each invocation receives the compressed payload of one certificate message
// and returns a fresh, single-use StepDecompressor. Factories must be safe for
// concurrent use because handshakes on different connections run concurrently.
type Decompressor interface {
	Algorithm() uint16
	New(input []byte) (StepDecompressor, error)
}
