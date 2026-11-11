// Package offlinecli provides a log SDK building block that scrubs
// dynamically registered multi-byte secrets out of a byte stream before the
// data reaches an underlying sink.
package offlinecli

import (
	"errors"
	"io"
	"sort"
	"sync"
)

// ErrClosed is returned when Write is called after Close.
var ErrClosed = errors.New("scrubber: writer is closed")

// trieNode is one node of the secret-matching prefix trie. A node is a match
// terminal when replacement is non-nil.
type trieNode struct {
	children    map[byte]*trieNode
	replacement []byte // non-nil means a secret ends here
}

func newTrieNode() *trieNode {
	return &trieNode{children: map[byte]*trieNode{}}
}

// Dictionary maps byte-sequence secrets to replacement strings.
//
// A Dictionary is safe for concurrent use through a Scrubber because every
// Scrubber operation holds the Scrubber mutex while touching it. A Dictionary
// shared between multiple Scrubbers must be guarded by the caller.
type Dictionary struct {
	secrets map[string]string
	root    *trieNode
}

// NewDictionary creates a Dictionary from a secret -> replacement map.
func NewDictionary(secrets map[string]string) *Dictionary {
	dict := &Dictionary{
		secrets: make(map[string]string, len(secrets)),
		root:    newTrieNode(),
	}
	keys := make([]string, 0, len(secrets))
	for secret := range secrets {
		keys = append(keys, secret)
	}
	sort.Strings(keys)
	for _, secret := range keys {
		dict.add(secret, secrets[secret])
	}
	return dict
}

// AddSecret registers or updates a secret. Empty secrets are rejected because
// matching the empty byte sequence is meaningless.
func (d *Dictionary) AddSecret(secret, replacement string) error {
	if len(secret) == 0 {
		return errors.New("scrubber: secret must not be empty")
	}
	d.secrets[secret] = replacement
	d.add(secret, replacement)
	return nil
}

func (d *Dictionary) add(secret, replacement string) {
	node := d.root
	for i := 0; i < len(secret); i++ {
		next := node.children[secret[i]]
		if next == nil {
			next = newTrieNode()
			node.children[secret[i]] = next
		}
		node = next
	}
	node.replacement = []byte(replacement)
}

// RemoveSecret unregisters a secret and rebuilds the trie. It reports whether
// the secret was present.
func (d *Dictionary) RemoveSecret(secret string) bool {
	if _, ok := d.secrets[secret]; !ok {
		return false
	}
	delete(d.secrets, secret)
	rebuilt := NewDictionary(d.secrets)
	d.root = rebuilt.root
	return true
}

// scanResult classifies one scan starting at the beginning of a buffer.
type scanResult int

const (
	// scanNone: no secret is a prefix of the buffer; emit the first byte.
	scanNone scanResult = iota
	// scanFull: a complete secret matched and was consumed from the buffer.
	scanFull
	// scanUndecided: the whole buffer is a prefix of some secret; keep it.
	scanUndecided
)

// scan judges the first bytes of buf under dictionary d. ownEnd is the
// exclusive end of the era that owns the first byte; bytes at or beyond it
// are merely borrowed to let an undecided cached prefix become decidable.
//
// A complete match whose trie node still has children is held back as a
// candidate while longer secrets sharing the start remain possible. The
// candidate is emitted once extension is disproved or the stream ends.
func (d *Dictionary) scan(buf []byte, ownEnd int, final bool, emit func([]byte) error) ([]byte, scanResult, error) {
	node := d.root
	matchDepth := 0
	depth := 0
	var replacement []byte
	for i := 0; i < len(buf); i++ {
		next := node.children[buf[i]]
		if next == nil {
			if i >= ownEnd && ownEnd > 0 {
				// The mismatch happened on a borrowed byte: the entire owning
				// era stayed a non-matching prefix and is released verbatim;
				// borrowed bytes go back to their own era untouched.
				if matchDepth > 0 {
					if err := emit(replacement); err != nil {
						return nil, scanFull, err
					}
					return buf[matchDepth:], scanFull, nil
				}
				if err := emit(buf[:ownEnd]); err != nil {
					return nil, scanNone, err
				}
				return buf[ownEnd:], scanNone, nil
			}
			if matchDepth > 0 {
				// Same-era mismatch after a complete candidate: the longest
				// secret at this start is now decidable.
				if err := emit(replacement); err != nil {
					return nil, scanFull, err
				}
				return buf[matchDepth:], scanFull, nil
			}
			// No secret starts here; the first byte is safe.
			if err := emit(buf[:1]); err != nil {
				return nil, scanNone, err
			}
			return buf[1:], scanNone, nil
		}
		depth = i + 1
		node = next
		if node.replacement != nil {
			// Same starting position; the deepest terminal wins eventually.
			matchDepth = i + 1
			replacement = node.replacement
		}
	}
	switch {
	case matchDepth > 0 && (final || len(node.children) == 0):
		// At stream end (or at a trie leaf) no longer secret can extend the
		// candidate, so the deepest terminal is the final answer.
		if err := emit(replacement); err != nil {
			return nil, scanFull, err
		}
		return buf[matchDepth:], scanFull, nil
	case !final && depth == len(buf) && len(node.children) > 0:
		// The whole buffer walked the trie and more bytes may still arrive.
		if ownEnd == len(buf) {
			// Every buffered byte belongs to the owning era: a future write
			// of the same dictionary may still complete the prefix.
			return buf, scanUndecided, nil
		}
		// The owning era is fully consumed and the remaining bytes were
		// borrowed without completing a secret: release the owning era
		// verbatim and hand the borrowed bytes back to their own era.
		if err := emit(buf[:ownEnd]); err != nil {
			return nil, scanNone, err
		}
		return buf[ownEnd:], scanNone, nil
	case matchDepth > 0 && !final:
		// A candidate matched, the node is still extendable, and more bytes
		// of the same era may arrive later.
		return buf, scanUndecided, nil
	default:
		// No candidate matched. If the owning era is fully consumed and the
		// remaining bytes were borrowed, release the whole owning era; at
		// stream end an incomplete prefix is emitted verbatim as well.
		if ownEnd < len(buf) {
			if err := emit(buf[:ownEnd]); err != nil {
				return nil, scanNone, err
			}
			return buf[ownEnd:], scanNone, nil
		}
		// Otherwise the first byte is simply safe.
		if err := emit(buf[:1]); err != nil {
			return nil, scanNone, err
		}
		return buf[1:], scanNone, nil
	}
}

// era marks which dictionary version a byte belongs to.
type era struct {
	start int // byte offset relative to the current pending buffer;
	// negative values mean the boundary sits before the buffer start.
	dict *Dictionary
}

// Scrubber is an io.WriteCloser that replaces registered secrets in the data
// written to it before forwarding the scrubbed bytes to an underlying writer.
type Scrubber struct {
	mu sync.Mutex

	underlying io.WriteCloser

	// pending holds bytes that have not been judged yet. eraBounds records
	// dictionary-version boundaries inside pending.
	pending   []byte
	eraBounds []era

	closed   bool
	writeErr error // latched once an underlying write fails
	closeErr error
}

var _ io.WriteCloser = (*Scrubber)(nil)

// NewScrubber wraps underlying. If dict is nil a fresh empty Dictionary is
// created so that AddSecret keeps working.
func NewScrubber(underlying io.WriteCloser, dict *Dictionary) *Scrubber {
	if dict == nil {
		dict = NewDictionary(nil)
	}
	return &Scrubber{
		underlying: underlying,
		eraBounds:  []era{{start: 0, dict: dict}},
	}
}

// Write appends p to the internal pending buffer and emits every byte that can
// already be judged. It returns len(p), nil unless the underlying writer fails
// or the scrubber is closed; buffered bytes are never counted as lost.
func (s *Scrubber) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return 0, ErrClosed
	}
	if s.writeErr != nil {
		return 0, s.writeErr
	}

	s.pending = append(s.pending, p...)
	if err := s.processLocked(); err != nil {
		return 0, err
	}
	return len(p), nil
}

// Close flushes every still-buffered byte and closes the underlying writer.
// At stream end a held-back candidate match is emitted as its replacement;
// a prefix that never completes a secret is emitted verbatim. Repeated calls
// are safe and return the same result without emitting or closing again.
func (s *Scrubber) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return s.closeErr
	}

	if s.writeErr == nil {
		if err := s.drainLocked(); err != nil {
			s.writeErr = err
			s.closeErr = err
			return err
		}
	}

	err := s.underlying.Close()
	s.closed = true
	s.closeErr = err
	return err
}

// AddSecret registers a secret on the current dictionary. Trie additions only
// widen the set of matches, so bytes already buffered are still judged
// correctly; new bytes immediately see the new secret.
func (s *Scrubber) AddSecret(secret, replacement string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	return s.currentDictLocked().AddSecret(secret, replacement)
}

// RemoveSecret unregisters a secret from the current dictionary.
func (s *Scrubber) RemoveSecret(secret string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	if !s.currentDictLocked().RemoveSecret(secret) {
		return errors.New("scrubber: secret not registered")
	}
	return nil
}

// RotateSecret replaces the whole dictionary, implementing key rotation.
//
// Bytes already buffered stay tied to the old dictionary until their prefix
// becomes decidable; only bytes written after the rotation are judged under
// the new dictionary. A nil dict installs a fresh empty dictionary.
func (s *Scrubber) RotateSecret(dict *Dictionary) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.writeErr != nil {
		return
	}
	if dict == nil {
		dict = NewDictionary(nil)
	}
	// The new boundary starts exactly where the old pending buffer ends, so
	// cached prefixes keep their original dictionary until they are judged.
	// If no bytes arrived since the last rotation, the unused last boundary
	// is replaced instead of stacking empty eras.
	last := &s.eraBounds[len(s.eraBounds)-1]
	if last.start == len(s.pending) {
		last.dict = dict
	} else {
		s.eraBounds = append(s.eraBounds, era{start: len(s.pending), dict: dict})
	}
}

func (s *Scrubber) currentDictLocked() *Dictionary {
	return s.eraBounds[len(s.eraBounds)-1].dict
}

// processLocked emits every byte that can be judged. The remainder stays in
// pending and is always a prefix of at least one secret in its era's
// dictionary (or empty).
func (s *Scrubber) processLocked() error {
	return s.runLocked(false)
}

// drainLocked is the final, terminating pass used by Close: nothing more can
// arrive, so held-back candidate matches are emitted as replacements and any
// genuinely incomplete prefix is emitted verbatim.
func (s *Scrubber) drainLocked() error {
	return s.runLocked(true)
}

func (s *Scrubber) runLocked(final bool) error {
	for len(s.pending) > 0 {
		// The dictionary is chosen by the era of the first buffered byte.
		// A still-undecided cached prefix may borrow later-era bytes until it
		// becomes decidable; borrowed bytes that do not complete an old-era
		// secret are returned to their own era for re-judgement.
		dict := s.dictAtLocked(0)
		ownEnd := len(s.pending)
		if len(s.eraBounds) > 1 {
			ownEnd = s.eraBounds[1].start
		}

		remainder, result, err := dict.scan(s.pending, ownEnd, final, s.emitLocked)
		if err != nil {
			s.writeErr = err
			return err
		}
		if result == scanUndecided {
			return nil
		}

		consumed := len(s.pending) - len(remainder)
		next := append([]byte(nil), remainder...)
		s.consumeLocked(consumed)
		s.pending = next
	}
	return nil
}

// consumeLocked drops the first n bytes from pending and shifts every era
// boundary so offsets stay relative to the new buffer start. Boundaries that
// cross before the buffer start keep a negative offset instead of collapsing
// onto 0, otherwise fully consumed eras would swallow later eras.
func (s *Scrubber) consumeLocked(n int) {
	for i := range s.eraBounds {
		s.eraBounds[i].start -= n
	}
	// The first boundary must always describe the era of the first buffered
	// byte. Drop every boundary that starts before the buffer, then anchor the
	// first survivor at offset 0.
	first := 0
	for first < len(s.eraBounds) && s.eraBounds[first].start < 0 {
		first++
	}
	if first == len(s.eraBounds) {
		first = len(s.eraBounds) - 1
	}
	s.eraBounds = s.eraBounds[first:]
	s.eraBounds[0].start = 0
}

// dictAtLocked returns the dictionary that governs the byte at offset pos.
func (s *Scrubber) dictAtLocked(pos int) *Dictionary {
	dict := s.eraBounds[0].dict
	for _, bound := range s.eraBounds {
		if bound.start <= pos {
			dict = bound.dict
		} else {
			break
		}
	}
	return dict
}

// emitLocked forwards one emitted chunk to the underlying writer, retrying on
// short writes until the whole chunk is accepted.
func (s *Scrubber) emitLocked(chunk []byte) error {
	for len(chunk) > 0 {
		n, err := s.underlying.Write(chunk)
		if err != nil {
			return err
		}
		if n <= 0 {
			return errors.New("scrubber: underlying writer accepted no bytes")
		}
		if n > len(chunk) {
			n = len(chunk)
		}
		chunk = chunk[n:]
	}
	return nil
}
