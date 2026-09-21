// Command crcdemo walks through segmented CRC-32: arbitrary chunking,
// length-based composition, sub-segment replacement without rescanning
// unchanged data, and the failure modes (missing / corrupt / overflow /
// same-digest-different-length).
package main

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"segcrc-lab/segcrc"
)

func main() {
	// 1) One byte stream, fed in arbitrary irregular chunks.
	data := []byte("the-quick-brown-fox-jumps-over-the-lazy-dog")
	h := segcrc.NewHasher()
	chunks := []int{3, 0, 7, 1, 9, 40} // 0-sized chunk included
	pos := 0
	for _, c := range chunks {
		end := pos + c
		if end > len(data) {
			end = len(data)
		}
		_, _ = h.Write(data[pos:end])
		pos = end
	}
	whole := h.Summary()
	fmt.Printf("whole digest (chunks %v): %s\n", chunks, whole.Encode())

	// 2) Split into three independently stored segments.
	head := data[:13]
	mid := data[13:28]
	tail := data[28:]
	store := map[string][]byte{"head": head, "mid": mid, "tail": tail}
	src := segcrc.SourceFunc(func(name string) (io.Reader, bool) {
		b, ok := store[name]
		if !ok {
			return nil, false // missing
		}
		return strings.NewReader(string(b)), true
	})

	hash := func(name string) segcrc.Summary {
		s, err := segcrc.HashSegment(src, name, int64(len(store[name])), 1<<20)
		if err != nil {
			panic(err)
		}
		return s
	}
	hSum := hash("head")
	mSum := hash("mid")
	tSum := hash("tail")
	fmt.Printf("segments: head=%s mid=%s tail=%s\n",
		hSum.Encode(), mSum.Encode(), tSum.Encode())

	// 3) Compose from summaries only; bytes are never rescanned.
	j, _ := segcrc.NewJoiner("head", "mid", "tail")
	_ = j.Set("head", hSum)
	_ = j.Set("mid", mSum)
	_ = j.Set("tail", tSum)
	composed, err := j.Build()
	must(err)
	fmt.Printf("composed without rescanning bytes: %s  (match=%v)\n",
		composed.Encode(), composed == whole)

	// 4) Replace ONLY the middle segment (even with a new length). Head and
	// tail bytes are never opened again.
	newMid := []byte("<<REPLACED-MIDDLE>>")
	store["mid"] = newMid
	// Recompute just mid; unchanged segments keep their cached summaries.
	mSum2, err := segcrc.HashSegment(src, "mid", int64(len(newMid)), 1<<20)
	must(err)
	must(j.Replace("mid", mSum2))
	expected := segcrc.Summary{}
	eh := segcrc.NewHasher()
	_, _ = eh.Write(head)
	_, _ = eh.Write(newMid)
	_, _ = eh.Write(tail)
	expected = eh.Summary()
	must(j.Verify(expected))
	got, _ := j.Build()
	fmt.Printf("after replacing mid (length %d -> %d): %s  overall verify=OK\n",
		mSum.Length, mSum2.Length, got.Encode())

	// 5) Failure modes.
	_, err = segcrc.ParseSummary("not-a-summary")
	fmt.Printf("bad summary parse: %v\n", err)

	_, err = segcrc.ReadSegment(src, "ghost",
		segcrc.Summary{Length: 5}, 1<<20)
	fmt.Printf("missing segment:   isErrMissing=%v\n", errors.Is(err, segcrc.ErrMissingData))

	corrupt := segcrc.Summary{CRC: 0xdeadbeef, Length: int64(len(head))}
	_, err = segcrc.ReadSegment(src, "head", corrupt, 1<<20)
	fmt.Printf("wrong digest:      isErrCorrupt=%v\n", errors.Is(err, segcrc.ErrCorruptData))

	_, err = segcrc.ReadSegment(src, "head",
		segcrc.Summary{Length: 1 << 40}, 1024)
	fmt.Printf("quota overrun:     isErrOverflow=%v\n", errors.Is(err, segcrc.ErrLenOverflow))

	_, err = segcrc.Combine(
		segcrc.Summary{CRC: 1, Length: segcrc.MaxLen},
		segcrc.Summary{CRC: 2, Length: 1})
	fmt.Printf("length overflow:   isErrOverflow=%v\n", errors.Is(err, segcrc.ErrLenOverflow))

	fake := segcrc.Summary{CRC: got.CRC, Length: got.Length + 1}
	err = j.Verify(fake)
	fmt.Printf("same crc +1 byte:  isErrLenMismatch=%v\n", errors.Is(err, segcrc.ErrLenMismatch))

	// 6) Empty segments are identities.
	e, _ := segcrc.Combine(segcrc.Empty, got)
	fmt.Printf("empty identity:    empty||x == x -> %v\n", e == got)
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
