package rangecoder

import (
	"errors"
	"testing"
)

func sampleCheckpoint() Checkpoint {
	m := NewModel()
	for i := 0; i < 500; i++ {
		m.update(i % 200)
	}
	return Checkpoint{Offset: 12345, SymbolIndex: 678, Model: m}
}

func TestCheckpointRoundTrip(t *testing.T) {
	cp := sampleCheckpoint()
	got, err := ParseCheckpoint(cp.Marshal())
	if err != nil {
		t.Fatalf("ParseCheckpoint: %v", err)
	}
	if got.Offset != cp.Offset || got.SymbolIndex != cp.SymbolIndex {
		t.Fatalf("got offset=%d symbols=%d, want %d/%d",
			got.Offset, got.SymbolIndex, cp.Offset, cp.SymbolIndex)
	}
	if !got.Model.Equal(cp.Model) {
		t.Fatal("model state did not survive serialization")
	}
}

func TestCheckpointVersionRejected(t *testing.T) {
	rec := sampleCheckpoint().Marshal()
	rec[4], rec[5] = 0, 99 // version field
	_, err := ParseCheckpoint(rec)
	if !errors.Is(err, ErrUnsupportedVersion) {
		t.Fatalf("got %v, want ErrUnsupportedVersion", err)
	}
}

func TestCheckpointTamperingRejected(t *testing.T) {
	cases := map[string]func([]byte) []byte{
		"payload bit flip": func(b []byte) []byte { b[checkpointHdrLen+3] ^= 0x01; return b },
		"magic":            func(b []byte) []byte { b[0] = 'X'; return b },
		"length field":     func(b []byte) []byte { b[11]++; return b },
		"truncated":        func(b []byte) []byte { return b[:len(b)-5] },
	}
	for name, tamper := range cases {
		t.Run(name, func(t *testing.T) {
			rec := tamper(sampleCheckpoint().Marshal())
			if _, err := ParseCheckpoint(rec); err == nil {
				t.Fatal("tampered checkpoint accepted")
			}
		})
	}
}

func TestCheckpointTableRoundTrip(t *testing.T) {
	data := textLike(9000, 10)
	_, cps := encodeWithCheckpoints(data, 1000)
	blob := MarshalCheckpointTable(cps)
	got, err := ParseCheckpointTable(blob)
	if err != nil {
		t.Fatalf("ParseCheckpointTable: %v", err)
	}
	if len(got) != len(cps) {
		t.Fatalf("got %d checkpoints, want %d", len(got), len(cps))
	}
	for i := range cps {
		if got[i].Offset != cps[i].Offset ||
			got[i].SymbolIndex != cps[i].SymbolIndex ||
			!got[i].Model.Equal(cps[i].Model) {
			t.Fatalf("checkpoint %d mismatch", i)
		}
	}
}

func TestCheckpointTableTamperingRejected(t *testing.T) {
	_, cps := encodeWithCheckpoints(textLike(3000, 11), 1000)
	blob := MarshalCheckpointTable(cps)

	bad := append([]byte(nil), blob...)
	bad[len(bad)/2] ^= 0x80
	if _, err := ParseCheckpointTable(bad); err == nil {
		t.Fatal("corrupted table accepted")
	}

	bad = append([]byte(nil), blob...)
	bad[5] = 2 // table version
	if _, err := ParseCheckpointTable(bad); !errors.Is(err, ErrUnsupportedVersion) {
		t.Fatalf("got %v, want ErrUnsupportedVersion", err)
	}
}
