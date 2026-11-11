package netsec

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"errors"
	"fmt"
	"sync"
	"testing"
)

func newGCM(t *testing.T) cipher.AEAD {
	t.Helper()
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i*7 + 1)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatalf("aes.NewCipher: %v", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatalf("cipher.NewGCM: %v", err)
	}
	return aead
}

func newTestAdapter(t *testing.T) *Adapter {
	t.Helper()
	a, err := NewAdapter(newGCM(t), Config{
		MaxPlaintext:  1 << 20,
		MaxCiphertext: 1 << 20,
		MaxTempBytes:  1 << 20,
	})
	if err != nil {
		t.Fatalf("NewAdapter: %v", err)
	}
	return a
}

func testNonce(a *Adapter, seq byte) []byte {
	nonce := make([]byte, a.NonceSize())
	for i := range nonce {
		nonce[i] = byte(i+1) ^ seq
	}
	return nonce
}

// TestOpenTamperedCiphertextKeepsDst flips every bit of a real ciphertext,
// one bit at a time, and verifies that Open always fails and never touches
// the destination's logical length or (stale "old tenant") contents.
func TestOpenTamperedCiphertextKeepsDst(t *testing.T) {
	a := newTestAdapter(t)
	nonce := testNonce(a, 0x01)
	aad := []byte("channel-header v1")
	msg := []byte("tenant-A secret payload: account balances and tokens")

	var dst []byte
	if err := a.Seal(&dst, nonce, msg, aad); err != nil {
		t.Fatalf("Seal: %v", err)
	}
	ct := append([]byte(nil), dst...)

	stale := []byte("OLD-TENANT-RESIDUE-MUST-SURVIVE")
	for i := range ct {
		for bit := 0; bit < 8; bit++ {
			tampered := append([]byte(nil), ct...)
			tampered[i] ^= 1 << uint(bit)

			dst := append([]byte(nil), stale...)
			err := a.Open(&dst, nonce, tampered, aad)
			if !errors.Is(err, ErrAuthentication) {
				t.Fatalf("byte %d bit %d: err = %v, want ErrAuthentication", i, bit, err)
			}
			if len(dst) != len(stale) || !bytes.Equal(dst, stale) {
				t.Fatalf("byte %d bit %d: dst mutated to %q", i, bit, dst)
			}
		}
	}

	// Tampering with nonce and AAD must fail the same way.
	badNonce := append([]byte(nil), nonce...)
	badNonce[0] ^= 0xff
	badAAD := []byte("channel-header v2")
	cases := map[string]func(*[]byte) error{
		"nonce": func(d *[]byte) error { return a.Open(d, badNonce, ct, aad) },
		"aad":   func(d *[]byte) error { return a.Open(d, nonce, ct, badAAD) },
	}
	for name, mutate := range cases {
		dst := append([]byte(nil), stale...)
		if err := mutate(&dst); !errors.Is(err, ErrAuthentication) {
			t.Fatalf("%s: err = %v, want ErrAuthentication", name, err)
		}
		if !bytes.Equal(dst, stale) {
			t.Fatalf("%s: dst mutated to %q", name, dst)
		}
	}
}

// TestSealAliasLayouts exercises fully overlapping, partially overlapping,
// and independent input/output buffers for Seal.
func TestSealAliasLayouts(t *testing.T) {
	a := newTestAdapter(t)
	raw := newGCM(t) // independent verifier
	msg := []byte("alias-layout plaintext, long enough to overlap: 0123456789abcdef")
	aad := []byte("aad")

	layouts := map[string]func() (dst *[]byte, pt []byte){
		"full overlap": func() (*[]byte, []byte) {
			backing := make([]byte, len(msg)+a.Overhead()+16)
			copy(backing, msg)
			d := backing[:len(msg)]
			return &d, backing[:len(msg)]
		},
		"partial overlap dst before": func() (*[]byte, []byte) {
			backing := make([]byte, len(msg)+a.Overhead()+32)
			copy(backing[8:], msg)
			d := backing[:len(msg)/2]
			return &d, backing[8 : 8+len(msg)]
		},
		"partial overlap dst after": func() (*[]byte, []byte) {
			backing := make([]byte, len(msg)+a.Overhead()+32)
			copy(backing, msg)
			d := backing[8 : 8+len(msg)/2]
			return &d, backing[:len(msg)]
		},
		"independent": func() (*[]byte, []byte) {
			d := make([]byte, 0, 64)
			return &d, append([]byte(nil), msg...)
		},
	}

	for name, setup := range layouts {
		t.Run(name, func(t *testing.T) {
			dst, pt := setup()
			nonce := testNonce(a, 0x02)
			if err := a.Seal(dst, nonce, pt, aad); err != nil {
				t.Fatalf("Seal: %v", err)
			}
			got, err := raw.Open(nil, nonce, *dst, aad)
			if err != nil {
				t.Fatalf("verify Open: %v", err)
			}
			if !bytes.Equal(got, msg) {
				t.Fatalf("round trip mismatch: got %q want %q", got, msg)
			}
		})
	}
}

// TestOpenAliasLayouts exercises the same layouts for Open, where the
// ciphertext aliases the destination buffer.
func TestOpenAliasLayouts(t *testing.T) {
	a := newTestAdapter(t)
	msg := []byte("plaintext that will be decrypted into aliased buffers")
	aad := []byte("aad")
	nonce := testNonce(a, 0x03)

	var ctHolder []byte
	if err := a.Seal(&ctHolder, nonce, msg, aad); err != nil {
		t.Fatalf("Seal: %v", err)
	}
	ct := append([]byte(nil), ctHolder...)

	layouts := map[string]func() (dst *[]byte, in []byte){
		"full overlap": func() (*[]byte, []byte) {
			backing := make([]byte, len(ct)+16)
			copy(backing, ct)
			d := backing[:len(ct)]
			return &d, backing[:len(ct)]
		},
		"partial overlap dst before": func() (*[]byte, []byte) {
			backing := make([]byte, len(ct)+32)
			copy(backing[8:], ct)
			d := backing[:len(ct)/2]
			return &d, backing[8 : 8+len(ct)]
		},
		"partial overlap dst after": func() (*[]byte, []byte) {
			backing := make([]byte, len(ct)+32)
			copy(backing, ct)
			d := backing[8 : 8+len(ct)/2]
			return &d, backing[:len(ct)]
		},
		"independent": func() (*[]byte, []byte) {
			d := make([]byte, 0, 64)
			return &d, append([]byte(nil), ct...)
		},
	}

	for name, setup := range layouts {
		t.Run(name, func(t *testing.T) {
			dst, in := setup()
			if err := a.Open(dst, nonce, in, aad); err != nil {
				t.Fatalf("Open: %v", err)
			}
			if !bytes.Equal(*dst, msg) {
				t.Fatalf("got %q want %q", *dst, msg)
			}
		})
	}
}

// TestEmptyMessage covers empty plaintext and empty AAD: the seal output is
// exactly the tag, and the round trip restores a zero-length destination.
func TestEmptyMessage(t *testing.T) {
	a := newTestAdapter(t)
	nonce := testNonce(a, 0x04)

	var ct []byte
	if err := a.Seal(&ct, nonce, nil, nil); err != nil {
		t.Fatalf("Seal empty: %v", err)
	}
	if len(ct) != a.Overhead() {
		t.Fatalf("empty seal length = %d, want overhead %d", len(ct), a.Overhead())
	}

	dst := []byte("stale contents from an earlier tenant")
	if err := a.Open(&dst, nonce, ct, nil); err != nil {
		t.Fatalf("Open empty: %v", err)
	}
	if len(dst) != 0 {
		t.Fatalf("empty plaintext: dst length = %d, want 0", len(dst))
	}

	// Truncated ciphertext (shorter than the tag) is rejected, dst intact.
	dst = []byte("keep me")
	if err := a.Open(&dst, nonce, ct[:a.Overhead()-1], nil); !errors.Is(err, ErrAuthentication) {
		t.Fatalf("truncated: err = %v, want ErrAuthentication", err)
	}
	if string(dst) != "keep me" {
		t.Fatalf("dst mutated: %q", dst)
	}
}

// TestReuseAcrossMessages reuses one destination buffer across many
// Seal/Open cycles with growing and shrinking messages, and interleaves a
// failed Open to prove the last good contents survive.
func TestReuseAcrossMessages(t *testing.T) {
	a := newTestAdapter(t)
	aad := []byte("reuse")

	var dst []byte
	for i := 0; i < 200; i++ {
		size := (i*37)%512 + i%3 // varied, includes growth and shrink
		msg := bytes.Repeat([]byte{byte(i)}, size)
		nonce := testNonce(a, byte(i))

		if err := a.Seal(&dst, nonce, msg, aad); err != nil {
			t.Fatalf("iter %d Seal: %v", i, err)
		}
		if err := a.Open(&dst, nonce, dst, aad); err != nil {
			t.Fatalf("iter %d Open: %v", i, err)
		}
		if !bytes.Equal(dst, msg) {
			t.Fatalf("iter %d: round trip mismatch (len %d)", i, len(dst))
		}

		if i == 100 {
			good := append([]byte(nil), dst...)
			var tampered []byte
			if err := a.Seal(&tampered, nonce, msg, aad); err != nil {
				t.Fatalf("iter %d re-Seal: %v", i, err)
			}
			tampered[len(tampered)/2] ^= 0x80
			if err := a.Open(&dst, nonce, tampered, aad); !errors.Is(err, ErrAuthentication) {
				t.Fatalf("iter %d tampered: err = %v", i, err)
			}
			if !bytes.Equal(dst, good) {
				t.Fatalf("iter %d: failed Open clobbered last good contents", i)
			}
		}
	}
}

// TestLimits verifies that oversize inputs and scratch requirements are
// rejected with the dedicated errors, leaving the destination untouched.
func TestLimits(t *testing.T) {
	a, err := NewAdapter(newGCM(t), Config{
		MaxPlaintext:  64,
		MaxCiphertext: 64 + 16,
		MaxTempBytes:  32,
	})
	if err != nil {
		t.Fatalf("NewAdapter: %v", err)
	}
	nonce := testNonce(a, 0x06)
	keep := []byte("untouched")

	dst := append([]byte(nil), keep...)
	if err := a.Seal(&dst, nonce, make([]byte, 65), nil); !errors.Is(err, ErrPlaintextTooLarge) {
		t.Fatalf("Seal oversize plaintext: err = %v", err)
	}
	if !bytes.Equal(dst, keep) {
		t.Fatalf("dst mutated after ErrPlaintextTooLarge")
	}

	// 33-byte plaintext needs 33+16 scratch bytes > MaxTempBytes=32.
	if err := a.Seal(&dst, nonce, make([]byte, 33), nil); !errors.Is(err, ErrTempLimitExceeded) {
		t.Fatalf("Seal temp limit: err = %v", err)
	}
	if !bytes.Equal(dst, keep) {
		t.Fatalf("dst mutated after ErrTempLimitExceeded (seal)")
	}

	if err := a.Open(&dst, nonce, make([]byte, 81), nil); !errors.Is(err, ErrCiphertextTooLarge) {
		t.Fatalf("Open oversize ciphertext: err = %v", err)
	}
	if !bytes.Equal(dst, keep) {
		t.Fatalf("dst mutated after ErrCiphertextTooLarge")
	}

	// 49-byte ciphertext needs 49-16=33 scratch bytes > MaxTempBytes=32.
	if err := a.Open(&dst, nonce, make([]byte, 49), nil); !errors.Is(err, ErrTempLimitExceeded) {
		t.Fatalf("Open temp limit: err = %v", err)
	}
	if !bytes.Equal(dst, keep) {
		t.Fatalf("dst mutated after ErrTempLimitExceeded (open)")
	}

	if err := a.Seal(&dst, []byte("short"), nil, nil); !errors.Is(err, ErrInvalidNonce) {
		t.Fatalf("bad nonce: err = %v", err)
	}
	if !bytes.Equal(dst, keep) {
		t.Fatalf("dst mutated after ErrInvalidNonce")
	}
}

// TestConcurrentUse hammers one adapter from many goroutines to exercise
// the scratch pool.
func TestConcurrentUse(t *testing.T) {
	a := newTestAdapter(t)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			var dst []byte
			for i := 0; i < 100; i++ {
				msg := []byte(fmt.Sprintf("goroutine %d message %d", g, i))
				nonce := testNonce(a, byte(g^i))
				if err := a.Seal(&dst, nonce, msg, nil); err != nil {
					t.Errorf("Seal: %v", err)
					return
				}
				if err := a.Open(&dst, nonce, dst, nil); err != nil {
					t.Errorf("Open: %v", err)
					return
				}
				if !bytes.Equal(dst, msg) {
					t.Errorf("round trip mismatch")
					return
				}
			}
		}(g)
	}
	wg.Wait()
}
