package allocplan

import (
	"errors"
	"testing"
)

func TestClockCannotRewind(t *testing.T) {
	planner, err := New(Config{TagDomainSize: 2, AddressSpace: 4, Quarantine: 3})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	clock := planner.Clock()
	clock.Advance(5)
	if got := clock.Tick(); got != 5 {
		t.Fatalf("tick after advance = %d, want 5", got)
	}
	clock.Advance(-10)
	clock.AdvanceTo(1)
	if got := clock.Tick(); got != 5 {
		t.Fatalf("clock rewound to %d, want 5", got)
	}
	clock.AdvanceTo(9)
	if got := clock.Tick(); got != 9 {
		t.Fatalf("AdvanceTo tick = %d, want 9", got)
	}
}

func TestConfigValidation(t *testing.T) {
	cases := []Config{
		{TagDomainSize: 0, AddressSpace: 4, Quarantine: 1},
		{TagDomainSize: 2, AddressSpace: 0, Quarantine: 1},
		{TagDomainSize: 2, AddressSpace: 4, Quarantine: -1},
	}
	for i, cfg := range cases {
		if _, err := New(cfg); !errors.Is(err, ErrBadConfig) {
			t.Fatalf("case %d: got %v, want ErrBadConfig", i, err)
		}
	}
}

func TestSaturatingAdd(t *testing.T) {
	max := int64(1<<63 - 1)
	if got := saturatingAdd(max, 1); got != max {
		t.Fatalf("saturatingAdd overflow = %d, want %d", got, max)
	}
	if got := saturatingAdd(10, -3); got != 7 {
		t.Fatalf("saturatingAdd signed = %d, want 7", got)
	}
}

func TestRejectedErrorsAndNoStateChange(t *testing.T) {
	planner, _ := New(Config{TagDomainSize: 1, AddressSpace: 2, Quarantine: 4})
	before := planner.Segments()
	if _, _, err := planner.Alloc(0); !errors.Is(err, ErrBadSize) {
		t.Fatalf("zero-size alloc error = %v", err)
	}
	if _, _, err := planner.Alloc(3); !errors.Is(err, ErrNoAddressSpace) {
		t.Fatalf("oversized alloc error = %v", err)
	}
	if err := planner.Free(0); !errors.Is(err, ErrBadSegment) {
		t.Fatalf("free of free segment error = %v", err)
	}
	if _, err := planner.Split(0, 1); !errors.Is(err, ErrBadSegment) {
		t.Fatalf("split of free segment error = %v", err)
	}
	after := planner.Segments()
	if len(after) != len(before) || after[0] != before[0] {
		t.Fatalf("failed requests mutated state: before=%+v after=%+v", before, after)
	}
}
