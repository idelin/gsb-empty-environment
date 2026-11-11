// Package signedbody provides an net/http middleware that verifies
// HMAC-SHA256 chained, chunk-framed request bodies before any downstream
// handler is allowed to see them.
//
// Wire format: the request body is a sequence of frames
//
//	[4-byte big-endian length][chunk payload]
//
// terminated by EOF. The chain digest is
//
//	d_0 = HMAC(key, "signedbody/chunk/v1" ‖ 0^32 ‖ seq ‖ len ‖ payload)
//	d_i = HMAC(key, "signedbody/chunk/v1" ‖ d_{i-1} ‖ seq ‖ len ‖ payload)
//
// and the final signature, sent in the X-Signedbody-Signature header as hex,
// binds the total plaintext length:
//
//	sig = HMAC(key, "signedbody/final/v1" ‖ d_last ‖ totalLen)
//
// The expected total length is sent in X-Signedbody-Total-Length.
package signedbody

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"os"
	"strconv"
	"sync/atomic"
)

const (
	// HeaderSignature carries the hex-encoded final HMAC-SHA256 signature.
	HeaderSignature = "X-Signedbody-Signature"
	// HeaderTotalLength carries the decimal total plaintext length that the
	// final signature binds.
	HeaderTotalLength = "X-Signedbody-Total-Length"

	domainChunk = "signedbody/chunk/v1"
	domainFinal = "signedbody/final/v1"

	maxChunkSize = 64 << 20 // 64 MiB per frame, guards against bogus lengths

	defaultMemoryBudget = 1 << 20 // 1 MiB
)

// ErrVerification is returned (and surfaced to clients as 401) when the
// signature chain does not validate.
var ErrVerification = errors.New("signedbody: signature verification failed")

// Config configures the middleware.
type Config struct {
	// Key is the shared HMAC key. Required.
	Key []byte
	// MemoryBudget is the maximum number of body bytes held in RAM before
	// spilling to a request-specific temporary file. Defaults to 1 MiB.
	MemoryBudget int64
	// TempDir is the directory for spill files. Defaults to os.TempDir().
	TempDir string
}

var requestCounter atomic.Uint64

// Middleware returns an http.Handler that fully verifies the request body's
// signature chain before invoking next. The downstream handler is never
// called with unverified bytes: on any failure the request is rejected and
// all buffered data (memory or temp file) is discarded.
func Middleware(cfg Config, next http.Handler) http.Handler {
	if len(cfg.Key) == 0 {
		panic("signedbody: Config.Key is required")
	}
	if cfg.MemoryBudget <= 0 {
		cfg.MemoryBudget = defaultMemoryBudget
	}
	key := make([]byte, len(cfg.Key))
	copy(key, cfg.Key)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := requestCounter.Add(1)
		buf := newSpillBuffer(cfg.MemoryBudget, cfg.TempDir,
			fmt.Sprintf("signedbody-req-%d-", id))
		// Whatever happens downstream or on the wire, the request-specific
		// spill file is reclaimed when this request is done.
		defer buf.cleanup()

		total, err := verifyAndBuffer(r.Body, key, buf,
			r.Header.Get(HeaderSignature), r.Header.Get(HeaderTotalLength))
		if err != nil {
			// Never call downstream with unverified data. If the client
			// went away (cancelled upload), there is nobody to answer.
			select {
			case <-r.Context().Done():
				return
			default:
			}
			http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
			return
		}

		body, err := buf.reader()
		if err != nil {
			http.Error(w, http.StatusText(http.StatusInternalServerError),
				http.StatusInternalServerError)
			return
		}
		defer body.Close()

		r.Body = body
		r.ContentLength = total
		r.Header.Del(HeaderSignature)
		r.Header.Del(HeaderTotalLength)
		next.ServeHTTP(w, r)
	})
}

// verifyAndBuffer streams frames from body, verifies the chain incrementally,
// and writes the plaintext to buf. It returns the total plaintext length.
func verifyAndBuffer(body io.Reader, key []byte, buf io.Writer, sigHex, totalHeader string) (int64, error) {
	expected, err := hex.DecodeString(sigHex)
	if err != nil || len(expected) != sha256.Size {
		return 0, ErrVerification
	}
	declared, err := strconv.ParseInt(totalHeader, 10, 64)
	if err != nil || declared < 0 {
		return 0, ErrVerification
	}

	c := newChain(key)
	var total int64
	hdr := make([]byte, 4)
	for {
		if _, err := io.ReadFull(body, hdr); err != nil {
			if err == io.EOF {
				break
			}
			// Truncated or cancelled upload.
			return 0, fmt.Errorf("signedbody: reading frame header: %w", err)
		}
		n := binary.BigEndian.Uint32(hdr)
		if n > maxChunkSize {
			return 0, ErrVerification
		}
		hw := c.chunkWriter(n)
		if _, err := io.CopyN(io.MultiWriter(hw, buf), body, int64(n)); err != nil {
			return 0, fmt.Errorf("signedbody: reading chunk %d: %w", c.seq, err)
		}
		c.commit(hw)
		total += int64(n)
	}

	if total != declared {
		return 0, ErrVerification
	}
	if !hmac.Equal(c.final(uint64(total)), expected) {
		return 0, ErrVerification
	}
	return total, nil
}

// chain computes the rolling HMAC-SHA256 chain state.
type chain struct {
	key  []byte
	prev []byte
	seq  uint64
}

func newChain(key []byte) *chain {
	return &chain{key: key, prev: make([]byte, sha256.Size)}
}

// chunkWriter returns a hash.Hash primed with the domain separator, previous
// digest, sequence number and chunk length. The chunk payload must be written
// to it, then passed to commit.
func (c *chain) chunkWriter(n uint32) hash.Hash {
	h := hmac.New(sha256.New, c.key)
	h.Write([]byte(domainChunk))
	h.Write(c.prev)
	var fixed [16]byte
	binary.BigEndian.PutUint64(fixed[0:8], c.seq)
	binary.BigEndian.PutUint64(fixed[8:16], uint64(n))
	h.Write(fixed[:])
	return h
}

func (c *chain) commit(h hash.Hash) {
	c.prev = h.Sum(nil)
	c.seq++
}

// final computes the closing signature binding the total plaintext length.
func (c *chain) final(total uint64) []byte {
	h := hmac.New(sha256.New, c.key)
	h.Write([]byte(domainFinal))
	h.Write(c.prev)
	var lb [8]byte
	binary.BigEndian.PutUint64(lb[:], total)
	h.Write(lb[:])
	return h.Sum(nil)
}

// EncodeBody frames chunks into the wire format.
func EncodeBody(chunks [][]byte) []byte {
	var total int
	for _, c := range chunks {
		total += 4 + len(c)
	}
	out := make([]byte, 0, total)
	var hdr [4]byte
	for _, c := range chunks {
		binary.BigEndian.PutUint32(hdr[:], uint32(len(c)))
		out = append(out, hdr[:]...)
		out = append(out, c...)
	}
	return out
}

// Sign computes the final signature and total length for the given chunks,
// for use with the HeaderSignature and HeaderTotalLength headers.
func Sign(key []byte, chunks [][]byte) (signature string, total int64) {
	c := newChain(key)
	for _, chunk := range chunks {
		hw := c.chunkWriter(uint32(len(chunk)))
		hw.Write(chunk)
		c.commit(hw)
		total += int64(len(chunk))
	}
	return hex.EncodeToString(c.final(uint64(total))), total
}

// spillBuffer buffers plaintext in memory up to a budget, then spills to a
// request-specific temporary file.
type spillBuffer struct {
	budget  int64
	dir     string
	pattern string

	mem     []byte
	file    *os.File
	path    string
	spilled bool
}

func newSpillBuffer(budget int64, dir, pattern string) *spillBuffer {
	return &spillBuffer{budget: budget, dir: dir, pattern: pattern}
}

func (b *spillBuffer) Write(p []byte) (int, error) {
	if !b.spilled {
		if int64(len(b.mem)+len(p)) <= b.budget {
			b.mem = append(b.mem, p...)
			return len(p), nil
		}
		f, err := os.CreateTemp(b.dir, b.pattern+"*.tmp")
		if err != nil {
			return 0, err
		}
		b.file = f
		b.path = f.Name()
		b.spilled = true
		if _, err := f.Write(b.mem); err != nil {
			return 0, err
		}
		b.mem = nil
	}
	return b.file.Write(p)
}

// reader returns a ReadCloser over the buffered plaintext. After a spilled
// buffer is opened for reading, writes are no longer possible.
func (b *spillBuffer) reader() (io.ReadCloser, error) {
	if !b.spilled {
		return io.NopCloser(bytes.NewReader(b.mem)), nil
	}
	if err := b.file.Close(); err != nil {
		return nil, err
	}
	b.file = nil
	return os.Open(b.path)
}

// cleanup releases the spill file, if any. It is idempotent.
func (b *spillBuffer) cleanup() {
	if b.file != nil {
		b.file.Close()
		b.file = nil
	}
	if b.spilled && b.path != "" {
		os.Remove(b.path)
		b.path = ""
	}
}
