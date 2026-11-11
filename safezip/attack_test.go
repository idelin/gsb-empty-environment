package safezip

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestCRCFailureBlocksCommit(t *testing.T) {
	payload := []byte(strings.Repeat("abcdefg", 100))
	raw := buildZip(false, fixtureMember{name: "a.txt", data: payload})

	corrupt := append([]byte(nil), raw...)
	// Mutate a byte inside the deflate payload, well past the local header.
	m0, _ := func() (*Member, bool) {
		a0, err := Open(BytesStore{B: raw}, testConfig())
		if err != nil {
			t.Fatalf("pre-open: %v", err)
		}
		return a0.Lookup("a.txt")
	}()
	idx := int(m0.DataOffset) + int(m0.CompressedSize) - 1
	corrupt[idx] ^= 0xFF
	a, err := Open(BytesStore{B: corrupt}, testConfig())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	cache := NewMemCache()
	m, _ := a.Lookup("a.txt")
	if _, err := a.Fetch(m, cache); !errors.Is(err, ErrCRC) {
		t.Fatalf("expected ErrCRC, got %v", err)
	}
	if _, ok := cache.Get(a.cacheKey(m)); ok {
		t.Fatal("corrupt member polluted the cache")
	}
}

func TestTruncatedObject(t *testing.T) {
	raw := buildZip(false, fixtureMember{name: "a.txt", data: []byte("payload")})
	cut := raw[:len(raw)-3]
	if _, err := Open(BytesStore{B: cut}, testConfig()); !errors.Is(err, ErrTruncated) && !errors.Is(err, ErrFormat) {
		t.Fatalf("expected ErrTruncated/ErrFormat, got %v", err)
	}
}

func TestOverlappingPayloadsRejected(t *testing.T) {
	raw := buildZip(false,
		fixtureMember{name: "a.txt", data: []byte("AAAA")},
		fixtureMember{name: "b.txt", data: []byte("BBBB")},
	)
	tampered := append([]byte(nil), raw...)
	firstCD := bytes.Index(tampered, u32(sigCentral))
	secondCD := bytes.Index(tampered[firstCD+4:], u32(sigCentral)) + firstCD + 4
	// Redirect b's local header offset to a's local header (offset zero).
	binaryPutU32(tampered[secondCD+42:], 0)
	_, err := Open(BytesStore{B: tampered}, testConfig())
	if !errors.Is(err, ErrBinding) && !errors.Is(err, ErrOverlap) {
		t.Fatalf("expected binding/overlap error, got %v", err)
	}
}

func TestPseudoDirectoryRejected(t *testing.T) {
	raw := buildZip(false,
		fixtureMember{name: "real.txt", data: []byte("data")},
		fixtureMember{name: "fake/", dir: true},
	)
	tampered := append([]byte(nil), raw...)
	firstCD := bytes.Index(tampered, u32(sigCentral))
	secondCD := bytes.Index(tampered[firstCD+4:], u32(sigCentral)) + firstCD + 4
	binaryPutU32(tampered[secondCD+16:], 0x12345678) // CRC
	binaryPutU32(tampered[secondCD+20:], 42)         // compressed size
	binaryPutU32(tampered[secondCD+24:], 42)         // uncompressed size
	_, err := Open(BytesStore{B: tampered}, testConfig())
	if !errors.Is(err, ErrPseudoDirectory) {
		t.Fatalf("expected ErrPseudoDirectory, got %v", err)
	}
}

func TestOverlongDeclarationsRejected(t *testing.T) {
	raw := buildZip(false, fixtureMember{name: "a.txt", data: []byte("data")})

	t.Run("compressed past EOF", func(t *testing.T) {
		tampered := append([]byte(nil), raw...)
		idx := bytes.Index(tampered, u32(sigCentral))
		binaryPutU32(tampered[idx+20:], uint32(len(tampered)+1024))
		_, err := Open(BytesStore{B: tampered}, testConfig())
		if !errors.Is(err, ErrDeclaredTooLong) && !errors.Is(err, ErrTruncated) {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("uncompressed limit exceeded", func(t *testing.T) {
		tampered := append([]byte(nil), raw...)
		idx := bytes.Index(tampered, u32(sigCentral))
		binaryPutU32(tampered[idx+24:], uint32(2<<30))
		cfg := testConfig()
		cfg.Limits.MaxMember = 1024
		_, err := Open(BytesStore{B: tampered}, cfg)
		if !errors.Is(err, ErrDeclaredTooLong) {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("name too long", func(t *testing.T) {
		long := strings.Repeat("n", 2000)
		rawLong := buildZip(false, fixtureMember{name: long, data: []byte("x")})
		cfg := testConfig()
		cfg.Limits.MaxName = 64
		_, err := Open(BytesStore{B: rawLong}, cfg)
		if !errors.Is(err, ErrDeclaredTooLong) {
			t.Fatalf("got %v", err)
		}
	})
}

func TestLocalHeaderNameMismatch(t *testing.T) {
	raw := buildZip(false, fixtureMember{name: "a.txt", data: []byte("data")})
	tampered := append([]byte(nil), raw...)
	copy(tampered[localHeaderFixed:], "z.txt")
	_, err := Open(BytesStore{B: tampered}, testConfig())
	if !errors.Is(err, ErrBinding) {
		t.Fatalf("expected ErrBinding, got %v", err)
	}
}

func TestLocalHeaderMethodMismatch(t *testing.T) {
	raw := buildZip(false, fixtureMember{name: "a.txt", data: []byte("data")})
	tampered := append([]byte(nil), raw...)
	binaryPutU16(tampered[8:], methodStore)
	_, err := Open(BytesStore{B: tampered}, testConfig())
	if !errors.Is(err, ErrBinding) {
		t.Fatalf("expected ErrBinding, got %v", err)
	}
}

func TestDuplicateNamesRejected(t *testing.T) {
	raw := buildZip(false,
		fixtureMember{name: "a.txt", data: []byte("one")},
		fixtureMember{name: "a.txt", data: []byte("two")},
	)
	_, err := Open(BytesStore{B: raw}, testConfig())
	if !errors.Is(err, ErrBinding) {
		t.Fatalf("expected ErrBinding, got %v", err)
	}
}

func TestPathTraversalRejected(t *testing.T) {
	raw := buildZip(false, fixtureMember{name: "../evil.txt", data: []byte("x")})
	if _, err := Open(BytesStore{B: raw}, testConfig()); !errors.Is(err, ErrFormat) {
		t.Fatalf("expected ErrFormat, got %v", err)
	}
}

func TestDescriptorTamperDetected(t *testing.T) {
	raw := buildZip(true, fixtureMember{name: "a.txt", data: []byte(strings.Repeat("d", 64))})
	tampered := append([]byte(nil), raw...)
	descIdx := bytes.Index(tampered, u32(sigDataDesc))
	if descIdx < 0 {
		t.Fatal("no data descriptor found")
	}
	binaryPutU32(tampered[descIdx+4:], 0xDEADBEEF)
	a, err := Open(BytesStore{B: tampered}, testConfig())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	m, _ := a.Lookup("a.txt")
	r, err := a.OpenMember(m)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(io.Discard, r); err != nil {
		t.Fatalf("copy: %v", err)
	}
	if err := r.Close(); !errors.Is(err, ErrBinding) {
		t.Fatalf("expected ErrBinding, got %v", err)
	}
}
