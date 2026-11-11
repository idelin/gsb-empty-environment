package dkimcanon

import (
	"crypto/subtle"
	"hash"
	"io"
)

// WriteFunc adapts a plain callback into an io.Writer, allowing the
// canonicalized stream to be wired directly into a relay's write hook.
type WriteFunc func(p []byte) error

// Write implements io.Writer by invoking the callback.
func (f WriteFunc) Write(p []byte) (int, error) {
	if err := f(p); err != nil {
		return 0, err
	}
	return len(p), nil
}

// Digester streams a mail body through a Canonicalizer into a hash, producing
// the DKIM body hash ("bh=") defined in RFC 6376, section 3.7.
//
// It is itself an io.Writer: feed the raw body with Write and call Close (or
// Sum) once the body is complete. Line-ending validation happens before any
// byte reaches the hash, so an illegal newline always fails verification.
type Digester struct {
	canon *Canonicalizer
	hash  hash.Hash
}

// NewDigester builds a Digester for the given algorithm and hash. Common
// choices are crypto.SHA256.New() or (for legacy signatures)
// crypto.SHA1.New().
func NewDigester(algo Algorithm, h hash.Hash, opts ...Option) *Digester {
	return &Digester{
		canon: NewCanonicalizer(h, algo, opts...),
		hash:  h,
	}
}

// NewDigesterWithSink canonicalizes into both h (for the body hash) and an
// extra sink such as a relay's forwarding callback. The canonical byte stream
// delivered to the two sinks is identical.
func NewDigesterWithSink(algo Algorithm, h hash.Hash, sink io.Writer, opts ...Option) *Digester {
	return &Digester{
		canon: NewCanonicalizer(io.MultiWriter(h, sink), algo, opts...),
		hash:  h,
	}
}

// Write feeds a chunk of the raw message body.
func (d *Digester) Write(p []byte) (int, error) { return d.canon.Write(p) }

// Close finishes canonicalization. It must be called before Sum or Verify so
// that trailing empty lines are reduced and the final CRLF is emitted.
func (d *Digester) Close() error { return d.canon.Close() }

// Sum returns the body hash. It closes the canonicalizer first; callers that
// need to handle a canonicalization error explicitly should call Close first.
func (d *Digester) Sum() ([]byte, error) {
	if err := d.Close(); err != nil {
		return nil, err
	}
	return d.hash.Sum(nil), nil
}

// Verify reports whether the recomputed body hash equals want (the decoded
// "bh=" tag value). It returns ErrDigestMismatch on a mismatch and propagates
// any canonicalization error (for example ErrInvalidNewline): a body with
// illegal line endings can therefore never be reported as verified.
func (d *Digester) Verify(want []byte) error {
	got, err := d.Sum()
	if err != nil {
		return err
	}
	if subtle.ConstantTimeCompare(got, want) != 1 {
		return ErrDigestMismatch
	}
	return nil
}

// Canonicalizer exposes the underlying canonicalizer for status inspection.
func (d *Digester) Canonicalizer() *Canonicalizer { return d.canon }

// Truncated reports whether the configured body length limit was reached.
func (d *Digester) Truncated() bool { return d.canon.Truncated() }

// Emitted reports how many canonical bytes were fed to the hash.
func (d *Digester) Emitted() int64 { return d.canon.Emitted() }

// BodyHash is a one-shot convenience: it canonicalizes body and returns its
// hash. newHash constructs the hash, for example sha256.New.
func BodyHash(algo Algorithm, newHash func() hash.Hash, body []byte, opts ...Option) ([]byte, error) {
	h := newHash()
	d := NewDigester(algo, h, opts...)
	if _, err := d.Write(body); err != nil {
		return nil, err
	}
	return d.Sum()
}
