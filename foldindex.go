// Package foldindex builds a search index over caller-supplied text using a
// caller-supplied one-to-many character folding table, and maps folded-space
// matches back to intervals of the original text.
//
// The folding table maps a character (or a multi-character sequence) to its
// folded replacement, which may be empty, one, or many characters. Documents
// and queries are folded with the same table; matches found in folded space
// are translated back to the minimal original byte interval covering the
// match. A hit that does not align with folding-token boundaries (i.e. it
// covers only part of an expansion) is reported with Exact == false.
//
// Each table has a content-derived version. Rebuilding an index with a new
// table produces a new index generation; cursors issued by a previous
// generation are rejected with ErrStaleCursor and must not be mixed in.
package foldindex

import (
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// ErrStaleCursor is returned when a cursor was issued by an index with a
// different table version or generation and cannot be used here.
var ErrStaleCursor = errors.New("foldindex: cursor belongs to a different table version or index generation")

// ErrEmptyQuery is returned when the query folds to an empty string.
var ErrEmptyQuery = errors.New("foldindex: query is empty after folding")

// Table is a caller-provided character folding table. Keys are one or more
// characters (longest match wins at each position); values are the folded
// replacements and may be empty (deletion) or longer than the key
// (one-to-many expansion).
type Table struct {
	rules   map[string]string
	byFirst map[rune][]string // keys grouped by first rune, longest first
	version string
}

// NewTable builds a folding table from rules and derives its version.
func NewTable(rules map[string]string) (*Table, error) {
	t := &Table{rules: make(map[string]string, len(rules)), byFirst: map[rune][]string{}}
	for k, v := range rules {
		if k == "" {
			return nil, fmt.Errorf("foldindex: folding rule has an empty key")
		}
		if _, dup := t.rules[k]; dup {
			return nil, fmt.Errorf("foldindex: duplicate folding rule for %q", k)
		}
		t.rules[k] = v
		r, _ := utf8.DecodeRuneInString(k)
		t.byFirst[r] = append(t.byFirst[r], k)
	}
	for _, keys := range t.byFirst {
		sort.Slice(keys, func(i, j int) bool { return len(keys[i]) > len(keys[j]) })
	}

	keys := make([]string, 0, len(t.rules))
	for k := range t.rules {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	h := sha1.New()
	for _, k := range keys {
		h.Write([]byte(k))
		h.Write([]byte{0})
		h.Write([]byte(t.rules[k]))
		h.Write([]byte{0xff})
	}
	t.version = hex.EncodeToString(h.Sum(nil))[:12]
	return t, nil
}

// Version returns the content-derived version of the table. Any change to
// the rules produces a new version.
func (t *Table) Version() string { return t.version }

// span is a half-open byte interval [start, end) in the original text.
type span struct{ start, end int }

// fold applies the table to s and returns the folded text plus, for every
// folded byte, the original interval it came from and whether the byte
// starts a folding token.
func (t *Table) fold(s string) (folded string, spans []span, tokStart []bool) {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		matched, repl := s[i:i+size], s[i:i+size]
		for _, k := range t.byFirst[r] {
			if strings.HasPrefix(s[i:], k) {
				matched, repl = k, t.rules[k]
				break
			}
		}
		for j := 0; j < len(repl); j++ {
			spans = append(spans, span{i, i + len(matched)})
			tokStart = append(tokStart, j == 0)
		}
		b.WriteString(repl)
		i += len(matched)
	}
	return b.String(), spans, tokStart
}

type doc struct {
	orig     string
	folded   string
	spans    []span
	tokStart []bool
}

// Index is a folded search index bound to one table version and generation.
type Index struct {
	table *Table
	gen   uint64
	docs  map[string]*doc
	order []string // document IDs, sorted
}

// NewIndex creates an empty index (generation 0) for the given table.
func NewIndex(t *Table) *Index {
	return &Index{table: t, docs: map[string]*doc{}}
}

// Table returns the folding table the index was built with.
func (ix *Index) Table() *Table { return ix.table }

// Generation returns the index generation. It increments on every Reindex.
func (ix *Index) Generation() uint64 { return ix.gen }

// Add indexes or replaces a document under id.
func (ix *Index) Add(id, text string) {
	if _, ok := ix.docs[id]; !ok {
		ix.order = append(ix.order, id)
		sort.Strings(ix.order)
	}
	folded, spans, tokStart := ix.table.fold(text)
	ix.docs[id] = &doc{orig: text, folded: folded, spans: spans, tokStart: tokStart}
}

// Reindex builds a new index from the same documents with a new table.
// The new index has an incremented generation, so cursors issued by the
// receiver are rejected by the new index and vice versa.
func (ix *Index) Reindex(t *Table) *Index {
	next := NewIndex(t)
	next.gen = ix.gen + 1
	for _, id := range ix.order {
		next.Add(id, ix.docs[id].orig)
	}
	return next
}

// Hit is a search result mapped back to the original text.
type Hit struct {
	DocID string
	Start int  // byte offset of the covering interval in the original text
	End   int  // exclusive
	Exact bool // true if the folded match aligns exactly with folding-token boundaries
}

// Text returns the original text covered by the hit.
func (h Hit) Text(ix *Index) string {
	d := ix.docs[h.DocID]
	if d == nil {
		return ""
	}
	return d.orig[h.Start:h.End]
}

// Search folds query with the index table, finds all occurrences (including
// overlapping ones), maps them back to original intervals, and merges
// duplicate intervals into a single hit. Results are ordered by document ID
// then by start offset.
//
// limit caps the number of returned hits (limit <= 0 means no limit). When
// more hits remain, the returned cursor is non-empty and can be passed to
// the next call. A cursor is only valid for the index generation and table
// version that issued it; otherwise ErrStaleCursor is returned.
func (ix *Index) Search(query string, limit int, cursor string) (hits []Hit, next string, err error) {
	offset := 0
	if cursor != "" {
		c, err := decodeCursor(cursor)
		if err != nil {
			return nil, "", err
		}
		if c.version != ix.table.version || c.gen != ix.gen {
			return nil, "", ErrStaleCursor
		}
		offset = c.offset
	}

	fq, _, _ := ix.table.fold(query)
	if fq == "" {
		return nil, "", ErrEmptyQuery
	}

	all := ix.searchAll(fq)
	if offset > len(all) {
		offset = len(all)
	}
	end := len(all)
	if limit > 0 && offset+limit < end {
		end = offset + limit
	}
	if end < len(all) {
		next = encodeCursor(ix.table.version, ix.gen, end)
	}
	return all[offset:end], next, nil
}

func (ix *Index) searchAll(fq string) []Hit {
	var hits []Hit
	for _, id := range ix.order {
		d := ix.docs[id]
		for pos := 0; pos+len(fq) <= len(d.folded); {
			i := strings.Index(d.folded[pos:], fq)
			if i < 0 {
				break
			}
			s := pos + i
			e := s + len(fq)
			exact := d.tokStart[s] && (e == len(d.folded) || d.tokStart[e])
			hits = append(hits, Hit{
				DocID: id,
				Start: d.spans[s].start,
				End:   d.spans[e-1].end,
				Exact: exact,
			})
			pos = s + 1 // allow overlapping matches
		}
	}
	return mergeHits(hits)
}

// mergeHits merges hits that map to the same original interval. A merged
// hit is exact only if every contributing hit was exact.
func mergeHits(hits []Hit) []Hit {
	type key struct {
		doc        string
		start, end int
	}
	seen := make(map[key]int, len(hits))
	out := hits[:0]
	for _, h := range hits {
		k := key{h.DocID, h.Start, h.End}
		if i, ok := seen[k]; ok {
			out[i].Exact = out[i].Exact && h.Exact
			continue
		}
		seen[k] = len(out)
		out = append(out, h)
	}
	return out
}

const cursorPrefix = "fi1"

type cursorState struct {
	version string
	gen     uint64
	offset  int
}

func encodeCursor(version string, gen uint64, offset int) string {
	return fmt.Sprintf("%s:%s:%d:%d", cursorPrefix, version, gen, offset)
}

func decodeCursor(s string) (cursorState, error) {
	var c cursorState
	parts := strings.Split(s, ":")
	if len(parts) != 4 || parts[0] != cursorPrefix {
		return c, fmt.Errorf("foldindex: malformed cursor %q", s)
	}
	c.version = parts[1]
	gen, err := strconv.ParseUint(parts[2], 10, 64)
	if err != nil {
		return c, fmt.Errorf("foldindex: malformed cursor %q", s)
	}
	offset, err := strconv.Atoi(parts[3])
	if err != nil || offset < 0 {
		return c, fmt.Errorf("foldindex: malformed cursor %q", s)
	}
	c.gen, c.offset = gen, offset
	return c, nil
}
