// Package desensitize provides streaming, dictionary-based log masking.
//
// The masker scans input left to right and replaces dictionary entries
// using longest-match (maximal munch) semantics. Matches never overlap:
// once a span is consumed by a match, scanning resumes after it. When two
// entries share the same pattern, the entry with the smaller ID wins.
//
// Input is streamed: a byte is only committed to the output once no future
// input could extend a match covering it. Dictionary versions are swapped
// with SwitchVersion, which takes effect at an explicit byte boundary.
package desensitize

import "errors"

// ErrEmptyPattern is returned by NewVersion when an entry has an empty pattern.
var ErrEmptyPattern = errors.New("desensitize: entry pattern must not be empty")

// Entry is a single dictionary item. Pattern is the byte sequence to find,
// Replacement is what it is masked with, and ID breaks ties between
// identical patterns (smaller ID wins).
type Entry struct {
	ID          int64
	Pattern     string
	Replacement string
}

// Version is an immutable dictionary snapshot. Build one per dictionary
// version and hand it to a Masker; versions are safe for concurrent use.
type Version struct {
	root *node
}

type node struct {
	children    map[byte]*node
	terminal    bool
	entryID     int64
	replacement string
}

// NewVersion builds a dictionary version from entries. Duplicate patterns
// are resolved by keeping the entry with the smaller ID.
func NewVersion(entries []Entry) (*Version, error) {
	root := &node{}
	for _, e := range entries {
		if e.Pattern == "" {
			return nil, ErrEmptyPattern
		}
		cur := root
		for i := 0; i < len(e.Pattern); i++ {
			b := e.Pattern[i]
			if cur.children == nil {
				cur.children = make(map[byte]*node)
			}
			nxt, ok := cur.children[b]
			if !ok {
				nxt = &node{}
				cur.children[b] = nxt
			}
			cur = nxt
		}
		if !cur.terminal || e.ID < cur.entryID {
			cur.terminal = true
			cur.entryID = e.ID
			cur.replacement = e.Replacement
		}
	}
	return &Version{root: root}, nil
}

// decision is the outcome of trying to decide the fate of buf[0].
type decision struct {
	matchLen int    // length of the winning match at buf[0]; 0 means literal byte
	entryID  int64  // winning entry ID when matchLen > 0
	repl     string // replacement for the winning match
	pending  bool   // true when more input could still extend a match
}

// decide reports how buf[0] must be emitted. With flush set, the buffered
// bytes are treated as end of input; otherwise a live trie path at the end
// of the buffer means the decision must wait for more bytes.
func (v *Version) decide(buf []byte, flush bool) decision {
	cur := v.root
	best := 0
	var bestID int64
	var bestRepl string
	for i := 0; i < len(buf); i++ {
		nxt, ok := cur.children[buf[i]]
		if !ok {
			// Dead end: no match starting at buf[0] can grow past i,
			// so the best terminal seen so far is final.
			return decision{matchLen: best, entryID: bestID, repl: bestRepl}
		}
		cur = nxt
		if cur.terminal {
			best = i + 1
			bestID = cur.entryID
			bestRepl = cur.replacement
		}
	}
	// Consumed every buffered byte along a live path: later input might
	// still extend the match, so only commit when flushing.
	if !flush && len(cur.children) > 0 {
		return decision{pending: true}
	}
	return decision{matchLen: best, entryID: bestID, repl: bestRepl}
}
