package netsec

import (
	"errors"
	"testing"
)

var (
	testSubuid = []SubIDRange{{Start: 100000, Length: 65536}}
	testSubgid = []SubIDRange{{Start: 200000, Length: 65536}}
)

func TestValidateMapAcceptsSelfAndSubuid(t *testing.T) {
	entries := []IDMapEntry{
		{ContainerStart: 0, HostStart: 1000, Length: 1},
		{ContainerStart: 1, HostStart: 100000, Length: 65536},
	}
	if err := ValidateMap(1000, testSubuid, entries); err != nil {
		t.Fatalf("valid map rejected: %v", err)
	}
}

func TestValidateMapRejectsHostRoot(t *testing.T) {
	entries := []IDMapEntry{{ContainerStart: 0, HostStart: 0, Length: 1}}
	err := ValidateMap(1000, testSubuid, entries)
	if !errors.Is(err, ErrHostNotAllowed) {
		t.Fatalf("host uid 0 accepted: %v", err)
	}
}

func TestValidateMapRejectsHostRootInsideLargeRange(t *testing.T) {
	entries := []IDMapEntry{{ContainerStart: 0, HostStart: 0, Length: 65536}}
	err := ValidateMap(1000, testSubuid, entries)
	if !errors.Is(err, ErrHostNotAllowed) {
		t.Fatalf("host range covering uid 0 accepted: %v", err)
	}
}

func TestValidateMapRejectsOverflow(t *testing.T) {
	cases := []IDMapEntry{
		{ContainerStart: 0, HostStart: 100000, Length: 1<<32 - 100000 + 1},
		{ContainerStart: 1<<32 - 10, HostStart: 100000, Length: 11},
		{ContainerStart: 0, HostStart: 1<<32 - 1, Length: 2},
	}
	for i, e := range cases {
		err := ValidateMap(1000, []SubIDRange{{Start: 0, Length: ^uint32(0)}}, []IDMapEntry{e})
		if !errors.Is(err, ErrOverflow) {
			t.Fatalf("case %d: overflow accepted: %v", i, err)
		}
	}
}

func TestValidateMapSubuidBoundaries(t *testing.T) {
	ok := []IDMapEntry{
		{ContainerStart: 0, HostStart: 100000, Length: 1},
		{ContainerStart: 1, HostStart: 165535, Length: 1},
		{ContainerStart: 2, HostStart: 100000, Length: 65536},
	}
	if err := ValidateMap(1000, testSubuid, ok); err != nil {
		t.Fatalf("boundary-valid map rejected: %v", err)
	}

	bad := []IDMapEntry{
		{ContainerStart: 0, HostStart: 99999, Length: 1},
		{ContainerStart: 0, HostStart: 165536, Length: 1},
		{ContainerStart: 0, HostStart: 100000, Length: 65537},
		{ContainerStart: 0, HostStart: 165535, Length: 2},
	}
	for i, e := range bad {
		err := ValidateMap(1000, testSubuid, []IDMapEntry{e})
		if !errors.Is(err, ErrHostNotAllowed) {
			t.Fatalf("case %d: out-of-subuid host range accepted: %v", i, err)
		}
	}
}

func TestValidateMapRejectsContainerOverlap(t *testing.T) {
	overlap := []IDMapEntry{
		{ContainerStart: 0, HostStart: 100000, Length: 10},
		{ContainerStart: 5, HostStart: 100010, Length: 10},
	}
	err := ValidateMap(1000, testSubuid, overlap)
	if !errors.Is(err, ErrOverlap) {
		t.Fatalf("overlapping container ranges accepted: %v", err)
	}

	touching := []IDMapEntry{
		{ContainerStart: 0, HostStart: 100000, Length: 10},
		{ContainerStart: 10, HostStart: 100010, Length: 10},
	}
	if err := ValidateMap(1000, testSubuid, touching); err != nil {
		t.Fatalf("adjacent container ranges rejected: %v", err)
	}
}

func TestValidateMapEntryLimit(t *testing.T) {
	entries := make([]IDMapEntry, MaxMapEntries+1)
	for i := range entries {
		entries[i] = IDMapEntry{ContainerStart: uint32(i), HostStart: 100000 + uint32(i), Length: 1}
	}
	err := ValidateMap(1000, testSubuid, entries)
	if !errors.Is(err, ErrTooManyEntries) {
		t.Fatalf("341 entries accepted: %v", err)
	}

	if err := ValidateMap(1000, testSubuid, entries[:MaxMapEntries]); err != nil {
		t.Fatalf("340 entries rejected: %v", err)
	}
}

func TestWriteGIDMapRequiresSetgroupsDeny(t *testing.T) {
	entries := []IDMapEntry{{ContainerStart: 0, HostStart: 1000, Length: 1}}

	w := NewMapWriter(1000, 1000, testSubuid, testSubgid, false)
	if err := w.WriteGIDMap(entries); !errors.Is(err, ErrSetgroupsNeeded) {
		t.Fatalf("gid_map written before setgroups=deny: %v", err)
	}

	w = NewMapWriter(1000, 1000, testSubuid, testSubgid, false)
	if err := w.WriteSetgroups(true); err != nil {
		t.Fatal(err)
	}
	if err := w.WriteGIDMap(entries); err != nil {
		t.Fatalf("gid_map after setgroups=deny rejected: %v", err)
	}
}

func TestWriteGIDMapAllowedWithCapSetGID(t *testing.T) {
	entries := []IDMapEntry{{ContainerStart: 0, HostStart: 1000, Length: 1}}
	w := NewMapWriter(1000, 1000, testSubuid, testSubgid, true)
	if err := w.WriteGIDMap(entries); err != nil {
		t.Fatalf("gid_map with CAP_SETGID rejected: %v", err)
	}
}

func TestWriteUIDMapUsesSubuidTable(t *testing.T) {
	w := NewMapWriter(1000, 1000, testSubuid, testSubgid, false)
	bad := []IDMapEntry{{ContainerStart: 0, HostStart: 200000, Length: 1}}
	if err := w.WriteUIDMap(bad); !errors.Is(err, ErrHostNotAllowed) {
		t.Fatalf("uid_map validated against subgid table: %v", err)
	}
}
