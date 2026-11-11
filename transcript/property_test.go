package transcript

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"testing"
)

// TestRandomizedNoCollision builds many random legal transcripts and
// guarantees two different frame sequences never share a digest, while
// the builder digest always equals the independently rendered encoding.
func TestRandomizedNoCollision(t *testing.T) {
	rng := rand.New(rand.NewSource(1271))
	seen := map[[sha256.Size]byte]string{}

	for iter := 0; iter < 400; iter++ {
		b := New()
		var frames []frame
		tag := uint64(1)
		phase := uint64(0)

		for step := 0; step < 1+rng.Intn(6); step++ {
			if rng.Intn(3) == 0 {
				phase += uint64(rng.Intn(3))
				if err := b.BeginPhase(phase); err != nil {
					t.Fatal(err)
				}
			}

			switch rng.Intn(4) {
			case 0:
				p := make([]byte, rng.Intn(32))
				rng.Read(p)
				if err := b.WriteBytes(tag, p); err != nil {
					t.Fatal(err)
				}
				frames = append(frames, frame{phase: phase, tag: tag, typ: typeBytes, payload: p})
			case 1:
				v := rng.Uint64()
				if err := b.WriteUint(tag, v); err != nil {
					t.Fatal(err)
				}
				frames = append(frames, frame{phase: phase, tag: tag, typ: typeUint,
					payload: appendUvarint(nil, v)})
			case 2:
				v := int64(rng.Uint64())
				if err := b.WriteInt(tag, v); err != nil {
					t.Fatal(err)
				}
				frames = append(frames, frame{phase: phase, tag: tag, typ: typeInt,
					payload: appendUvarint(nil, encodeZigZag(v))})
			case 3:
				length := rng.Intn(40)
				fw, err := b.BeginField(tag, length)
				if err != nil {
					t.Fatal(err)
				}
				p := make([]byte, length)
				rng.Read(p)
				rest := p
				for len(rest) > 0 {
					k := 1 + rng.Intn(len(rest))
					if _, err := fw.Write(rest[:k]); err != nil {
						t.Fatal(err)
					}
					rest = rest[k:]
				}
				if err := fw.Close(); err != nil {
					t.Fatal(err)
				}
				frames = append(frames, frame{phase: phase, tag: tag, typ: typeBytes, payload: p})
			}
			tag++
		}

		d, err := b.Close()
		if err != nil {
			t.Fatal(err)
		}
		key := fmt.Sprintf("phase=%d frames=%v", phase, frames)
		if prior, ok := seen[d.Sum]; ok && prior != key {
			t.Fatalf("digest collision:\n  %s\n  %s", prior, key)
		}
		seen[d.Sum] = key

		want := sha256.Sum256(manualTranscript(frames, phase))
		if want != d.Sum {
			t.Fatalf("iter %d: builder digest != manual encoding", iter)
		}
	}
}

// TestConcurrentFields exercises the internal lock: many goroutines each
// open, stream and close one complete field. Fields are serialized, so
// every distinct tag is recorded exactly once.
func TestConcurrentFields(t *testing.T) {
	b := New()
	const writers = 32
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(tag uint64) {
			defer wg.Done()
			payload := []byte(fmt.Sprintf("field-%d", tag))
			// Only one field may be open at a time; retry on the
			// expected ErrOpenField until this goroutine wins a slot.
			for {
				fw, err := b.BeginField(tag, len(payload))
				if errors.Is(err, ErrOpenField) {
					continue
				}
				if err != nil {
					errs <- err
					return
				}
				if _, err := fw.Write(payload); err != nil {
					errs <- err
					return
				}
				if err := fw.Close(); err != nil {
					errs <- err
					return
				}
				return
			}
		}(uint64(i + 1))
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	d, err := b.Close()
	if err != nil {
		t.Fatal(err)
	}
	if d.Fields != writers {
		t.Fatalf("fields = %d, want %d", d.Fields, writers)
	}
	for tag := uint64(1); tag <= writers; tag++ {
		if !b.HasTag(tag) {
			t.Fatalf("tag %d missing", tag)
		}
	}
}
