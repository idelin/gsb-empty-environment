package descriptor

import (
	"encoding/binary"
	"errors"
	"fmt"
	"testing"
)

const (
	testPageSize = 256
	testEntry    = 16
)

type testPages struct {
	pages map[PageID][]byte
}

func newTestPages() *testPages {
	return &testPages{pages: make(map[PageID][]byte)}
}

func (p *testPages) PageSize() uint64 { return testPageSize }

func (p *testPages) ReadPage(id PageID) ([]byte, error) {
	data, ok := p.pages[id]
	if !ok {
		return nil, fmt.Errorf("missing page %d", id)
	}
	return data, nil
}

func (p *testPages) write(addr Address, data []byte) {
	for len(data) > 0 {
		id := PageID(uint64(addr) / testPageSize)
		off := uint64(addr) % testPageSize
		page, ok := p.pages[id]
		if !ok {
			page = make([]byte, testPageSize)
			p.pages[id] = page
		}
		n := uint64(len(data))
		if n > testPageSize-off {
			n = testPageSize - off
		}
		copy(page[off:off+n], data[:n])
		addr += Address(n)
		data = data[n:]
	}
}

func encodeEntry(op byte, target Address) []byte {
	entry := make([]byte, testEntry)
	binary.LittleEndian.PutUint64(entry, uint64(op)|(uint64(target)<<8))
	return entry
}

func putEntry(pages *testPages, addr Address, op byte, target Address) {
	pages.write(addr, encodeEntry(op, target))
}

func testFormat(table *Table) *Format {
	patterns := []Pattern{
		{
			Name:  "write",
			Value: 1,
			Fields: []AddressField{{
				Name:       "dst",
				BitOffset:  8,
				BitWidth:   56,
				Kind:       FieldWrite,
				AccessSize: 8,
			}},
		},
		{
			Name:  "read",
			Value: 2,
			Fields: []AddressField{{
				Name:       "src",
				BitOffset:  8,
				BitWidth:   56,
				Kind:       FieldRead,
				AccessSize: 8,
			}},
		},
	}
	if table != nil {
		patterns = append(patterns, Pattern{
			Name:  "indirect",
			Value: 3,
			Fields: []AddressField{{
				Name:      "next",
				BitOffset: 8,
				BitWidth:  56,
				Kind:      FieldIndirect,
				Table:     table,
			}},
		})
	}
	return &Format{
		EntrySize: testEntry,
		Opcode:    OpcodeField{BitOffset: 0, BitWidth: 8},
		Patterns:  patterns,
	}
}

type recorder struct {
	events  []string
	frozen  []PageID
	submits int
	ops     []Operation
}

type recordingFreezer struct {
	r    *recorder
	fail PageID
}

func (f recordingFreezer) FreezePage(id PageID) error {
	f.r.events = append(f.r.events, fmt.Sprintf("freeze:%d", id))
	if id == f.fail {
		return errors.New("freeze denied")
	}
	f.r.frozen = append(f.r.frozen, id)
	return nil
}

type recordingSubmitter struct {
	r *recorder
}

func (s recordingSubmitter) Submit(ops []Operation) error {
	s.r.events = append(s.r.events, "submit")
	s.r.submits++
	s.r.ops = append([]Operation(nil), ops...)
	return nil
}

func authorizeWrites(t *testing.T, regions ...Region) *RegionSet {
	t.Helper()
	set, err := NewRegionSet(regions...)
	if err != nil {
		t.Fatal(err)
	}
	return set
}

func TestDirectReadWriteAndAddressSemantics(t *testing.T) {
	pages := newTestPages()
	putEntry(pages, 0, 1, 3) // raw 3, scaled by 256 => 0x300
	putEntry(pages, testEntry, 2, 0x400)
	format := testFormat(nil)
	format.Patterns[0].Fields[0].Semantics = AddressSemantics{Scale: 256}

	auth := authorizeWrites(t,
		Region{Start: 0x300, Size: 8, Allow: AccessWrite},
		Region{Start: 0x400, Size: 8, Allow: AccessRead},
	)
	v := New(pages, auth)
	v.CheckReads = true
	plan, err := v.Validate(Request{Address: 0, Length: 2 * testEntry, Format: format})
	if err != nil {
		t.Fatal(err)
	}
	ops := plan.Operations()
	if len(ops) != 2 {
		t.Fatalf("got %d operations, want 2", len(ops))
	}
	if ops[0].Access != AccessWrite || ops[0].Address != 0x300 || ops[0].Size != 8 {
		t.Fatalf("unexpected write: %+v", ops[0])
	}
	if ops[1].Access != AccessRead || ops[1].Address != 0x400 {
		t.Fatalf("unexpected read: %+v", ops[1])
	}
}

func TestIndirectWriteOutsideAuthorizationIsRejected(t *testing.T) {
	pages := newTestPages()
	table := &Table{
		HeaderSize: 0,
		Count:      CountSpec{Mode: CountFixed, Value: 1},
	}
	format := testFormat(table)
	table.Format = format

	putEntry(pages, 0, 3, 0x100)
	putEntry(pages, 0x100, 1, 0x500) // not in the authorized write region

	auth := authorizeWrites(t, Region{Start: 0x300, Size: 8, Allow: AccessWrite})
	plan, err := New(pages, auth).Validate(Request{Address: 0, Length: testEntry, Format: format})
	if err == nil {
		t.Fatal("expected authorization failure")
	}
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("got %v, want ErrUnauthorized", err)
	}
	if plan != nil {
		t.Fatal("validation returned a partial plan")
	}

	var rec recorder
	if rec.submits != 0 {
		t.Fatal("submitter ran without a plan")
	}
}

func TestSharedSubtableIsMemoizedButExpandedPerReference(t *testing.T) {
	pages := newTestPages()
	table := &Table{
		HeaderSize: 0,
		Count:      CountSpec{Mode: CountFixed, Value: 2},
	}
	format := testFormat(table)
	table.Format = format

	// Two root entries point at the same subtable.
	putEntry(pages, 0, 3, 0x100)
	putEntry(pages, testEntry, 3, 0x100)
	putEntry(pages, 0x100, 1, 0x300)
	putEntry(pages, 0x100+testEntry, 1, 0x308)

	auth := authorizeWrites(t, Region{Start: 0x300, Size: 16, Allow: AccessWrite})
	plan, err := New(pages, auth).Validate(Request{Address: 0, Length: 2 * testEntry, Format: format})
	if err != nil {
		t.Fatal(err)
	}
	writes := plan.Writes()
	if len(writes) != 4 {
		t.Fatalf("got %d writes, want 4 (shared table expanded twice)", len(writes))
	}
	refs := plan.References()
	wantRefs := []PageID{0, 1, 3}
	if len(refs) != len(wantRefs) {
		t.Fatalf("got refs %v, want %v", refs, wantRefs)
	}
	for i := range refs {
		if refs[i] != wantRefs[i] {
			t.Fatalf("got refs %v, want %v", refs, wantRefs)
		}
	}

	var rec recorder
	freezer := recordingFreezer{r: &rec, fail: ^PageID(0)}
	submitter := recordingSubmitter{r: &rec}
	if err := plan.Submit(freezer, submitter); err != nil {
		t.Fatal(err)
	}
	if rec.submits != 1 {
		t.Fatalf("got %d submit calls, want 1", rec.submits)
	}
	if len(rec.frozen) != len(wantRefs) {
		t.Fatalf("got frozen pages %v, want each referenced page once: %v", rec.frozen, wantRefs)
	}
	if rec.events[len(rec.events)-1] != "submit" {
		t.Fatalf("submit happened before all freezes: %v", rec.events)
	}
}

func TestSelfModifyingTableUsesFrozenSnapshot(t *testing.T) {
	pages := newTestPages()
	table := &Table{
		HeaderSize: 0,
		Count:      CountSpec{Mode: CountFixed, Value: 2},
	}
	format := testFormat(table)
	table.Format = format

	// Entry 0 overwrites entry 1 in the same table. If validation executed
	// writes inline, entry 1 would no longer be the original write opcode.
	putEntry(pages, 0, 3, 0x100)
	putEntry(pages, 0x100, 1, 0x110) // write into the next table entry
	putEntry(pages, 0x110, 1, 0x300)

	auth := authorizeWrites(t,
		Region{Start: 0x100, Size: 2 * testEntry, Allow: AccessWrite},
		Region{Start: 0x300, Size: 8, Allow: AccessWrite},
	)
	plan, err := New(pages, auth).Validate(Request{Address: 0, Length: testEntry, Format: format})
	if err != nil {
		t.Fatal(err)
	}
	writes := plan.Writes()
	if len(writes) != 2 {
		t.Fatalf("got %d writes, want 2", len(writes))
	}
	if writes[0].Address != 0x110 || writes[1].Address != 0x300 {
		t.Fatalf("unexpected writes: %+v", writes)
	}
	if writes[0].Entry != 0x100 {
		t.Fatalf("write provenance lost: %+v", writes[0])
	}
}

func TestInvalidTailCausesZeroExecution(t *testing.T) {
	tests := []struct {
		name    string
		build   func(*testPages, *Format, *Table)
		length  uint64
		wantErr error
	}{
		{
			name: "partial trailing entry",
			build: func(pages *testPages, _ *Format, _ *Table) {
				putEntry(pages, 0, 1, 0x300)
			},
			length:  2*testEntry - 1,
			wantErr: ErrTail,
		},
		{
			name: "invalid tail opcode in self modifying table",
			build: func(pages *testPages, _ *Format, table *Table) {
				table.Count.Value = 3
				putEntry(pages, 0, 3, 0x100)
				putEntry(pages, 0x100, 1, 0x110)
				putEntry(pages, 0x110, 1, 0x300)
				putEntry(pages, 0x120, 0xff, 0x308)
			},
			length:  testEntry,
			wantErr: ErrUnknownOpcode,
		},
		{
			name: "invalid tail after shared subtable write",
			build: func(pages *testPages, _ *Format, table *Table) {
				table.Count.Value = 1
				putEntry(pages, 0, 3, 0x100)
				putEntry(pages, testEntry, 0xff, 0)
				putEntry(pages, 0x100, 1, 0x300)
			},
			length:  2 * testEntry,
			wantErr: ErrUnknownOpcode,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pages := newTestPages()
			table := &Table{
				HeaderSize: 0,
				Count:      CountSpec{Mode: CountFixed, Value: 2},
			}
			format := testFormat(table)
			table.Format = format
			tc.build(pages, format, table)
			auth := authorizeWrites(t,
				Region{Start: 0x100, Size: 2 * testEntry, Allow: AccessWrite},
				Region{Start: 0x300, Size: 8, Allow: AccessWrite},
			)
			plan, err := New(pages, auth).Validate(Request{
				Address: 0,
				Length:  tc.length,
				Format:  format,
			})
			if err == nil {
				t.Fatal("expected validation failure")
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("got %v, want %v", err, tc.wantErr)
			}
			if plan != nil {
				t.Fatal("validation returned a plan for an invalid descriptor")
			}

			var rec recorder
			if rec.submits != 0 || len(rec.frozen) != 0 {
				t.Fatalf("invalid descriptor caused execution: %+v", rec)
			}
		})
	}
}

func TestCountBytesFieldInvalidRemainderIsTail(t *testing.T) {
	pages := newTestPages()
	table := &Table{
		HeaderSize: 4,
		Count: CountSpec{
			Mode:  CountBytesField,
			Field: &ScalarField{ByteOffset: 0, ByteSize: 4},
		},
	}
	format := testFormat(table)
	table.Format = format

	putEntry(pages, 0, 3, 0x100)
	header := make([]byte, 4)
	binary.LittleEndian.PutUint32(header, testEntry+1) // partial second entry
	pages.write(0x100, header)

	auth := authorizeWrites(t)
	plan, err := New(pages, auth).Validate(Request{Address: 0, Length: testEntry, Format: format})
	if err == nil || !errors.Is(err, ErrTail) {
		t.Fatalf("got %v, want ErrTail", err)
	}
	if plan != nil {
		t.Fatal("got plan for invalid table length")
	}
}

func TestCountFieldTable(t *testing.T) {
	pages := newTestPages()
	table := &Table{
		HeaderSize: 8,
		Count: CountSpec{
			Mode:  CountField,
			Field: &ScalarField{ByteOffset: 0, ByteSize: 4},
		},
	}
	format := testFormat(table)
	table.Format = format

	putEntry(pages, 0, 3, 0x100)
	pages.write(0x100, []byte{2, 0, 0, 0, 0, 0, 0, 0})
	putEntry(pages, 0x108, 1, 0x300)
	putEntry(pages, 0x108+testEntry, 1, 0x308)

	auth := authorizeWrites(t, Region{Start: 0x300, Size: 16, Allow: AccessWrite})
	plan, err := New(pages, auth).Validate(Request{Address: 0, Length: testEntry, Format: format})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Writes()) != 2 {
		t.Fatalf("got %d writes, want 2", len(plan.Writes()))
	}
}

func TestIndirectCycleIsRejected(t *testing.T) {
	pages := newTestPages()
	table := &Table{
		HeaderSize: 0,
		Count:      CountSpec{Mode: CountFixed, Value: 1},
	}
	format := testFormat(table)
	table.Format = format

	putEntry(pages, 0, 3, 0x100)
	putEntry(pages, 0x100, 3, 0x100) // table points to itself

	auth := authorizeWrites(t)
	plan, err := New(pages, auth).Validate(Request{Address: 0, Length: testEntry, Format: format})
	if err == nil || !errors.Is(err, ErrCycle) {
		t.Fatalf("got %v, want ErrCycle", err)
	}
	if plan != nil {
		t.Fatal("got plan for cyclic table graph")
	}
}

func TestFreezeFailurePreventsSubmit(t *testing.T) {
	pages := newTestPages()
	format := testFormat(nil)
	putEntry(pages, 0, 1, 0x300)
	auth := authorizeWrites(t, Region{Start: 0x300, Size: 8, Allow: AccessWrite})
	plan, err := New(pages, auth).Validate(Request{Address: 0, Length: testEntry, Format: format})
	if err != nil {
		t.Fatal(err)
	}

	var rec recorder
	err = plan.Submit(recordingFreezer{r: &rec, fail: 0}, recordingSubmitter{r: &rec})
	if !errors.Is(err, ErrFreeze) {
		t.Fatalf("got %v, want ErrFreeze", err)
	}
	if rec.submits != 0 {
		t.Fatalf("submitted %d times after freeze failure", rec.submits)
	}
}

func TestCheckReadsCoversIndirectTableMemory(t *testing.T) {
	pages := newTestPages()
	table := &Table{
		HeaderSize: 0,
		Count:      CountSpec{Mode: CountFixed, Value: 1},
	}
	format := testFormat(table)
	table.Format = format
	putEntry(pages, 0, 3, 0x100)
	putEntry(pages, 0x100, 1, 0x300)

	v := New(pages, authorizeWrites(t,
		Region{Start: 0x300, Size: 8, Allow: AccessWrite},
	))
	v.CheckReads = true
	plan, err := v.Validate(Request{Address: 0, Length: testEntry, Format: format})
	if err == nil || !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("got %v, want ErrUnauthorized for unreadable indirect table", err)
	}
	if plan != nil {
		t.Fatal("got plan with unreadable indirect table")
	}
}

func TestEntryLimitRejectsBeforeExecution(t *testing.T) {
	pages := newTestPages()
	putEntry(pages, 0, 1, 0x300)
	putEntry(pages, testEntry, 1, 0x308)
	v := New(pages, authorizeWrites(t,
		Region{Start: 0x300, Size: 16, Allow: AccessWrite},
	))
	v.Limits.MaxEntries = 1
	plan, err := v.Validate(Request{Address: 0, Length: 2 * testEntry, Format: testFormat(nil)})
	if err == nil || !errors.Is(err, ErrLimit) {
		t.Fatalf("got %v, want ErrLimit", err)
	}
	if plan != nil {
		t.Fatal("got plan after entry limit was exceeded")
	}
}

func TestUnknownRootOpcode(t *testing.T) {
	pages := newTestPages()
	putEntry(pages, 0, 0x7f, 0)
	plan, err := New(pages, authorizeWrites(t)).Validate(Request{
		Address: 0,
		Length:  testEntry,
		Format:  testFormat(nil),
	})
	if err == nil || !errors.Is(err, ErrUnknownOpcode) {
		t.Fatalf("got %v, want ErrUnknownOpcode", err)
	}
	if plan != nil {
		t.Fatal("got plan for unknown opcode")
	}
}

func TestRegionSetUnionCoverage(t *testing.T) {
	set := authorizeWrites(t,
		Region{Start: 0x300, Size: 8, Allow: AccessWrite},
		Region{Start: 0x308, Size: 8, Allow: AccessWrite},
	)
	if err := set.Authorize(0x300, 16, AccessWrite); err != nil {
		t.Fatalf("adjacent regions should permit the whole span: %v", err)
	}
	gapped := authorizeWrites(t,
		Region{Start: 0x300, Size: 4, Allow: AccessWrite},
		Region{Start: 0x30c, Size: 4, Allow: AccessWrite},
	)
	if err := gapped.Authorize(0x300, 16, AccessWrite); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("got %v, want ErrUnauthorized for a gap", err)
	}
}
