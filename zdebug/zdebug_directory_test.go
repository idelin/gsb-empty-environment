package zdebug

import (
	"bytes"
	"errors"
	"testing"
)

func TestNewHostRejectsMalformedDirectory(t *testing.T) {
	valid := Section{
		Name:            "ok",
		Method:          MethodFrame,
		PhysicalOffset:  0,
		PhysicalLength:  8,
		MaxDecompressed: 4,
	}

	tests := []struct {
		name     string
		dataSize int64
		section  Section
	}{
		{"empty name", 16, withName(valid, "")},
		{"negative offset", 16, withOffset(valid, -1)},
		{"negative length", 16, withLength(valid, -1)},
		{"negative cap", 16, withCap(valid, -1)},
		{"offset past data", 16, withOffset(valid, 20)},
		{"range past data", 16, withLength(valid, 40)},
		{"unknown method", 16, withMethod(valid, 42)},
	}

	factory := NewCountingFactory(frameFactory)
	cfg := testConfig(factory, 16, 0)

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewHostConfig(bytes.NewReader(make([]byte, tc.dataSize)), tc.dataSize,
				[]Section{tc.section}, cfg)
			if !errors.Is(err, ErrInvalidSection) {
				t.Fatalf("want ErrInvalidSection, got %v", err)
			}
		})
	}

	t.Run("duplicate name", func(t *testing.T) {
		dup := withName(valid, "ok")
		dup.PhysicalOffset = 4
		_, err := NewHostConfig(bytes.NewReader(make([]byte, 32)), 32,
			[]Section{valid, dup}, cfg)
		if !errors.Is(err, ErrInvalidSection) {
			t.Fatalf("want ErrInvalidSection, got %v", err)
		}
	})

	t.Run("unknown section name", func(t *testing.T) {
		host := mustHost(t, bytes.NewReader(make([]byte, 16)), 16, []Section{valid}, cfg)
		t.Cleanup(func() { host.Close() })
		if _, err := host.Size("missing"); !errors.Is(err, ErrUnknownSection) {
			t.Fatalf("want ErrUnknownSection, got %v", err)
		}
	})
}

func TestOverlappingPhysicalRanges(t *testing.T) {
	payload := bytes.Repeat([]byte("overlap-"), 40) // 320 bytes
	encoded := EncodeFrames(payload[:128], payload[128:])

	// Both directory entries point at the identical physical range.
	a := Section{
		Name:            ".zdebug_a",
		Method:          MethodFrame,
		PhysicalOffset:  0,
		PhysicalLength:  int64(len(encoded)),
		MaxDecompressed: int64(len(payload)),
	}
	b := Section{
		Name:            ".zdebug_b",
		Method:          MethodFrame,
		PhysicalOffset:  0,
		PhysicalLength:  int64(len(encoded)),
		MaxDecompressed: int64(len(payload)),
	}

	factory := NewCountingFactory(frameFactory)
	// Budget only holds a few pages, so the two consumers genuinely compete.
	cfg := testConfig(factory, 64, 256)

	host := mustHost(t, bytes.NewReader(encoded), int64(len(encoded)), []Section{a, b}, cfg)
	t.Cleanup(func() { host.Close() })

	gotA, err := readAll(t, host, a.Name)
	if err != nil {
		t.Fatalf("read A: %v", err)
	}
	if !bytes.Equal(gotA, payload) {
		t.Fatalf("section A mismatch: got %d bytes, want %d", len(gotA), len(payload))
	}

	gotB, err := readAll(t, host, b.Name)
	if err != nil {
		t.Fatalf("read B: %v", err)
	}
	if !bytes.Equal(gotB, payload) {
		t.Fatalf("section B mismatch: got %d bytes, want %d", len(gotB), len(payload))
	}

	// Overlapping physical ranges must still use independent decoders per
	// section; demand paging may re-create them under eviction, so assert
	// each section decoded at least once rather than an exact total.
	if runs := host.entries[a.Name].runs; runs < 1 {
		t.Fatalf("section A never decoded, runs=%d", runs)
	}
	if runs := host.entries[b.Name].runs; runs < 1 {
		t.Fatalf("section B never decoded, runs=%d", runs)
	}
	if factory.NewCount.Load() < 2 {
		t.Fatalf("overlapping sections need independent decoders, got %d", factory.NewCount.Load())
	}
	if used := host.BudgetUsed(); used > 256 {
		t.Fatalf("global budget violated: used %d > 256", used)
	}

	// Caches are keyed per section: evicting A's pages must not corrupt B.
	if _, err := host.ReadAt(b.Name, make([]byte, 32), 0); err != nil {
		t.Fatalf("re-read B: %v", err)
	}
	again, err := readAll(t, host, a.Name)
	if err != nil {
		t.Fatalf("re-materialize A: %v", err)
	}
	if !bytes.Equal(again, payload) {
		t.Fatal("section A content changed after eviction")
	}
}

func mustHost(t *testing.T, data interface {
	ReadAt([]byte, int64) (int, error)
}, size int64, sections []Section, cfg Config) *Host {
	t.Helper()
	host, err := NewHostConfig(data, size, sections, cfg)
	if err != nil {
		t.Fatalf("NewHostConfig: %v", err)
	}
	return host
}

func readAll(t *testing.T, host *Host, name string) ([]byte, error) {
	t.Helper()
	size, err := host.Size(name)
	if err != nil {
		return nil, err
	}
	out := make([]byte, size)
	if _, err := host.ReadAt(name, out, 0); err != nil {
		return nil, err
	}
	return out, nil
}

func withName(s Section, name string) Section {
	s.Name = name
	return s
}

func withOffset(s Section, offset int64) Section {
	s.PhysicalOffset = offset
	return s
}

func withLength(s Section, length int64) Section {
	s.PhysicalLength = length
	return s
}

func withCap(s Section, cap int64) Section {
	s.MaxDecompressed = cap
	return s
}

func withMethod(s Section, method Method) Section {
	s.Method = method
	return s
}
