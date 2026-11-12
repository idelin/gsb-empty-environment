package netsec

import (
	"context"
	"errors"
	"testing"
)

func TestRangeSemantics(t *testing.T) {
	if (Range{Offset: 0, Length: 0}).Valid() {
		t.Fatal("zero-length range must be invalid")
	}
	if (Range{Offset: -1, Length: 2}).Valid() {
		t.Fatal("negative offset must be invalid")
	}
	if (Range{Offset: 1 << 62, Length: 1 << 62}).Valid() {
		t.Fatal("overflowing range must be invalid")
	}
	r := Range{Offset: 10, Length: 5}
	if r.End() != 15 {
		t.Fatalf("end = %d, want 15", r.End())
	}
	if !r.Within(15) || r.Within(14) {
		t.Fatal("Within boundary check wrong")
	}
	adjacentA := Range{Offset: 0, Length: 10}
	adjacentB := Range{Offset: 10, Length: 3}
	if adjacentA.Overlaps(adjacentB) {
		t.Fatal("half-open adjacent ranges must not overlap")
	}
	if !adjacentA.Overlaps(Range{Offset: 9, Length: 2}) {
		t.Fatal("ranges sharing a byte must overlap")
	}
}

func TestPreflightAcceptsValidManifest(t *testing.T) {
	manifest := Manifest{
		Partitions: []Partition{
			partitionFor(t, "a", 4096, []byte("firmware-a")),
			partitionFor(t, "b", 8192, []byte("firmware-b")),
		},
		Protected: []Range{{Offset: 0, Length: 512}},
	}
	update, err := Prepare(context.Background(), manifest, testConfig(t))
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	defer update.Close()

	device := newFakeDevice(testDeviceSize)
	if err := update.Commit(device); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if got := string(device.mem[4096 : 4096+len("firmware-a")]); got != "firmware-a" {
		t.Fatalf("partition a mismatch: %q", got)
	}
	if got := string(device.mem[8192 : 8192+len("firmware-b")]); got != "firmware-b" {
		t.Fatalf("partition b mismatch: %q", got)
	}
	if device.writeCount() == 0 {
		t.Fatal("expected device writes on successful commit")
	}
}

func TestPreflightRejections(t *testing.T) {
	good := func(t *testing.T) Manifest {
		return Manifest{
			Partitions: []Partition{
				partitionFor(t, "a", 4096, []byte("firmware-a")),
			},
			Protected: []Range{{Offset: 0, Length: 512}},
		}
	}

	tests := []struct {
		name        string
		mutate      func(*Manifest)
		wantErr     error
		tightBudget bool
	}{
		{
			name:    "empty manifest",
			mutate:  func(m *Manifest) { m.Partitions = nil },
			wantErr: ErrEmptyManifest,
		},
		{
			name: "destination out of device",
			mutate: func(m *Manifest) {
				m.Partitions[0].Dest = Range{Offset: testDeviceSize - 2, Length: 8}
			},
			wantErr: ErrRangeOutOfDevice,
		},
		{
			name: "invalid zero-length destination",
			mutate: func(m *Manifest) {
				m.Partitions[0].Dest = Range{Offset: 4096, Length: 0}
			},
			wantErr: ErrInvalidRange,
		},
		{
			name: "protected overlap",
			mutate: func(m *Manifest) {
				m.Partitions[0].Dest = Range{Offset: 500, Length: 100}
			},
			wantErr: ErrProtectedOverlap,
		},
		{
			name: "partition overlap",
			mutate: func(m *Manifest) {
				m.Partitions = append(m.Partitions, partitionFor(t, "b", 4100, []byte("x")))
			},
			wantErr: ErrPartitionOverlap,
		},
		{
			name: "expansion budget",
			mutate: func(m *Manifest) {
				m.Partitions[0].Dest.Length = 1024
			},
			wantErr:     ErrExpansionBudget,
			tightBudget: true,
		},
		{
			name: "duplicate name",
			mutate: func(m *Manifest) {
				m.Partitions = append(m.Partitions, partitionFor(t, "a", 8192, []byte("other")))
			},
			wantErr: ErrDuplicateName,
		},
		{
			name:    "unknown compression",
			mutate:  func(m *Manifest) { m.Partitions[0].Compression = "xz" },
			wantErr: ErrUnknownCompression,
		},
		{
			name:    "unknown hash",
			mutate:  func(m *Manifest) { m.Partitions[0].HashAlgo = "md5" },
			wantErr: ErrUnknownHash,
		},
		{
			name: "bad hash length",
			mutate: func(m *Manifest) {
				m.Partitions[0].ExpectedHash = []byte{1, 2, 3}
			},
			wantErr: ErrBadHashLength,
		},
		{
			name:    "missing payload opener",
			mutate:  func(m *Manifest) { m.Partitions[0].Open = nil },
			wantErr: ErrMissingPayload,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manifest := good(t)
			tt.mutate(&manifest)
			config := testConfig(t)
			if tt.tightBudget {
				config.MaxExpansion = 512
			}
			device := newFakeDevice(testDeviceSize)
			update, err := Prepare(context.Background(), manifest, config)
			if err == nil {
				update.Close()
				t.Fatal("Prepare succeeded, want rejection")
			}
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Prepare error = %v, want sentinel %v", err, tt.wantErr)
			}
			if device.writeCount() != 0 {
				t.Fatalf("device received %d writes after preflight rejection", device.writeCount())
			}
		})
	}
}

func TestInvalidConfig(t *testing.T) {
	manifest := Manifest{Partitions: []Partition{partitionFor(t, "a", 0, []byte("x"))}}
	if _, err := Prepare(context.Background(), manifest, Config{DeviceSize: 0, MaxExpansion: 10}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("zero device size: %v", err)
	}
	if _, err := Prepare(context.Background(), manifest, Config{DeviceSize: 10, MaxExpansion: 0}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("zero budget: %v", err)
	}
	if err := (&StagedUpdate{}).Commit(nil); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("nil device commit: %v", err)
	}
}
