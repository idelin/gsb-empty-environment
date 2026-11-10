package main

import (
	"encoding/hex"
	"encoding/json"
	"io"
	"log"
	"os"
	"sort"
)

type part struct {
	offset int64
	data   []byte
}

type input struct {
	Size  int64  `json:"size"`
	Parts []part `json:"parts"`
}

func (p *part) UnmarshalJSON(b []byte) error {
	var raw [2]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	if err := json.Unmarshal(raw[0], &p.offset); err != nil {
		return err
	}
	var encoded string
	if err := json.Unmarshal(raw[1], &encoded); err != nil {
		return err
	}
	data, err := hex.DecodeString(encoded)
	if err != nil {
		return err
	}
	p.data = data
	return nil
}

func (p part) MarshalJSON() ([]byte, error) {
	return json.Marshal([2]any{p.offset, hex.EncodeToString(p.data)})
}

type conflictResult struct {
	Conflict int64 `json:"conflict"`
}

type gapResult struct {
	Gap int64 `json:"gap"`
}

type rowsResult struct {
	Rows [][]string `json:"rows"`
}

// seg is a stored, non-overlapping byte interval [start, start+len(data)).
type seg struct {
	start int64
	data  []byte
}

type segmentSet struct {
	segs []seg
}

// insert stores data at offset. It returns the smallest offset at which the
// incoming byte differs from an already stored byte, or -1 when there is no
// conflict. Equal overlapping bytes are merged into one stored interval.
func (s *segmentSet) insert(offset int64, data []byte) int64 {
	if len(data) == 0 {
		return -1
	}
	conflict := int64(-1)

	idx := sort.Search(len(s.segs), func(i int) bool {
		return s.segs[i].start > offset
	})

	// Check overlaps with segments at or before idx.
	for j := idx - 1; j >= 0 && offset < s.segs[j].start+int64(len(s.segs[j].data)); j-- {
		existing := s.segs[j]
		start := max(offset, existing.start)
		end := min(offset+int64(len(data)), existing.start+int64(len(existing.data)))
		for pos := start; pos < end; pos++ {
			incoming := data[pos-offset]
			stored := existing.data[pos-existing.start]
			if incoming != stored {
				if conflict == -1 || pos < conflict {
					conflict = pos
				}
			}
		}
	}

	// Check overlaps with segments at or after idx.
	for j := idx; j < len(s.segs) && offset+int64(len(data)) > s.segs[j].start; j++ {
		existing := s.segs[j]
		start := max(offset, existing.start)
		end := min(offset+int64(len(data)), existing.start+int64(len(existing.data)))
		for pos := start; pos < end; pos++ {
			incoming := data[pos-offset]
			stored := existing.data[pos-existing.start]
			if incoming != stored {
				if conflict == -1 || pos < conflict {
					conflict = pos
				}
			}
		}
	}

	// Build the union interval, including merely adjacent segments so that the
	// stored set stays as compact as possible.
	start := offset
	end := offset + int64(len(data))
	left := idx
	for left > 0 && s.segs[left-1].start+int64(len(s.segs[left-1].data)) >= start {
		left--
		if s.segs[left].start < start {
			start = s.segs[left].start
		}
	}
	right := idx
	for right < len(s.segs) && s.segs[right].start <= end {
		if segEnd := s.segs[right].start + int64(len(s.segs[right].data)); segEnd > end {
			end = segEnd
		}
		right++
	}

	merged := make([]byte, end-start)
	for k := left; k < right; k++ {
		existing := s.segs[k]
		dst := existing.start - start
		srcStart := max(0, start-existing.start)
		srcEnd := min(int64(len(existing.data)), end-existing.start)
		copy(merged[dst:], existing.data[srcStart:srcEnd])
	}
	// The incoming bytes take precedence; conflicts were already recorded.
	copy(merged[offset-start:], data)

	replacement := seg{start: start, data: merged}
	newLen := left + 1 + len(s.segs) - right
	if cap(s.segs) < newLen {
		grown := make([]seg, newLen, newLen*2)
		copy(grown, s.segs[:left])
		copy(grown[left+1:], s.segs[right:])
		grown[left] = replacement
		s.segs = grown
		return conflict
	}
	s.segs = s.segs[:newLen]
	tail := s.segs[right:]
	copy(s.segs[left+1:newLen], tail[:newLen-left-1])
	s.segs[left] = replacement
	return conflict
}

func max(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func min(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

// firstGap returns the first missing offset, or -1 when [0, size) is fully
// covered by the stored segments.
func (s *segmentSet) firstGap(size int64) int64 {
	var expected int64
	for _, existing := range s.segs {
		if existing.start > expected {
			return expected
		}
		if end := existing.start + int64(len(existing.data)); end > expected {
			expected = end
		}
		if expected >= size {
			return -1
		}
	}
	if expected < size {
		return expected
	}
	return -1
}

const (
	csvFieldStart = iota
	csvUnquoted
	csvQuoted
	csvQuoteEnd
)

// parseCSV parses RFC 4180 style CSV over the contiguous byte stream formed
// by the segments: records end with CRLF, the final record may omit it, and
// CRLF inside quotes is part of the field.
func parseCSV(segs []seg) [][]string {
	rows := make([][]string, 0)
	field := make([]byte, 0)
	row := make([]string, 0)
	state := csvFieldStart

	sIdx := 0
	pIdx := 0
	var pushed byte
	pushback := false

	next := func() (byte, bool) {
		if pushback {
			pushback = false
			return pushed, true
		}
		for sIdx < len(segs) {
			if pIdx < len(segs[sIdx].data) {
				b := segs[sIdx].data[pIdx]
				pIdx++
				if pIdx == len(segs[sIdx].data) {
					sIdx++
					pIdx = 0
				}
				return b, true
			}
			sIdx++
			pIdx = 0
		}
		return 0, false
	}

	endField := func() {
		row = append(row, string(field))
		field = field[:0]
	}
	endRecord := func() {
		endField()
		rows = append(rows, row)
		row = make([]string, 0, len(row))
	}

	for {
		b, ok := next()
		if !ok {
			break
		}
		switch state {
		case csvFieldStart:
			switch {
			case b == '"':
				state = csvQuoted
			case b == ',':
				endField()
			case b == '\r':
				if nb, nok := next(); !nok || nb != '\n' {
					field = append(field, b)
					if nok {
						pushed = nb
						pushback = true
					}
					state = csvUnquoted
				} else {
					endRecord()
					state = csvFieldStart
				}
			default:
				field = append(field, b)
				state = csvUnquoted
			}
		case csvUnquoted:
			switch {
			case b == ',':
				endField()
				state = csvFieldStart
			case b == '\r':
				if nb, nok := next(); !nok || nb != '\n' {
					field = append(field, b)
					if nok {
						pushed = nb
						pushback = true
					}
				} else {
					endRecord()
					state = csvFieldStart
				}
			default:
				field = append(field, b)
			}
		case csvQuoted:
			switch {
			case b == '"':
				state = csvQuoteEnd
			default:
				field = append(field, b)
			}
		case csvQuoteEnd:
			switch {
			case b == '"':
				field = append(field, '"')
				state = csvQuoted
			case b == ',':
				endField()
				state = csvFieldStart
			case b == '\r':
				if nb, nok := next(); !nok || nb != '\n' {
					field = append(field, b)
					if nok {
						pushed = nb
						pushback = true
					}
					state = csvUnquoted
				} else {
					endRecord()
					state = csvFieldStart
				}
			default:
				field = append(field, b)
				state = csvUnquoted
			}
		}
	}

	if state != csvFieldStart || len(row) > 0 {
		endField()
		rows = append(rows, row)
	}

	return rows
}

func solve(r io.Reader, w io.Writer) error {
	var in input
	dec := json.NewDecoder(r)
	if err := dec.Decode(&in); err != nil {
		return err
	}

	stored := segmentSet{}
	minConflict := int64(-1)
	for _, piece := range in.Parts {
		if c := stored.insert(piece.offset, piece.data); c != -1 && (minConflict == -1 || c < minConflict) {
			minConflict = c
		}
	}

	var result any
	switch {
	case minConflict != -1:
		result = conflictResult{Conflict: minConflict}
	default:
		if gap := stored.firstGap(in.Size); gap != -1 {
			result = gapResult{Gap: gap}
		} else {
			result = rowsResult{Rows: parseCSV(stored.segs)}
		}
	}

	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc.Encode(result)
}

func main() {
	if err := solve(os.Stdin, os.Stdout); err != nil {
		log.Fatal(err)
	}
}
