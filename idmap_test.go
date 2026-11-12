package netsec

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math"
	"testing"
)

// 挂载服务为请求者分配的主机侧授权区间：uid/gid 均为 [100000,101000)。
func testAllowed() RequesterRange {
	return RequesterRange{
		UIDs: IDRange{First: 100000, Count: 1000},
		GIDs: IDRange{First: 100000, Count: 1000},
	}
}

func TestValidateIDMap_Overlapping(t *testing.T) {
	tests := []struct {
		name    string
		entries []IDMapEntry
		wantErr error
	}{
		{
			name: "container side overlap",
			entries: []IDMapEntry{
				{ContainerID: 0, HostID: 100000, Count: 100},
				{ContainerID: 50, HostID: 100500, Count: 100},
			},
			wantErr: ErrOverlappingMapping,
		},
		{
			name: "host side overlap",
			entries: []IDMapEntry{
				{ContainerID: 0, HostID: 100000, Count: 100},
				{ContainerID: 200, HostID: 100050, Count: 100},
			},
			wantErr: ErrOverlappingMapping,
		},
		{
			name: "adjacent ranges are allowed",
			entries: []IDMapEntry{
				{ContainerID: 0, HostID: 100000, Count: 100},
				{ContainerID: 100, HostID: 100100, Count: 100},
			},
			wantErr: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateIDMap(tt.entries, testAllowed().UIDs)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("ValidateIDMap() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateIDMap_Wraparound(t *testing.T) {
	tests := []struct {
		name    string
		entry   IDMapEntry
		wantErr error
	}{
		{
			name:    "container first+count wraps past uint32",
			entry:   IDMapEntry{ContainerID: math.MaxUint32, HostID: 100000, Count: 2},
			wantErr: ErrRangeOverflow,
		},
		{
			name:    "host first+count wraps past uint32",
			entry:   IDMapEntry{ContainerID: 0, HostID: math.MaxUint32, Count: 10},
			wantErr: ErrRangeOverflow,
		},
		{
			name:    "count equal to full uint32 span wraps",
			entry:   IDMapEntry{ContainerID: 1, HostID: 100000, Count: math.MaxUint32},
			wantErr: ErrRangeOverflow,
		},
		{
			name:    "zero count rejected",
			entry:   IDMapEntry{ContainerID: 0, HostID: 100000, Count: 0},
			wantErr: ErrZeroCount,
		},
		{
			name:    "full uint32 span is representable without wraparound",
			entry:   IDMapEntry{ContainerID: 0, HostID: 0, Count: math.MaxUint32},
			wantErr: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			allowed := testAllowed().UIDs
			if tt.wantErr == nil && tt.entry.Count == math.MaxUint32 {
				allowed = IDRange{First: 0, Count: math.MaxUint32}
			}
			err := ValidateIDMap([]IDMapEntry{tt.entry}, allowed)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("ValidateIDMap() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateIDMap_OutsideAllowed(t *testing.T) {
	tests := []struct {
		name    string
		entry   IDMapEntry
		wantErr error
	}{
		{
			name:    "starts before allowed range",
			entry:   IDMapEntry{ContainerID: 0, HostID: 99999, Count: 2},
			wantErr: ErrRangeOutsideAllowed,
		},
		{
			name:    "extends past allowed range",
			entry:   IDMapEntry{ContainerID: 0, HostID: 100999, Count: 2},
			wantErr: ErrRangeOutsideAllowed,
		},
		{
			name:    "entirely outside allowed range",
			entry:   IDMapEntry{ContainerID: 0, HostID: 200000, Count: 10},
			wantErr: ErrRangeOutsideAllowed,
		},
		{
			name:    "fully contained is valid",
			entry:   IDMapEntry{ContainerID: 0, HostID: 100000, Count: 1000},
			wantErr: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateIDMap([]IDMapEntry{tt.entry}, testAllowed().UIDs)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("ValidateIDMap() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateIDMap_DoesNotMutateInput(t *testing.T) {
	entries := []IDMapEntry{
		{ContainerID: 0, HostID: 100000, Count: 100},
		{ContainerID: 100, HostID: 100100, Count: 50},
	}
	snapshot := append([]IDMapEntry(nil), entries...)
	if err := ValidateIDMap(entries, testAllowed().UIDs); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for i := range entries {
		if entries[i] != snapshot[i] {
			t.Fatalf("entry %d mutated: got %+v, want %+v", i, entries[i], snapshot[i])
		}
	}
}

func TestValidateIDMap_EmptyAndAllowedEdgeCases(t *testing.T) {
	t.Run("no entries and no allowed range is valid", func(t *testing.T) {
		if err := ValidateIDMap(nil, IDRange{}); err != nil {
			t.Fatalf("error = %v, want nil", err)
		}
	})

	t.Run("no entries still validates malformed allowed range", func(t *testing.T) {
		err := ValidateIDMap(nil, IDRange{First: math.MaxUint32, Count: 2})
		if !errors.Is(err, ErrRangeOverflow) {
			t.Fatalf("error = %v, want %v", err, ErrRangeOverflow)
		}
	})

	t.Run("malformed allowed range rejected even with entries", func(t *testing.T) {
		entries := []IDMapEntry{{ContainerID: 0, HostID: 100000, Count: 1}}
		err := ValidateIDMap(entries, IDRange{First: 100000, Count: 0})
		if !errors.Is(err, ErrZeroCount) {
			t.Fatalf("error = %v, want %v", err, ErrZeroCount)
		}
	})

	t.Run("root in a later entry still triggers nosuid", func(t *testing.T) {
		entries := []IDMapEntry{
			{ContainerID: 100, HostID: 100100, Count: 50},
			{ContainerID: 0, HostID: 100000, Count: 50},
		}
		req := MountRequest{UIDMap: entries, GIDMap: entries}
		err := ValidateMount(req, testAllowed())
		if !errors.Is(err, ErrRootMappingNeedsNosuid) {
			t.Fatalf("error = %v, want %v", err, ErrRootMappingNeedsNosuid)
		}
	})

	t.Run("gid map failure surfaces from mount validation", func(t *testing.T) {
		req := MountRequest{
			UIDMap: []IDMapEntry{{ContainerID: 0, HostID: 100000, Count: 1}},
			GIDMap: []IDMapEntry{{ContainerID: 0, HostID: 0, Count: 1}},
		}
		err := ValidateMount(req, testAllowed())
		if !errors.Is(err, ErrRangeOutsideAllowed) {
			t.Fatalf("error = %v, want %v", err, ErrRangeOutsideAllowed)
		}
	})
}

func TestValidateMount_RootMappingRequiresNosuid(t *testing.T) {
	allowed := testAllowed()
	rootMap := []IDMapEntry{{ContainerID: 0, HostID: 100000, Count: 1}}
	nonRootMap := []IDMapEntry{{ContainerID: 1, HostID: 100000, Count: 1}}

	tests := []struct {
		name    string
		req     MountRequest
		wantErr error
	}{
		{
			name:    "non-initial namespace root mapping without nosuid",
			req:     MountRequest{UIDMap: rootMap, GIDMap: rootMap},
			wantErr: ErrRootMappingNeedsNosuid,
		},
		{
			name:    "non-initial namespace root mapping with nosuid",
			req:     MountRequest{Flags: MountFlagNosuid, UIDMap: rootMap, GIDMap: rootMap},
			wantErr: nil,
		},
		{
			name:    "initial namespace root mapping may omit nosuid",
			req:     MountRequest{InitialNamespace: true, UIDMap: rootMap, GIDMap: rootMap},
			wantErr: nil,
		},
		{
			name:    "non-root mapping may omit nosuid",
			req:     MountRequest{UIDMap: nonRootMap, GIDMap: nonRootMap},
			wantErr: nil,
		},
		{
			name: "nosuid among other flags still satisfies requirement",
			req: MountRequest{
				Flags:  MountFlagNoDev | MountFlagNosuid | MountFlagNoExec,
				UIDMap: rootMap,
				GIDMap: rootMap,
			},
			wantErr: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateMount(tt.req, allowed)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("ValidateMount() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateMount_RejectsIllegalMapBeforeNosuid(t *testing.T) {
	// 越权主机 uid 必须先于 nosuid 规则被拒绝。
	req := MountRequest{
		UIDMap: []IDMapEntry{{ContainerID: 0, HostID: 0, Count: 1}},
		GIDMap: []IDMapEntry{{ContainerID: 0, HostID: 100000, Count: 1}},
	}
	err := ValidateMount(req, testAllowed())
	if !errors.Is(err, ErrRangeOutsideAllowed) {
		t.Fatalf("ValidateMount() error = %v, want %v", err, ErrRangeOutsideAllowed)
	}
}

func TestTranslateID(t *testing.T) {
	entries := []IDMapEntry{
		{ContainerID: 0, HostID: 100000, Count: 100},
		{ContainerID: 500, HostID: 100500, Count: 50},
	}

	tests := []struct {
		name      string
		id        uint32
		direction Direction
		want      uint32
		wantErr   error
	}{
		{name: "to host first entry", id: 0, direction: ToHost, want: 100000},
		{name: "to host with offset", id: 42, direction: ToHost, want: 100042},
		{name: "to host second entry", id: 525, direction: ToHost, want: 100525},
		{name: "to container with offset", id: 100042, direction: ToContainer, want: 42},
		{name: "to container second entry", id: 100525, direction: ToContainer, want: 525},
		{name: "unmapped to host", id: 300, direction: ToHost, wantErr: ErrUnmappedID},
		{name: "unmapped to container", id: 99999, direction: ToContainer, wantErr: ErrUnmappedID},
		{name: "unknown direction", id: 0, direction: Direction(99), wantErr: ErrInvalidDirection},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := TranslateID(entries, tt.id, tt.direction)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("TranslateID() error = %v, want %v", err, tt.wantErr)
			}
			if tt.wantErr == nil && got != tt.want {
				t.Fatalf("TranslateID() = %d, want %d", got, tt.want)
			}
		})
	}
}

// makeCapabilityXattr 构造 security.capability 的测试数据。
func makeCapabilityXattr(t *testing.T, revision uint32, effective bool, rootID uint32, size int) []byte {
	t.Helper()
	buf := make([]byte, size)
	magic := revision
	if effective {
		magic |= vfsCapFlagEffective
	}
	binary.LittleEndian.PutUint32(buf[0:4], magic)
	if size == vfsNsCapDataSize {
		binary.LittleEndian.PutUint32(buf[vfsNsCapRootIDOffset:], rootID)
	}
	return buf
}

func TestTranslateCapabilityRootID_Revision3(t *testing.T) {
	entries := []IDMapEntry{{ContainerID: 0, HostID: 100000, Count: 1000}}

	t.Run("container to host rewrites rootid", func(t *testing.T) {
		input := makeCapabilityXattr(t, vfsCapRevision3, true, 42, vfsNsCapDataSize)
		original := append([]byte(nil), input...)

		out, err := TranslateCapabilityRootID(entries, input, ToHost)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := binary.LittleEndian.Uint32(out[vfsNsCapRootIDOffset:]); got != 100042 {
			t.Fatalf("translated rootid = %d, want 100042", got)
		}
		if !bytes.Equal(input, original) {
			t.Fatal("input xattr value was mutated")
		}
		if &input[0] == &out[0] {
			t.Fatal("returned slice aliases input buffer")
		}
	})

	t.Run("host to container rewrites rootid back", func(t *testing.T) {
		input := makeCapabilityXattr(t, vfsCapRevision3, false, 100042, vfsNsCapDataSize)
		out, err := TranslateCapabilityRootID(entries, input, ToContainer)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := binary.LittleEndian.Uint32(out[vfsNsCapRootIDOffset:]); got != 42 {
			t.Fatalf("translated rootid = %d, want 42", got)
		}
	})

	t.Run("unmapped rootid rejected", func(t *testing.T) {
		input := makeCapabilityXattr(t, vfsCapRevision3, true, 5000, vfsNsCapDataSize)
		_, err := TranslateCapabilityRootID(entries, input, ToHost)
		if !errors.Is(err, ErrUnmappedID) {
			t.Fatalf("error = %v, want %v", err, ErrUnmappedID)
		}
	})
}

func TestTranslateCapabilityRootID_LegacyRevisions(t *testing.T) {
	entries := []IDMapEntry{{ContainerID: 0, HostID: 100000, Count: 1000}}

	for _, revision := range []uint32{vfsCapRevision1, vfsCapRevision2} {
		input := makeCapabilityXattr(t, revision, true, 0, vfsCapDataSize)
		out, err := TranslateCapabilityRootID(entries, input, ToHost)
		if err != nil {
			t.Fatalf("revision 0x%x: unexpected error: %v", revision, err)
		}
		if !bytes.Equal(out, input) {
			t.Fatalf("revision 0x%x: legacy value must be copied through unchanged", revision)
		}
		if &input[0] == &out[0] {
			t.Fatalf("revision 0x%x: returned slice aliases input buffer", revision)
		}
	}
}

func TestTranslateCapabilityRootID_Malformed(t *testing.T) {
	entries := []IDMapEntry{{ContainerID: 0, HostID: 100000, Count: 1000}}

	t.Run("bad length", func(t *testing.T) {
		_, err := TranslateCapabilityRootID(entries, make([]byte, 7), ToHost)
		if !errors.Is(err, ErrInvalidCapabilityXattr) {
			t.Fatalf("error = %v, want %v", err, ErrInvalidCapabilityXattr)
		}
	})

	t.Run("unknown revision", func(t *testing.T) {
		input := makeCapabilityXattr(t, uint32(0x09000000), false, 0, vfsNsCapDataSize)
		_, err := TranslateCapabilityRootID(entries, input, ToHost)
		if !errors.Is(err, ErrInvalidCapabilityXattr) {
			t.Fatalf("error = %v, want %v", err, ErrInvalidCapabilityXattr)
		}
	})

	t.Run("revision 3 with legacy size", func(t *testing.T) {
		input := makeCapabilityXattr(t, vfsCapRevision3, false, 0, vfsCapDataSize)
		_, err := TranslateCapabilityRootID(entries, input, ToHost)
		if !errors.Is(err, ErrInvalidCapabilityXattr) {
			t.Fatalf("error = %v, want %v", err, ErrInvalidCapabilityXattr)
		}
	})

	t.Run("legacy revision with ns size", func(t *testing.T) {
		input := makeCapabilityXattr(t, vfsCapRevision2, false, 0, vfsNsCapDataSize)
		_, err := TranslateCapabilityRootID(entries, input, ToHost)
		if !errors.Is(err, ErrInvalidCapabilityXattr) {
			t.Fatalf("error = %v, want %v", err, ErrInvalidCapabilityXattr)
		}
	})

	t.Run("unknown flag bits rejected", func(t *testing.T) {
		input := makeCapabilityXattr(t, vfsCapRevision3, true, 42, vfsNsCapDataSize)
		input[0] |= 0x02
		_, err := TranslateCapabilityRootID(entries, input, ToHost)
		if !errors.Is(err, ErrInvalidCapabilityXattr) {
			t.Fatalf("error = %v, want %v", err, ErrInvalidCapabilityXattr)
		}
	})
}

func TestTranslateXAttr_Dispatch(t *testing.T) {
	entries := []IDMapEntry{{ContainerID: 0, HostID: 100000, Count: 1000}}

	t.Run("security.capability is translated", func(t *testing.T) {
		input := makeCapabilityXattr(t, vfsCapRevision3, true, 42, vfsNsCapDataSize)
		out, err := TranslateXAttr(XAttrNameSecurityCapability, input, entries, ToHost)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := binary.LittleEndian.Uint32(out[vfsNsCapRootIDOffset:]); got != 100042 {
			t.Fatalf("translated rootid = %d, want 100042", got)
		}
	})

	t.Run("other xattrs pass through untouched", func(t *testing.T) {
		input := []byte("user.opaque-data")
		out, err := TranslateXAttr("user.note", input, entries, ToHost)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !bytes.Equal(out, input) {
			t.Fatalf("unrelated xattr altered: got %q, want %q", out, input)
		}
	})
}
