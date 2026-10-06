// Package chunker splits a byte stream into chunks of at most a given byte
// budget, cutting only at UTF-8 character boundaries.
//
// Rules:
//   - Every chunk is at most Budget bytes and never splits a multi-byte
//     character across chunks; a character that does not fit moves whole
//     to the next chunk (a truncated chunk tail is never misread as bad
//     bytes).
//   - A single character whose encoding exceeds the budget cannot be
//     placed in any chunk; it is dropped and reported separately as a
//     RuneError.
//   - Each invalid source byte is emitted as one U+FFFD replacement
//     character, and its original stream offset is preserved in the
//     chunk's Replacements list.
package chunker

import (
	"bufio"
	"fmt"
	"io"
	"unicode/utf8"
)

// Replacement records one invalid source byte that was emitted as U+FFFD.
type Replacement struct {
	Offset int64 // byte offset of the invalid byte in the original stream
	At     int   // byte index within Chunk.Data of the replacement character
}

// Chunk is one output segment.
type Chunk struct {
	Data  []byte // UTF-8 content, invalid bytes replaced by U+FFFD
	Start int64  // offset in the original stream of the first source byte
	End   int64  // offset one past the last source byte consumed

	Replacements []Replacement
}

// RuneError reports a single character whose encoding exceeds the budget.
// The character is omitted from every chunk.
type RuneError struct {
	Offset int64 // byte offset of the character in the original stream
	Rune   rune  // the character (utf8.RuneError for an invalid byte)
	Size   int   // size of its output encoding in bytes
}

func (e RuneError) Error() string {
	return fmt.Sprintf("chunker: character %#U (%d bytes) at offset %d exceeds budget", e.Rune, e.Size, e.Offset)
}

var replacement = []byte("�") // U+FFFD

// Split reads r to EOF and cuts it into chunks of at most budget bytes.
// Characters that individually exceed the budget are returned separately
// as RuneErrors. A non-nil error is returned only on underlying read
// failures.
func Split(r io.Reader, budget int) ([]Chunk, []RuneError, error) {
	if budget < 1 {
		return nil, nil, fmt.Errorf("chunker: budget must be >= 1, got %d", budget)
	}

	br := bufio.NewReader(r)
	var chunks []Chunk
	var errs []RuneError

	var buf []byte
	var reps []Replacement
	var start, end int64
	var offset int64

	flush := func() {
		if len(buf) == 0 {
			return
		}
		chunks = append(chunks, Chunk{Data: buf, Start: start, End: end, Replacements: reps})
		buf, reps = nil, nil
	}

	for {
		ru, size, err := br.ReadRune()
		if err == io.EOF {
			break
		}
		if err != nil {
			flush()
			return chunks, errs, err
		}

		out := make([]byte, 0, utf8.UTFMax)
		invalid := ru == utf8.RuneError && size == 1
		if invalid {
			out = replacement
		} else {
			tmp := [utf8.UTFMax]byte{}
			n := utf8.EncodeRune(tmp[:], ru)
			out = tmp[:n]
		}

		if len(out) > budget {
			flush()
			errs = append(errs, RuneError{Offset: offset, Rune: ru, Size: len(out)})
			offset += int64(size)
			continue
		}
		if len(buf)+len(out) > budget {
			flush()
		}
		if len(buf) == 0 {
			start = offset
		}
		if invalid {
			reps = append(reps, Replacement{Offset: offset, At: len(buf)})
		}
		buf = append(buf, out...)
		offset += int64(size)
		end = offset
	}
	flush()
	return chunks, errs, nil
}
