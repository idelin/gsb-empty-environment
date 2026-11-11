package sigv4chunk

import (
	"bufio"
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strconv"
	"strings"
)

var (
	errChunkHeaderTooLong     = errors.New("sigv4: chunk header exceeds size limit")
	errMalformedChunkHeader   = errors.New("sigv4: malformed chunk header")
	errMissingChunkSig        = errors.New("sigv4: chunk-signature parameter missing")
	errBadChunkSignature      = errors.New("sigv4: chunk signature is not valid hex")
	errChunkTooLarge          = errors.New("sigv4: chunk exceeds declared decoded length")
	errChunkSignatureMismatch = errors.New("sigv4: chunk signature does not match")
	errNoFinalEmptyChunk      = errors.New("sigv4: stream ended without the required empty chunk")
	errUnexpectedTrailer      = errors.New("sigv4: trailers and trailing bytes after the final chunk are not accepted")
	errDeclaredLengthMismatch = errors.New("sigv4: decoded content length does not match X-Amz-Decoded-Content-Length")
)

// chunkVerifier decodes an aws-chunked request body and verifies every chunk
// signature. Plaintext is only ever exposed through Read after the
// corresponding chunk signature has been verified; once any error occurs the
// reader fails permanently so unverified data can never reach a caller.
type chunkVerifier struct {
	r          *bufio.Reader
	amzDate    string
	scope      string
	key        []byte
	priorSig   []byte
	declared   int64 // -1 disables the declared-length check
	decoded    int64
	pending    []byte
	finished   bool
	failed     bool
	failureErr error
}

func newChunkVerifier(body io.Reader, amzDate, scope string, key, seedSignature []byte, declared int64) *chunkVerifier {
	return &chunkVerifier{
		r:        bufio.NewReader(body),
		amzDate:  amzDate,
		scope:    scope,
		key:      key,
		priorSig: seedSignature,
		declared: declared,
	}
}

func (c *chunkVerifier) fail(err error) (int, error) {
	c.failed = true
	c.failureErr = err
	c.pending = nil
	return 0, err
}

// Read returns verified plaintext only. After a verification failure every
// subsequent call returns the original error, even if the underlying body
// still contains bytes.
func (c *chunkVerifier) Read(p []byte) (int, error) {
	if c.failed {
		return 0, c.failureErr
	}
	if len(c.pending) > 0 {
		n := copy(p, c.pending)
		c.pending = c.pending[n:]
		return n, nil
	}
	if c.finished {
		return 0, io.EOF
	}

	data, final, err := c.nextChunk()
	if err != nil {
		return c.fail(err)
	}
	if final {
		c.finished = true
		if c.declared >= 0 && c.decoded != c.declared {
			return c.fail(errDeclaredLengthMismatch)
		}
		return 0, io.EOF
	}
	c.pending = data
	n := copy(p, c.pending)
	c.pending = c.pending[n:]
	return n, nil
}

// verifyAll drains the stream, returning the complete verified plaintext.
// On any failure the partial plaintext is discarded.
func (c *chunkVerifier) verifyAll() ([]byte, error) {
	var out bytes.Buffer
	if _, err := io.Copy(&out, c); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func (c *chunkVerifier) readLine() (string, error) {
	line := make([]byte, 0, 128)
	for len(line) < maxChunkHeaderSize {
		ch, err := c.r.ReadByte()
		if err != nil {
			return "", err
		}
		if ch == '\n' {
			return strings.TrimSuffix(string(line), "\r"), nil
		}
		line = append(line, ch)
	}
	return "", errChunkHeaderTooLong
}

// nextChunk reads and verifies one chunk. The returned data is nil for the
// mandatory final empty chunk, in which case final is true.
func (c *chunkVerifier) nextChunk() (data []byte, final bool, err error) {
	line, err := c.readLine()
	if err != nil {
		if errors.Is(err, io.EOF) {
			return nil, false, errNoFinalEmptyChunk
		}
		return nil, false, err
	}

	sizePart := line
	var params []string
	if semi := strings.IndexByte(line, ';'); semi >= 0 {
		sizePart = line[:semi]
		params = strings.Split(line[semi+1:], ";")
	}
	size, err := strconv.ParseUint(sizePart, 16, 64)
	if err != nil {
		return nil, false, errMalformedChunkHeader
	}

	var signature []byte
	for _, param := range params {
		name, value, ok := strings.Cut(param, "=")
		if !ok {
			return nil, false, errMalformedChunkHeader
		}
		if name == "chunk-signature" {
			signature, err = hex.DecodeString(value)
			if err != nil || len(signature) != sha256.Size {
				return nil, false, errBadChunkSignature
			}
		}
	}
	if signature == nil {
		return nil, false, errMissingChunkSig
	}

	if c.declared >= 0 && int64(size) > c.declared-c.decoded {
		return nil, false, errChunkTooLarge
	}
	data = make([]byte, size)
	if _, err := io.ReadFull(c.r, data); err != nil {
		return nil, false, err
	}
	crlf := make([]byte, 2)
	if _, err := io.ReadFull(c.r, crlf); err != nil {
		return nil, false, err
	}
	if crlf[0] != '\r' || crlf[1] != '\n' {
		return nil, false, errMalformedChunkHeader
	}

	if err := c.verifyChunk(data, signature); err != nil {
		return nil, false, err
	}
	c.priorSig = signature

	if size == 0 {
		// The terminating empty chunk must be the last thing on the wire:
		// chunk trailers are not supported.
		if _, readErr := c.r.ReadByte(); readErr == nil {
			return nil, false, errUnexpectedTrailer
		} else if !errors.Is(readErr, io.EOF) {
			return nil, false, readErr
		}
		return nil, true, nil
	}

	c.decoded += int64(size)
	return data, false, nil
}

// verifyChunk computes
//
//	string = "AWS4-HMAC-SHA256-PAYLOAD\n" + date + "\n" + scope + "\n"
//	       + hex(previousSignature) + "\n"
//	       + hex(sha256("")) + "\n" + hex(sha256(chunkData))
//	signature = HMAC-SHA256(signingKey, string)
//
// and compares it against the signature declared on the wire. The seed
// signature from the Authorization header seeds the first chunk.
func (c *chunkVerifier) verifyChunk(data, declaredSignature []byte) error {
	emptyHash := hexSHA256(nil)
	stringToSign := strings.Join([]string{
		payloadAlgorithm,
		c.amzDate,
		c.scope,
		hex.EncodeToString(c.priorSig),
		emptyHash,
		hexSHA256(data),
	}, "\n")
	expected := hmacSHA256(c.key, []byte(stringToSign))
	if !hmac.Equal(expected, declaredSignature) {
		return errChunkSignatureMismatch
	}
	return nil
}
