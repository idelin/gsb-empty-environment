package rangecoder

import (
	"errors"
	"testing"
)

func TestModelSerializationRoundTrip(t *testing.T) {
	m := NewModel()
	for i := 0; i < 100000; i++ {
		m.update((i * 7) % NumSymbols)
	}
	got, err := UnmarshalModel(m.Marshal())
	if err != nil {
		t.Fatalf("UnmarshalModel: %v", err)
	}
	if !got.Equal(m) {
		t.Fatal("model state did not survive serialization")
	}
}

func TestModelVerification(t *testing.T) {
	m := NewModel()
	for i := 0; i < 1000; i++ {
		m.update(i % 100)
	}
	good := m.Marshal()

	// Tamper with one frequency: the total no longer matches the sum.
	bad := append([]byte(nil), good...)
	bad[12+4*10] ^= 0x01
	if _, err := UnmarshalModel(bad); !errors.Is(err, ErrModelCorrupt) {
		t.Fatalf("tampered frequency: got %v, want ErrModelCorrupt", err)
	}

	// Wrong model version.
	bad = append([]byte(nil), good...)
	bad[5] = 7
	if _, err := UnmarshalModel(bad); !errors.Is(err, ErrUnsupportedVersion) {
		t.Fatalf("bad version: got %v, want ErrUnsupportedVersion", err)
	}

	// Wrong length.
	if _, err := UnmarshalModel(good[:len(good)-1]); !errors.Is(err, ErrModelCorrupt) {
		t.Fatalf("short buffer: got %v, want ErrModelCorrupt", err)
	}
}

// The rescale invariant: total stays bounded and every frequency >= 1.
func TestModelRescaleInvariant(t *testing.T) {
	m := NewModel()
	for i := 0; i < 200000; i++ {
		m.update(i % 3) // skewed distribution forces rescaling
		if m.total >= maxTotal {
			t.Fatalf("step %d: total %d exceeds bound %d", i, m.total, maxTotal)
		}
	}
	for i, f := range m.freq {
		if f < 1 {
			t.Fatalf("freq[%d] = %d, want >= 1", i, f)
		}
	}
	var sum uint32
	for _, f := range m.freq {
		sum += f
	}
	if sum != m.total {
		t.Fatalf("total %d != sum %d", m.total, sum)
	}
}
