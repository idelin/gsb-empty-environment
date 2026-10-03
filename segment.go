// Package invindex implements a compact, read-only inverted index segment
// for text keyword search.
//
// Layout of a segment:
//
//	Dictionary: terms sorted lexicographically, grouped into fixed-size
//	blocks. Each block's first term is stored in a sparse block index;
//	inside a block every entry stores only the prefix length shared with
//	the previous term plus its suffix (prefix-compressed dictionary).
//
//	Posting lists: document IDs are delta-encoded and, together with the
//	term frequencies, stored as varints. Every sqrt(n) postings a skip
//	pointer records (absolute docID, byte offset) so intersections can
//	jump over useless regions of long lists.
package invindex

import (
	"encoding/binary"
	"math"
	"sort"
	"strings"
)

// dictBlockSize is the number of dictionary entries per prefix-compression
// block. The first term of each block is stored in full in the block index.
const dictBlockSize = 16

// Builder collects documents and produces an immutable Segment.
type Builder struct {
	terms   map[string]map[uint64]uint32 // term -> docID -> frequency
	numDocs uint64
}

// NewBuilder returns an empty Builder.
func NewBuilder() *Builder {
	return &Builder{terms: make(map[string]map[uint64]uint32)}
}

// AddDocument tokenizes text (lowercase alphanumeric tokens) and adds every
// token occurrence of docID to the index.
func (b *Builder) AddDocument(docID uint64, text string) {
	for _, tok := range strings.FieldsFunc(strings.ToLower(text), isSep) {
		b.AddTerm(docID, tok)
	}
	if docID >= b.numDocs {
		b.numDocs = docID + 1
	}
}

// AddTerm records one occurrence of term in docID.
func (b *Builder) AddTerm(docID uint64, term string) {
	m := b.terms[term]
	if m == nil {
		m = make(map[uint64]uint32)
		b.terms[term] = m
	}
	m[docID]++
	if docID >= b.numDocs {
		b.numDocs = docID + 1
	}
}

func isSep(r rune) bool {
	return !('a' <= r && r <= 'z') && !('0' <= r && r <= '9')
}

// Segment is an immutable, compact inverted index.
type Segment struct {
	dictData []byte      // prefix-compressed dictionary entries
	blocks   []dictBlock // sparse index: first term of each block
	blob     []byte      // all posting lists, back to back
	numDocs  uint64
	numTerms int
}

type dictBlock struct {
	term   string
	offset int
}

// Build freezes the collected data into a read-only Segment.
func (b *Builder) Build() *Segment {
	terms := make([]string, 0, len(b.terms))
	for t := range b.terms {
		terms = append(terms, t)
	}
	sort.Strings(terms)

	seg := &Segment{numDocs: b.numDocs, numTerms: len(terms)}
	prevTerm := ""
	for i, t := range terms {
		shared := 0
		if i%dictBlockSize == 0 {
			seg.blocks = append(seg.blocks, dictBlock{term: t, offset: len(seg.dictData)})
		} else {
			shared = commonPrefix(prevTerm, t)
		}

		// Encode the posting list into the blob.
		docs := sortedDocs(b.terms[t])
		plOff := len(seg.blob)
		seg.blob = encodePostingList(seg.blob, docs, b.terms[t])

		// Dictionary entry: shared prefix len, suffix, posting list location.
		seg.dictData = appendUvarint(seg.dictData, uint64(shared))
		seg.dictData = appendUvarint(seg.dictData, uint64(len(t)-shared))
		seg.dictData = append(seg.dictData, t[shared:]...)
		seg.dictData = appendUvarint(seg.dictData, uint64(plOff))
		seg.dictData = appendUvarint(seg.dictData, uint64(len(seg.blob)-plOff))
		prevTerm = t
	}
	return seg
}

// NumDocs returns the number of indexed documents.
func (s *Segment) NumDocs() uint64 { return s.numDocs }

// NumTerms returns the number of distinct terms in the dictionary.
func (s *Segment) NumTerms() int { return s.numTerms }

// DictBytes returns the on-disk size of the dictionary in bytes.
func (s *Segment) DictBytes() int { return len(s.dictData) }

// PostingBytes returns the on-disk size of all posting lists in bytes.
func (s *Segment) PostingBytes() int { return len(s.blob) }

// Size returns the total segment size in bytes.
func (s *Segment) Size() int { return len(s.dictData) + len(s.blob) }

// Postings looks up term in the prefix-compressed dictionary and returns its
// posting list, or false if the term is not indexed.
func (s *Segment) Postings(term string) (*PostingList, bool) {
	off, length, ok := s.locate(strings.ToLower(term))
	if !ok {
		return nil, false
	}
	return decodePostingList(s.blob[off : off+length]), true
}

// locate finds the byte range of term's posting list inside the blob.
func (s *Segment) locate(term string) (off, length int, ok bool) {
	// Binary search the sparse block index for the greatest block whose
	// first term is <= term.
	i := sort.Search(len(s.blocks), func(i int) bool { return s.blocks[i].term > term }) - 1
	if i < 0 {
		return 0, 0, false
	}
	pos := s.blocks[i].offset
	end := len(s.dictData)
	if i+1 < len(s.blocks) {
		end = s.blocks[i+1].offset
	}
	prev := ""
	for j := 0; j < dictBlockSize && pos < end; j++ {
		shared, n1 := binary.Uvarint(s.dictData[pos:])
		pos += n1
		sufLen, n2 := binary.Uvarint(s.dictData[pos:])
		pos += n2
		t := prev[:shared] + string(s.dictData[pos:pos+int(sufLen)])
		pos += int(sufLen)
		plOff, n3 := binary.Uvarint(s.dictData[pos:])
		pos += n3
		plLen, n4 := binary.Uvarint(s.dictData[pos:])
		pos += n4
		if t == term {
			return int(plOff), int(plLen), true
		}
		if t > term {
			return 0, 0, false
		}
		prev = t
	}
	return 0, 0, false
}

// And evaluates a multi-term AND query: it returns the documents that contain
// every given term, using skip-pointer intersection driven by the shortest
// posting list. Frequencies in each Match are aligned with the terms order.
func (s *Segment) And(terms ...string) []Match {
	lists := make([]*PostingList, 0, len(terms))
	for _, t := range terms {
		pl, ok := s.Postings(t)
		if !ok {
			return nil
		}
		lists = append(lists, pl)
	}
	matches, _ := AndStats(lists...)
	return matches
}

func sortedDocs(m map[uint64]uint32) []uint64 {
	docs := make([]uint64, 0, len(m))
	for d := range m {
		docs = append(docs, d)
	}
	sort.Slice(docs, func(i, j int) bool { return docs[i] < docs[j] })
	return docs
}

func commonPrefix(a, b string) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	i := 0
	for i < n && a[i] == b[i] {
		i++
	}
	return i
}

func appendUvarint(dst []byte, v uint64) []byte {
	var buf [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(buf[:], v)
	return append(dst, buf[:n]...)
}

// skipInterval returns the distance between two skip pointers for a posting
// list of length n. Lists shorter than 8 postings get no skip pointers.
func skipInterval(n int) int {
	if n < 8 {
		return n // no skip pointer ever triggers
	}
	iv := int(math.Sqrt(float64(n)))
	if iv < 1 {
		iv = 1
	}
	return iv
}
