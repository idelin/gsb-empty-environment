// Package mask deterministically replaces specified fields in structured
// JSON documents. Equal original values map to equal tokens across
// documents; types and nulls are preserved; collisions on the digest are
// resolved by suffixes allocated through a persistent mapping table.
package mask

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
)

var (
	errSyntax = errors.New("mask: invalid JSON document")
	errEOF    = errors.New("mask: unexpected end of JSON document")
)

// DigestFunc computes the deterministic digest of an original value.
type DigestFunc func(value string) string

// Options configures a Masker.
type Options struct {
	// Fields lists dot-separated field paths to mask. The segment "*"
	// matches any single object key. Array elements are traversed
	// transparently, so "users.email" matches every element's email.
	Fields []string
	// Salt is mixed into the default digest.
	Salt string
	// Digest overrides the digest function (SHA-256 by default). Tests may
	// inject a constant digest to force collisions.
	Digest DigestFunc
}

// Masker masks documents against a shared Store. Allocations made by
// MaskDocument are staged in the Store's transaction and published by
// Store.Commit.
type Masker struct {
	store    *Store
	patterns [][]string
	digest   DigestFunc
}

// New creates a Masker writing allocations to store.
func New(store *Store, opts Options) *Masker {
	d := opts.Digest
	if d == nil {
		salt := opts.Salt
		d = func(v string) string {
			sum := sha256.Sum256([]byte(salt + "\x00" + v))
			return hex.EncodeToString(sum[:])
		}
	}
	var patterns [][]string
	for _, f := range opts.Fields {
		if f == "" {
			continue
		}
		patterns = append(patterns, strings.Split(f, "."))
	}
	return &Masker{store: store, patterns: patterns, digest: d}
}

// MaskDocument returns doc with the configured string fields replaced by
// deterministic tokens. Bytes outside masked string values are copied
// unchanged, so unspecified fields remain byte-identical, and nulls and
// non-string scalars keep their type and value.
func (m *Masker) MaskDocument(doc []byte) ([]byte, error) {
	sc := &scanner{src: doc, m: m}
	pos, err := sc.value(0, nil)
	if err != nil {
		return nil, err
	}
	if sc.skipWS(pos) != len(doc) {
		return nil, errSyntax
	}
	if len(sc.spans) == 0 {
		out := make([]byte, len(doc))
		copy(out, doc)
		return out, nil
	}
	var out []byte
	last := 0
	for _, sp := range sc.spans {
		out = append(out, doc[last:sp.start]...)
		value := unquoteJSON(doc[sp.start-1 : sp.end+1])
		out = append(out, m.tokenFor(value)...)
		last = sp.end
	}
	out = append(out, doc[last:]...)
	return out, nil
}

func (m *Masker) tokenFor(value string) string {
	if tok, ok := m.store.Lookup(value); ok {
		return tok
	}
	return m.store.Allocate(value, "msk_"+sanitize(m.digest(value)))
}

func (m *Masker) matches(path []string) bool {
	for _, p := range m.patterns {
		if len(p) != len(path) {
			continue
		}
		ok := true
		for i := range p {
			if p[i] != "*" && p[i] != path[i] {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

func sanitize(s string) string {
	var b []byte
	for i := 0; i < len(s) && len(b) < 24; i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
			b = append(b, c)
		case c >= 'A' && c <= 'Z':
			b = append(b, c+'a'-'A')
		default:
			b = append(b, '-')
		}
	}
	if len(b) == 0 {
		return "0"
	}
	return string(b)
}

func unquoteJSON(raw []byte) string {
	v, err := strconv.Unquote(string(raw))
	if err != nil {
		return string(raw[1 : len(raw)-1])
	}
	return v
}

type span struct{ start, end int }

type scanner struct {
	src   []byte
	m     *Masker
	spans []span
}

func (s *scanner) skipWS(pos int) int {
	for pos < len(s.src) {
		switch s.src[pos] {
		case ' ', '\t', '\n', '\r':
			pos++
		default:
			return pos
		}
	}
	return pos
}

func (s *scanner) value(pos int, path []string) (int, error) {
	pos = s.skipWS(pos)
	if pos >= len(s.src) {
		return 0, errEOF
	}
	switch c := s.src[pos]; {
	case c == '{':
		return s.object(pos, path)
	case c == '[':
		return s.array(pos, path)
	case c == '"':
		end, err := scanString(s.src, pos)
		if err != nil {
			return 0, err
		}
		if s.m.matches(path) {
			s.spans = append(s.spans, span{pos + 1, end - 1})
		}
		return end, nil
	case c == 't':
		return s.literal(pos, "true")
	case c == 'f':
		return s.literal(pos, "false")
	case c == 'n':
		return s.literal(pos, "null")
	default:
		end := scanNumber(s.src, pos)
		if end == pos {
			return 0, errSyntax
		}
		return end, nil
	}
}

func (s *scanner) literal(pos int, lit string) (int, error) {
	if !bytes.HasPrefix(s.src[pos:], []byte(lit)) {
		return 0, errSyntax
	}
	return pos + len(lit), nil
}

func (s *scanner) object(pos int, path []string) (int, error) {
	pos++
	pos = s.skipWS(pos)
	if pos < len(s.src) && s.src[pos] == '}' {
		return pos + 1, nil
	}
	for {
		pos = s.skipWS(pos)
		if pos >= len(s.src) {
			return 0, errEOF
		}
		if s.src[pos] != '"' {
			return 0, errSyntax
		}
		kend, err := scanString(s.src, pos)
		if err != nil {
			return 0, err
		}
		key := unquoteJSON(s.src[pos:kend])
		pos = s.skipWS(kend)
		if pos >= len(s.src) || s.src[pos] != ':' {
			return 0, errSyntax
		}
		child := make([]string, len(path)+1)
		copy(child, path)
		child[len(path)] = key
		pos, err = s.value(pos+1, child)
		if err != nil {
			return 0, err
		}
		pos = s.skipWS(pos)
		if pos >= len(s.src) {
			return 0, errEOF
		}
		switch s.src[pos] {
		case ',':
			pos++
		case '}':
			return pos + 1, nil
		default:
			return 0, errSyntax
		}
	}
}

func (s *scanner) array(pos int, path []string) (int, error) {
	pos++
	pos = s.skipWS(pos)
	if pos < len(s.src) && s.src[pos] == ']' {
		return pos + 1, nil
	}
	for {
		var err error
		pos, err = s.value(pos, path)
		if err != nil {
			return 0, err
		}
		pos = s.skipWS(pos)
		if pos >= len(s.src) {
			return 0, errEOF
		}
		switch s.src[pos] {
		case ',':
			pos++
		case ']':
			return pos + 1, nil
		default:
			return 0, errSyntax
		}
	}
}

func scanString(src []byte, pos int) (int, error) {
	i := pos + 1
	for i < len(src) {
		switch src[i] {
		case '\\':
			i += 2
			continue
		case '"':
			return i + 1, nil
		}
		i++
	}
	return 0, errEOF
}

func scanNumber(src []byte, pos int) int {
	i := pos
	for i < len(src) {
		switch c := src[i]; {
		case c >= '0' && c <= '9', c == '-', c == '+', c == '.', c == 'e', c == 'E':
			i++
		default:
			return i
		}
	}
	return i
}
