package netsec

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"time"
)

// tokenVersion binds the canonical wire format so future field changes cannot
// be silently reinterpreted by older verifiers.
const tokenVersion byte = 1

// digestLen is the size in bytes of an ancestor/token SHA-256 digest.
const digestLen = sha256.Size

// Digest is a content-addressed SHA-256 digest of a signed token.
type Digest [digestLen]byte

func (d Digest) Hex() string {
	return hex.EncodeToString(d[:])
}

// Token is one hop in a delegation path.
//
// Every hop is self-describing:
//   - Ancestors holds the full ordered chain of ancestor token digests,
//     starting at the bootstrap token and ending at the immediate parent.
//   - EarliestExpiry is min(bootstrap expiry, every intermediate expiry): no
//     hop may ever push it later than the value it inherited.
//   - RemainingHops counts the delegation hops still available; each hop
//     decrements the inherited value by one.
type Token struct {
	Version   byte
	Bootstrap bool
	// ChainID is the digest of the bootstrap token that roots this delegation
	// path. Every hop inherits it, so tokens from different chains cannot be
	// spliced together even when their per-token signatures are valid.
	ChainID        Digest
	Issuer         Identity
	Subject        Identity
	Ancestors      []Digest
	IssuedAt       time.Time
	EarliestExpiry time.Time
	RemainingHops  int
	Nonce          []byte
	Signature      []byte
}

// ErrMalformedToken is returned when a token fails structural validation.
var ErrMalformedToken = errors.New("netsec: malformed token")

// validate checks structural invariants independent of any signature or chain
// context.
func (t *Token) validate() error {
	if t == nil {
		return ErrMalformedToken
	}
	if t.Version != tokenVersion {
		return fmt.Errorf("%w: unsupported version %d", ErrMalformedToken, t.Version)
	}
	if err := t.Issuer.Validate(); err != nil {
		return err
	}
	if err := t.Subject.Validate(); err != nil {
		return err
	}
	if t.IssuedAt.IsZero() || t.EarliestExpiry.IsZero() {
		return fmt.Errorf("%w: missing timestamps", ErrMalformedToken)
	}
	if !t.EarliestExpiry.After(t.IssuedAt) {
		return fmt.Errorf("%w: expiry must be after issuance", ErrMalformedToken)
	}
	if t.RemainingHops < 0 {
		return fmt.Errorf("%w: negative remaining hops", ErrMalformedToken)
	}
	if len(t.Nonce) == 0 {
		return fmt.Errorf("%w: missing nonce", ErrMalformedToken)
	}
	if len(t.Signature) == 0 {
		return fmt.Errorf("%w: missing signature", ErrMalformedToken)
	}
	if t.Bootstrap {
		if len(t.Ancestors) != 0 {
			return fmt.Errorf("%w: bootstrap token cannot carry ancestors", ErrMalformedToken)
		}
	} else if len(t.Ancestors) == 0 {
		return fmt.Errorf("%w: delegated token must carry ancestor digests", ErrMalformedToken)
	}
	if t.ChainID == (Digest{}) {
		return fmt.Errorf("%w: token missing chain id", ErrMalformedToken)
	}
	return nil
}

// renderBytes renders the deterministic, length-prefixed encoding of the
// token. When includeChain is false the ChainID slot is omitted, which yields
// the self-referential-free root digest of a bootstrap token.
func (t *Token) renderBytes(includeChain bool) []byte {
	var buf bytes.Buffer

	putByte := func(b byte) { buf.WriteByte(b) }
	putBytes := func(b []byte) {
		var length [8]byte
		length[0] = byte(len(b) >> 56)
		length[1] = byte(len(b) >> 48)
		length[2] = byte(len(b) >> 40)
		length[3] = byte(len(b) >> 32)
		length[4] = byte(len(b) >> 24)
		length[5] = byte(len(b) >> 16)
		length[6] = byte(len(b) >> 8)
		length[7] = byte(len(b))
		buf.Write(length[:])
		buf.Write(b)
	}
	putString := func(s string) { putBytes([]byte(s)) }
	putTime := func(ts time.Time) { putString(strconv.FormatInt(ts.UnixNano(), 10)) }
	putInt := func(n int) { putString(strconv.Itoa(n)) }

	putByte(tokenVersion)
	if t.Bootstrap {
		putByte(1)
	} else {
		putByte(0)
	}
	putString(t.Issuer.TenantID)
	putString(t.Issuer.ServiceID)
	putString(t.Subject.TenantID)
	putString(t.Subject.ServiceID)

	// Fixed-size ancestor digests: only the count needs a prefix.
	var count [8]byte
	count[7] = byte(len(t.Ancestors))
	buf.Write(count[:])
	for i := range t.Ancestors {
		buf.Write(t.Ancestors[i][:])
	}
	if includeChain {
		buf.Write(t.ChainID[:])
	}

	putTime(t.IssuedAt)
	putTime(t.EarliestExpiry)
	putInt(t.RemainingHops)
	putBytes(t.Nonce)
	return buf.Bytes()
}

// signingBytes renders the canonical byte string that an issuer signs. All
// variable-length fields are length-prefixed so the encoding is unambiguous
// and canonical across processes.
func (t *Token) signingBytes() []byte {
	return t.renderBytes(true)
}

// rootDigest is the unforgeable content digest used as a bootstrap token's
// ChainID. It excludes ChainID itself to avoid a self-referential definition,
// and excludes Signature so it is stable across signing.
func (t *Token) rootDigest() Digest {
	return sha256.Sum256(t.renderBytes(false))
}

// Digest returns the content digest over the signed byte string. It is
// independent of the Signature field, so digests are stable before and after
// signing and cannot be forged by re-signing a mutated token.
func (t *Token) Digest() Digest {
	return sha256.Sum256(t.signingBytes())
}
