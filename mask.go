// Package mask deterministically replaces the values of selected fields
// in JSON documents. The same original value always maps to the same
// replacement across documents and runs, JSON types and null values are
// preserved, and bytes outside the selected fields are copied verbatim.
//
// Mappings are kept in a persistent Store. When two different originals
// would produce the same replacement, the collision is resolved by
// allocating a deterministic suffix from the persistent table. New
// mappings are committed in a single atomic transaction: if masking or
// committing fails, no partial mappings are published.
package mask

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const (
	kindString = "s"
	kindNumber = "n"
	kindBool   = "b"
)

const maxCommitRetries = 8

// DigestFunc derives the deterministic seed for a masked value from its
// type-tagged original. It is injectable so tests can force collisions.
type DigestFunc func(value string) string

// Option configures a Masker.
type Option func(*Masker)

// WithDigestFunc overrides the digest function used to derive masked
// values. Passing a constant function forces every value onto the same
// candidate, exercising collision handling.
func WithDigestFunc(f DigestFunc) Option {
	return func(m *Masker) { m.digest = f }
}

// WithKey sets the secret mixed into the default digest, so masked
// values cannot be reproduced without the key.
func WithKey(key string) Option {
	return func(m *Masker) { m.key = key }
}

// Masker rewrites JSON documents, replacing the values of the selected
// fields with deterministic, type-preserving substitutes.
type Masker struct {
	store  Store
	fields map[string]bool
	key    string
	digest DigestFunc
}

// New returns a Masker that masks the values of the named object fields
// (matched at any depth) and of any array elements nested under them.
func New(store Store, fields []string, opts ...Option) *Masker {
	m := &Masker{
		store:  store,
		fields: make(map[string]bool, len(fields)),
	}
	for _, f := range fields {
		m.fields[f] = true
	}
	for _, o := range opts {
		o(m)
	}
	if m.digest == nil {
		key := m.key
		m.digest = func(v string) string {
			sum := sha256.Sum256([]byte(key + "\x00" + v))
			return hex.EncodeToString(sum[:])
		}
	}
	return m
}

// Mask rewrites src, replacing the values of selected fields. All new
// mappings required by src are committed to the store in one atomic
// transaction; on any error the transaction is discarded and nothing is
// published. Bytes outside selected fields are copied verbatim.
func (m *Masker) Mask(src []byte) ([]byte, error) {
	for attempt := 0; attempt < maxCommitRetries; attempt++ {
		table, err := m.store.Load()
		if err != nil {
			return nil, fmt.Errorf("mask: load mapping table: %v", err)
		}
		tx := newTx(table.Entries)
		out, err := m.rewrite(src, tx)
		if err != nil {
			return nil, err
		}
		if !tx.dirty {
			return out, nil
		}
		err = m.store.Commit(table.Version, Table{Entries: tx.entries})
		if err == ErrConflict {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("mask: commit mapping table: %v", err)
		}
		return out, nil
	}
	return nil, errors.New("mask: mapping table keeps changing concurrently")
}

// tx accumulates the mappings needed by one document. It is published
// atomically by Store.Commit or discarded entirely.
type tx struct {
	entries map[string]string // type-tagged original -> masked JSON literal
	reverse map[string]string // masked JSON literal -> type-tagged original
	dirty   bool
}

func newTx(entries map[string]string) *tx {
	t := &tx{
		entries: make(map[string]string, len(entries)),
		reverse: make(map[string]string, len(entries)),
	}
	for k, v := range entries {
		t.entries[k] = v
		t.reverse[v] = k
	}
	return t
}

// maskedLiteral returns the masked JSON literal for orig, allocating and
// recording a new mapping in the transaction when necessary.
func (m *Masker) maskedLiteral(kind, orig string, t *tx) (string, error) {
	key := kind + ":" + orig
	if lit, ok := t.entries[key]; ok {
		return lit, nil
	}
	// Re-hash the digest output so any injected digest (including
	// non-hex or short constants) still yields a uniform hex seed.
	sum := sha256.Sum256([]byte(m.digest(key)))
	d := hex.EncodeToString(sum[:])
	base := candidate(kind, orig, d)
	lit := base
	for n := 0; ; n++ {
		if n > 0 {
			if kind == kindBool {
				if n > 1 {
					return "", fmt.Errorf("mask: no free boolean value for %q", orig)
				}
				lit = flipBool(base)
			} else {
				lit = suffix(kind, base, n)
			}
		}
		owner, taken := t.reverse[lit]
		if !taken || owner == key {
			break
		}
	}
	t.entries[key] = lit
	t.reverse[lit] = key
	t.dirty = true
	return lit, nil
}

// candidate builds the first-choice masked literal for a value from its
// 64-character hex seed.
func candidate(kind, orig, d string) string {
	switch kind {
	case kindString:
		return `"tok_` + d[:12] + `"`
	case kindBool:
		if d[0] < '8' {
			return "false"
		}
		return "true"
	case kindNumber:
		return numberCandidate(orig, d)
	}
	return ""
}

// numberCandidate derives a replacement number preserving the original's
// integer/float nature and sign.
func numberCandidate(orig, d string) string {
	isFloat := strings.ContainsAny(orig, ".eE")
	neg := strings.HasPrefix(orig, "-")
	digits := 0
	for i := 0; i < len(orig); i++ {
		c := orig[i]
		if c >= '0' && c <= '9' {
			digits++
		} else if c != '-' {
			break
		}
	}
	if digits < 1 {
		digits = 1
	}
	if digits > 15 {
		digits = 15
	}
	mod := uint64(1)
	for i := 0; i < digits; i++ {
		mod *= 10
	}
	n, _ := strconv.ParseUint(d[:12], 16, 64)
	n %= mod
	intPart := strconv.FormatUint(n, 10)
	if !isFloat {
		if neg && n != 0 {
			return "-" + intPart
		}
		return intPart
	}
	f, _ := strconv.ParseUint(d[12:20], 16, 64)
	frac := f % 1000000
	s := intPart + "." + fmt.Sprintf("%06d", frac)
	if neg && (n != 0 || frac != 0) {
		return "-" + s
	}
	return s
}

// suffix derives the n-th collision-resolution variant of base, keeping
// the literal's JSON type: strings gain a "#n" marker, numbers gain
// decimal digits.
func suffix(kind, base string, n int) string {
	if kind == kindNumber {
		if base == "0" {
			return strconv.Itoa(n)
		}
		return base + strconv.Itoa(n)
	}
	unq := strings.TrimSuffix(strings.TrimPrefix(base, `"`), `"`)
	return `"` + unq + "#" + strconv.Itoa(n) + `"`
}

func flipBool(base string) string {
	if base == "true" {
		return "false"
	}
	return "true"
}
