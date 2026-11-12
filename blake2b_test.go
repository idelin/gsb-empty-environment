package netsec

import (
	"bytes"
	"encoding/hex"
	"hash"
	"testing"
)

var _ hash.Hash = (*blake2b)(nil)

func TestBlake2bInterface(t *testing.T) {
	h := newBlake2b(64)
	if h.Size() != 64 || h.BlockSize() != 128 {
		t.Fatalf("size=%d block=%d", h.Size(), h.BlockSize())
	}

	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for an out-of-range digest size")
		}
	}()
	_ = newBlake2b(65)
}

func TestBlake2bVectors(t *testing.T) {
	cases := []struct {
		name   string
		size   int
		input  string
		digest string
	}{
		{
			name:   "rfc7693 empty 512",
			size:   64,
			input:  "",
			digest: "786a02f742015903c6c6fd852552d272912f4740e15847618a86e217f71f5419d25e1031afee585313896444934eb04b903a685b1448b755d56f701afe9be2ce",
		},
		{
			name:   "rfc7693 abc 512",
			size:   64,
			input:  "abc",
			digest: "ba80a53f981c4d0d6a2797b69f12f6e94c212f14685ac4b74b12bb6fdbffa2d17d87c5392aab792dc252d5de4533cc9518d38aa8dbf1925ab92386edd4009923",
		},
		{
			name:   "abc 256",
			size:   32,
			input:  "abc",
			digest: "bddd813c634239723171ef3fee98579b94964e3bb1cb3e427262c8c068d52319",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			want, err := hex.DecodeString(tc.digest)
			if err != nil {
				t.Fatalf("decode expected digest: %v", err)
			}
			h := newBlake2b(tc.size)
			if _, err := h.Write([]byte(tc.input)); err != nil {
				t.Fatalf("write: %v", err)
			}
			if got := h.Sum(nil); !bytes.Equal(got, want) {
				t.Fatalf("digest mismatch\ngot:  %x\nwant: %x", got, want)
			}
		})
	}
}

// TestBlake2bStreaming ensures chunked writes produce the same digest as a
// single write, including partial final blocks across the 128-byte boundary.
func TestBlake2bStreaming(t *testing.T) {
	// Boundary lengths hit empty final blocks, partial blocks and several
	// full blocks, the cases where byte-counter and buffering bugs surface.
	lengths := []int{0, 1, 127, 128, 129, 255, 256, 257, 1000, 4096, 4097}
	for _, length := range lengths {
		payload := bytes.Repeat([]byte("update-package-"), (length/15)+1)[:length]

		oneShot := newBlake2b(64)
		oneShot.Write(payload)
		want := oneShot.Sum(nil)

		for _, chunk := range []int{1, 7, 64, 127, 128, 129, 4096} {
			streamed := newBlake2b(64)
			for i := 0; i < len(payload); i += chunk {
				end := i + chunk
				if end > len(payload) {
					end = len(payload)
				}
				streamed.Write(payload[i:end])
			}
			if got := streamed.Sum(nil); !bytes.Equal(got, want) {
				t.Fatalf("length %d chunk %d produced a different digest", length, chunk)
			}
		}

		reset := newBlake2b(64)
		reset.Write([]byte("garbage"))
		reset.Reset()
		reset.Write(payload)
		if got := reset.Sum(nil); !bytes.Equal(got, want) {
			t.Fatalf("length %d: Reset did not restore the initial hash state", length)
		}

		// Sum must be non-destructive: repeated calls return the same value.
		if got := oneShot.Sum(nil); !bytes.Equal(got, want) {
			t.Fatalf("length %d: Sum mutated the hash state", length)
		}
	}
}
