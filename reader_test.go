package netsec

import (
	"errors"
	"io"
	"math/rand"
	"testing"
)

type canonicalEvent struct {
	kind     string
	frame    []byte
	sentinel error
}

func canonicalError(err error) error {
	for _, s := range []error{
		ErrProtocol,
		ErrBadTrailer,
		ErrFrameTooLarge,
		io.ErrUnexpectedEOF,
		io.EOF,
	} {
		if errors.Is(err, s) {
			return s
		}
	}
	return err
}

func runParser(chunks [][]byte, maxSize int) []canonicalEvent {
	p := NewParser(maxSize)
	fatal := false
	for _, c := range chunks {
		if err := p.Feed(c); err != nil {
			if errors.Is(err, ErrProtocol) {
				fatal = true
			}
			break
		}
	}
	var got []canonicalEvent
	for {
		frame, err := p.Next()
		if frame == nil && err == nil {
			break
		}
		if err != nil {
			got = append(got, canonicalEvent{kind: "err", sentinel: canonicalError(err)})
			if errors.Is(err, ErrProtocol) {
				fatal = true
				break
			}
			continue
		}
		got = append(got, canonicalEvent{kind: "frame", frame: append([]byte(nil), frame...)})
	}
	if !fatal {
		got = append(got, canonicalEvent{kind: "err", sentinel: canonicalError(p.End())})
	}
	return got
}

func compositions(n int) [][]int {
	if n == 0 {
		return [][]int{{}}
	}
	var out [][]int
	for first := 1; first <= n; first++ {
		for _, rest := range compositions(n - first) {
			out = append(out, append([]int{first}, rest...))
		}
	}
	return out
}

func chunksByComposition(data []byte, parts []int) [][]byte {
	chunks := make([][]byte, 0, len(parts))
	pos := 0
	for _, length := range parts {
		chunks = append(chunks, append([]byte(nil), data[pos:pos+length]...))
		pos += length
	}
	return chunks
}

func everyChunking(t *testing.T, data []byte) [][][]byte {
	t.Helper()
	n := len(data)
	var out [][][]byte
	out = append(out, [][]byte{append([]byte(nil), data...)})
	for k := 1; k < n; k++ {
		out = append(out, [][]byte{
			append([]byte(nil), data[:k]...),
			append([]byte(nil), data[k:]...),
		})
	}
	byteWise := make([][]byte, n)
	for i := range data {
		byteWise[i] = []byte{data[i]}
	}
	out = append(out, byteWise)
	if n <= 12 {
		for _, parts := range compositions(n) {
			out = append(out, chunksByComposition(data, parts))
		}
	} else {
		rng := rand.New(rand.NewSource(42))
		for trial := 0; trial < 200; trial++ {
			var parts []int
			remaining := n
			for remaining > 0 {
				length := 1 + rng.Intn(remaining)
				parts = append(parts, length)
				remaining -= length
			}
			out = append(out, chunksByComposition(data, parts))
		}
	}
	return out
}

func chunkLengths(chunks [][]byte) []int {
	lengths := make([]int, len(chunks))
	for i, c := range chunks {
		lengths[i] = len(c)
	}
	return lengths
}

func frame(payload string) []byte {
	return append(append([]byte{VT}, []byte(payload)...), FS, CR)
}
