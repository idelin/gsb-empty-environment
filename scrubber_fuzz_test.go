package offlinecli

import (
	"bytes"
	"testing"
)

// referenceScrub is an independent, deliberately simple oracle: it repeatedly
// finds the leftmost match and, on ties of start position, the longest one.
func referenceScrub(input []byte, dict *Dictionary) []byte {
	var out []byte
	for len(input) > 0 {
		bestLen := 0
		var bestRepl []byte
		node := dict.root
		for i := 0; i < len(input); i++ {
			next := node.children[input[i]]
			if next == nil {
				break
			}
			node = next
			if node.replacement != nil {
				bestLen = i + 1
				bestRepl = node.replacement
			}
		}
		if bestLen == 0 {
			out = append(out, input[0])
			input = input[1:]
			continue
		}
		out = append(out, bestRepl...)
		input = input[bestLen:]
	}
	return out
}

// FuzzScrubberChunks feeds one input through the scrubber in random-sized
// chunks and requires the same output as the reference oracle, regardless of
// where the Write boundaries fall.
func FuzzScrubberChunks(f *testing.F) {
	f.Add([]byte("abcabxabcxxabcabcd"), 3)
	f.Fuzz(func(t *testing.T, input []byte, chunkSeed int) {
		if len(input) == 0 {
			return
		}
		dict := NewDictionary(map[string]string{
			"abc":  "[A]",
			"abcd": "[LONG]",
			"x":    "[X]",
		})
		sink := &recallWriter{}
		s := NewScrubber(sink, dict)

		step := (int(chunkSeed)%5+5)%5 + 1
		for start := 0; start < len(input); {
			end := start + step
			if end > len(input) {
				end = len(input)
			}
			n, err := s.Write(input[start:end])
			if err != nil {
				t.Fatalf("Write failed: %v", err)
			}
			if n != end-start {
				t.Fatalf("Write reported %d, want %d", n, end-start)
			}
			start = end
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}

		want := referenceScrub(append([]byte(nil), input...), dict)
		if !bytes.Equal([]byte(sink.joined()), want) {
			t.Fatalf("input=%q got=%q want=%q", input, sink.joined(), want)
		}
	})
}

// FuzzScrubberShortWrites makes the underlying sink accept a varying number
// of bytes per call, proving retries never lose or duplicate output.
func FuzzScrubberShortWrites(f *testing.F) {
	f.Add([]byte("keykeyskeykeykeys"), 2)
	f.Fuzz(func(t *testing.T, input []byte, maxCall int) {
		if len(input) == 0 || maxCall <= 0 {
			return
		}
		dict := NewDictionary(map[string]string{
			"key":  "[K]",
			"keys": "[KS]",
		})
		sink := &shortWriter{maxPerCall: 1 + (int(maxCall) % 4)}
		s := NewScrubber(sink, dict)

		if _, err := s.Write(input); err != nil {
			t.Fatal(err)
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		want := referenceScrub(append([]byte(nil), input...), dict)
		if !bytes.Equal([]byte(sink.joined()), want) {
			t.Fatalf("input=%q got=%q want=%q", input, sink.joined(), want)
		}
	})
}
