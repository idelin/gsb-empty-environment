package netsec

import (
	"math/rand"
	"testing"
)

// fuzzEvents is runParser without the terminal End() event so two chunkings
// of the same finite stream can be compared directly.
func fuzzEvents(data []byte, limit int, rng *rand.Rand) []canonicalEvent {
	var chunks [][]byte
	remaining := data
	for len(remaining) > 0 {
		length := 1 + rng.Intn(len(remaining))
		chunks = append(chunks, append([]byte(nil), remaining[:length]...))
		remaining = remaining[length:]
	}

	p := NewParser(limit)
	var events []canonicalEvent
	for _, chunk := range chunks {
		if err := p.Feed(chunk); err != nil {
			break
		}
	}
	for {
		frame, err := p.Next()
		if frame == nil && err == nil {
			break
		}
		if err != nil {
			events = append(events, canonicalEvent{kind: "err", sentinel: canonicalError(err)})
			break
		}
		events = append(events, canonicalEvent{kind: "frame", frame: append([]byte(nil), frame...)})
	}
	return events
}

func FuzzParserChunking(f *testing.F) {
	f.Add([]byte{VT, 'a', FS, CR, VT, 'b', FS, CR}, 3, int64(1))
	f.Add([]byte{VT, VT, FS, 'X', 'j', 'u', 'n', 'k'}, 0, int64(2))
	f.Add([]byte{VT, 1, 2, 3, 4, FS, CR, VT, FS, CR}, 2, int64(3))

	f.Fuzz(func(t *testing.T, data []byte, limit int, seed int64) {
		if len(data) > 64 {
			data = data[:64]
		}
		if limit < 0 || limit > 32 {
			limit = ((limit % 5) + 5) % 5
		}

		whole, _ := drainWhole(data, limit)
		for i := 0; i < 8; i++ {
			rng := rand.New(rand.NewSource(seed + int64(i)))
			got := fuzzEvents(data, limit, rng)
			if !equalCanonical(got, whole) {
				t.Fatalf("chunking mismatch for % X (limit %d):\n got %#v\nwant %#v",
					data, limit, got, whole)
			}
		}
	})
}

// drainWhole feeds the complete buffer and returns the in-stream events plus
// the terminal End() sentinel separately.
func drainWhole(data []byte, limit int) (events []canonicalEvent, endErr error) {
	p := NewParser(limit)
	fatal := false
	if err := p.Feed(append([]byte(nil), data...)); err != nil {
		fatal = true
	}
	for {
		frame, err := p.Next()
		if frame == nil && err == nil {
			break
		}
		if err != nil {
			events = append(events, canonicalEvent{kind: "err", sentinel: canonicalError(err)})
			break
		}
		events = append(events, canonicalEvent{kind: "frame", frame: append([]byte(nil), frame...)})
	}
	if !fatal {
		endErr = canonicalError(p.End())
	}
	return events, endErr
}

func equalCanonical(a, b []canonicalEvent) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].kind != b[i].kind || a[i].sentinel != b[i].sentinel {
			return false
		}
		if string(a[i].frame) != string(b[i].frame) {
			return false
		}
	}
	return true
}
