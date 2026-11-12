package netsec

import (
	"encoding/binary"
	"errors"
	"math"
	"testing"
)

const (
	reclaimBase uint64 = 0x10000
	reclaimSize uint64 = 0x10000
	nvsBase     uint64 = 0x30000
	nvsSize     uint64 = 0x10000
	ramBase     uint64 = 0x50000
	ramSize     uint64 = 0x10000
)

func testMemoryMap() MemoryMap {
	return MemoryMap{Regions: []MemoryRegion{
		{Base: reclaimBase, Length: reclaimSize, Type: MemoryACPIReclaim},
		{Base: nvsBase, Length: nvsSize, Type: MemoryACPINVS},
		{Base: ramBase, Length: ramSize, Type: MemoryConventional},
	}}
}

// buildTable creates a syntactically valid table with a sealed checksum.
func buildTable(signature string, length int) []byte {
	data := make([]byte, length)
	copy(data[0:4], signature)
	binary.LittleEndian.PutUint32(data[4:8], uint32(length))
	data[8] = 2
	copy(data[10:16], "TESTOEM")
	copy(data[16:24], "TESTTBL0")
	binary.LittleEndian.PutUint32(data[24:28], 7)
	copy(data[28:32], "TEST")
	binary.LittleEndian.PutUint32(data[32:36], 1)
	sealTable(data)
	return data
}

// sealTable recomputes the checksum over the table's declared length.
func sealTable(data []byte) {
	length := int(binary.LittleEndian.Uint32(data[4:8]))
	data[9] = 0
	var sum uint8
	for _, b := range data[:length] {
		sum += b
	}
	data[9] = 0 - sum
}

func buildXSDT(entries ...uint64) []byte {
	data := buildTable(xsdtSignature, HeaderSize+8*len(entries))
	for i, entry := range entries {
		binary.LittleEndian.PutUint64(data[HeaderSize+i*8:HeaderSize+(i+1)*8], entry)
	}
	sealTable(data)
	return data
}

func buildRSDT(entries ...uint32) []byte {
	data := buildTable(rsdtSignature, HeaderSize+4*len(entries))
	for i, entry := range entries {
		binary.LittleEndian.PutUint32(data[HeaderSize+i*4:HeaderSize+(i+1)*4], entry)
	}
	sealTable(data)
	return data
}

func candidate(signature string, address uint64, source TableSource) Candidate {
	return Candidate{
		Data:            buildTable(signature, 64),
		PhysicalAddress: address,
		Source:          source,
	}
}

func requireIs(t *testing.T, err error, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("got error %v, want errors.Is(%v)", err, target)
	}
}

func TestAdmitAcceptsLegalFirmwareTables(t *testing.T) {
	dsdt := buildTable("DSDT", 64)
	ssdt := buildTable("SSDT", 48)

	got, err := Admit([]Candidate{
		{Data: dsdt, PhysicalAddress: reclaimBase, Source: SourceFirmware},
		{Data: ssdt, PhysicalAddress: nvsBase, Source: SourceFirmware},
	}, testMemoryMap(), LockModeNone)
	if err != nil {
		t.Fatalf("Admit returned error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d admitted tables, want 2", len(got))
	}
	if got[0].Signature != "DSDT" || got[0].Length != 64 {
		t.Fatalf("first table = %q/%d, want DSDT/64", got[0].Signature, got[0].Length)
	}
	if got[1].Signature != "SSDT" || got[1].Length != 48 {
		t.Fatalf("second table = %q/%d, want SSDT/48", got[1].Signature, got[1].Length)
	}
	if got[0].PhysicalAddress != reclaimBase || got[1].PhysicalAddress != nvsBase {
		t.Fatalf("admitted addresses = %#x/%#x", got[0].PhysicalAddress, got[1].PhysicalAddress)
	}
}

func TestAdmitCopiesAdmittedBytes(t *testing.T) {
	data := buildTable("DSDT", 48)
	got, err := Admit([]Candidate{{
		Data: data, PhysicalAddress: reclaimBase, Source: SourceFirmware,
	}}, testMemoryMap(), LockModeNone)
	if err != nil {
		t.Fatalf("Admit returned error: %v", err)
	}
	data[9]++
	if checksum(got[0].Data) != 0 {
		t.Fatal("admitted data changed when caller mutated its input slice")
	}
}

func TestRejectsBadChecksum(t *testing.T) {
	data := buildTable("DSDT", 64)
	data[9]++

	_, err := Admit([]Candidate{{
		Data: data, PhysicalAddress: reclaimBase, Source: SourceFirmware,
	}}, testMemoryMap(), LockModeNone)
	requireIs(t, err, ErrBadChecksum)
}

func TestRejectsTableInConventionalRAM(t *testing.T) {
	_, err := Admit([]Candidate{{
		Data:            buildTable("DSDT", 64),
		PhysicalAddress: ramBase + 0x200,
		Source:          SourceFirmware,
	}}, testMemoryMap(), LockModeNone)
	requireIs(t, err, ErrOutOfRegion)
}

func TestRejectsTableOutsideMemoryMap(t *testing.T) {
	_, err := Admit([]Candidate{{
		Data:            buildTable("DSDT", 64),
		PhysicalAddress: 0xdead0000,
		Source:          SourceFirmware,
	}}, testMemoryMap(), LockModeNone)
	requireIs(t, err, ErrOutOfRegion)
}

func TestRejectsLengthCrossingRegion(t *testing.T) {
	memoryMap := MemoryMap{Regions: []MemoryRegion{
		{Base: 0x1000, Length: 0x30, Type: MemoryACPIReclaim},
	}}
	_, err := Admit([]Candidate{{
		Data:            buildTable("DSDT", 0x40),
		PhysicalAddress: 0x1000,
		Source:          SourceFirmware,
	}}, memoryMap, LockModeNone)
	requireIs(t, err, ErrOutOfRegion)
}

func TestTableMayFillRegionExactly(t *testing.T) {
	memoryMap := MemoryMap{Regions: []MemoryRegion{
		{Base: 0x1000, Length: 0x40, Type: MemoryACPIReclaim},
	}}
	got, err := Admit([]Candidate{{
		Data:            buildTable("DSDT", 0x40),
		PhysicalAddress: 0x1000,
		Source:          SourceFirmware,
	}}, memoryMap, LockModeNone)
	if err != nil {
		t.Fatalf("table filling the region exactly was rejected: %v", err)
	}
	if got[0].Length != 0x40 {
		t.Fatalf("admitted length = %#x, want 0x40", got[0].Length)
	}
}

func TestLengthBounds(t *testing.T) {
	t.Run("shorter than header", func(t *testing.T) {
		data := buildTable("DSDT", 48)[:20]
		_, err := Admit([]Candidate{{
			Data: data, PhysicalAddress: reclaimBase, Source: SourceFirmware,
		}}, testMemoryMap(), LockModeNone)
		requireIs(t, err, ErrHeaderTooShort)
	})

	t.Run("declared length below header", func(t *testing.T) {
		data := buildTable("DSDT", 48)
		binary.LittleEndian.PutUint32(data[4:8], 32)
		sealTable(data)
		_, err := Admit([]Candidate{{
			Data: data, PhysicalAddress: reclaimBase, Source: SourceFirmware,
		}}, testMemoryMap(), LockModeNone)
		requireIs(t, err, ErrLengthTooSmall)
	})

	t.Run("declared length exceeds data", func(t *testing.T) {
		data := buildTable("DSDT", 48)
		_, err := Admit([]Candidate{{
			Data: data[:40], PhysicalAddress: reclaimBase, Source: SourceFirmware,
		}}, testMemoryMap(), LockModeNone)
		requireIs(t, err, ErrTruncated)
	})
}

func TestChecksumIgnoresTrailingPadding(t *testing.T) {
	data := buildTable("DSDT", 64)
	binary.LittleEndian.PutUint32(data[4:8], 48)
	sealTable(data)
	for i := 48; i < len(data); i++ {
		data[i] = 0xff
	}

	got, err := Admit([]Candidate{{
		Data: data, PhysicalAddress: reclaimBase, Source: SourceFirmware,
	}}, testMemoryMap(), LockModeNone)
	if err != nil {
		t.Fatalf("Admit returned error: %v", err)
	}
	if len(got[0].Data) != 48 {
		t.Fatalf("admitted data length = %d, want declared length 48", len(got[0].Data))
	}
}

func TestIntegrityLockControlsInitRDOverride(t *testing.T) {
	initrd := candidate("SSDT", reclaimBase+0x100, SourceInitRD)
	firmware := candidate("SSDT", reclaimBase+0x200, SourceFirmware)

	if err := Validate([]Candidate{initrd}, testMemoryMap(), LockModeNone); err != nil {
		t.Fatalf("permissive mode rejected a valid initrd table: %v", err)
	}
	err := Validate([]Candidate{initrd}, testMemoryMap(), LockModeIntegrity)
	requireIs(t, err, ErrInitRDOverride)

	if err := Validate([]Candidate{firmware}, testMemoryMap(), LockModeIntegrity); err != nil {
		t.Fatalf("integrity mode rejected a firmware table: %v", err)
	}
}

func TestIntegrityLockRejectsAnyInitRDTable(t *testing.T) {
	_, err := Admit([]Candidate{
		candidate("DSDT", reclaimBase, SourceFirmware),
		candidate("SSDT", nvsBase, SourceInitRD),
	}, testMemoryMap(), LockModeIntegrity)
	requireIs(t, err, ErrInitRDOverride)

	var admissionError *AdmissionError
	if !errors.As(err, &admissionError) {
		t.Fatalf("error %v is not an AdmissionError", err)
	}
	if admissionError.TableIndex != 1 {
		t.Fatalf("rejected table index = %d, want 1", admissionError.TableIndex)
	}
}

func TestRejectsDuplicateSignature(t *testing.T) {
	_, err := Admit([]Candidate{
		candidate("DSDT", reclaimBase, SourceFirmware),
		candidate("DSDT", reclaimBase+0x100, SourceFirmware),
	}, testMemoryMap(), LockModeNone)
	requireIs(t, err, ErrDuplicateSignature)
}

func TestRejectsDuplicateSSDTSignature(t *testing.T) {
	_, err := Admit([]Candidate{
		candidate("SSDT", reclaimBase, SourceFirmware),
		candidate("SSDT", nvsBase, SourceFirmware),
	}, testMemoryMap(), LockModeNone)
	requireIs(t, err, ErrDuplicateSignature)
}

func TestRejectsAddressWrap(t *testing.T) {
	t.Run("table range wraps", func(t *testing.T) {
		_, err := Admit([]Candidate{{
			Data:            buildTable("DSDT", 64),
			PhysicalAddress: math.MaxUint64 - 10,
			Source:          SourceFirmware,
		}}, testMemoryMap(), LockModeNone)
		requireIs(t, err, ErrAddressWrap)
	})

	t.Run("memory region wraps", func(t *testing.T) {
		memoryMap := MemoryMap{Regions: []MemoryRegion{
			{Base: math.MaxUint64 - 15, Length: 32, Type: MemoryACPIReclaim},
		}}
		_, err := Admit(nil, memoryMap, LockModeNone)
		requireIs(t, err, ErrAddressWrap)
	})
}

func TestXSDTPointers(t *testing.T) {
	t.Run("valid pointers in reclaim and NVS", func(t *testing.T) {
		first := reclaimBase + 0x400
		second := nvsBase + 0x400
		xsdt := buildXSDT(first, second)

		got, err := Admit([]Candidate{{
			Data: xsdt, PhysicalAddress: reclaimBase, Source: SourceFirmware,
		}}, testMemoryMap(), LockModeNone)
		if err != nil {
			t.Fatalf("Admit returned error: %v", err)
		}
		if len(got[0].ReferencedAddresses) != 2 ||
			got[0].ReferencedAddresses[0] != first ||
			got[0].ReferencedAddresses[1] != second {
			t.Fatalf("referenced addresses = %#v", got[0].ReferencedAddresses)
		}
	})

	t.Run("pointer into conventional RAM", func(t *testing.T) {
		xsdt := buildXSDT(reclaimBase+0x400, ramBase+0x400)
		_, err := Admit([]Candidate{{
			Data: xsdt, PhysicalAddress: reclaimBase, Source: SourceFirmware,
		}}, testMemoryMap(), LockModeNone)
		requireIs(t, err, ErrOutOfRegion)
	})

	t.Run("pointer outside any region", func(t *testing.T) {
		xsdt := buildXSDT(0xdead0000)
		_, err := Admit([]Candidate{{
			Data: xsdt, PhysicalAddress: reclaimBase, Source: SourceFirmware,
		}}, testMemoryMap(), LockModeNone)
		requireIs(t, err, ErrOutOfRegion)
	})

	t.Run("zero pointer", func(t *testing.T) {
		xsdt := buildXSDT(0)
		_, err := Admit([]Candidate{{
			Data: xsdt, PhysicalAddress: reclaimBase, Source: SourceFirmware,
		}}, testMemoryMap(), LockModeNone)
		requireIs(t, err, ErrOutOfRegion)
	})

	t.Run("pointer address wraps", func(t *testing.T) {
		xsdt := buildXSDT(math.MaxUint64 - 10)
		_, err := Admit([]Candidate{{
			Data: xsdt, PhysicalAddress: reclaimBase, Source: SourceFirmware,
		}}, testMemoryMap(), LockModeNone)
		requireIs(t, err, ErrAddressWrap)
	})

	t.Run("entry body not eight-byte aligned", func(t *testing.T) {
		xsdt := buildTable(xsdtSignature, HeaderSize+12)
		_, err := Admit([]Candidate{{
			Data: xsdt, PhysicalAddress: reclaimBase, Source: SourceFirmware,
		}}, testMemoryMap(), LockModeNone)
		requireIs(t, err, ErrMalformedXSDT)
	})

	t.Run("duplicate pointer", func(t *testing.T) {
		xsdt := buildXSDT(reclaimBase+0x400, reclaimBase+0x400)
		_, err := Admit([]Candidate{{
			Data: xsdt, PhysicalAddress: reclaimBase, Source: SourceFirmware,
		}}, testMemoryMap(), LockModeNone)
		requireIs(t, err, ErrDuplicateSignature)
	})

	t.Run("pointer header fits at region end", func(t *testing.T) {
		memoryMap := MemoryMap{Regions: []MemoryRegion{
			{Base: 0x1000, Length: 0x100, Type: MemoryACPIReclaim},
			{Base: 0x2000, Length: 0x40, Type: MemoryACPINVS},
		}}
		xsdt := buildXSDT(0x2000 + 0x40 - HeaderSize)
		_, err := Admit([]Candidate{{
			Data: xsdt, PhysicalAddress: 0x1000, Source: SourceFirmware,
		}}, memoryMap, LockModeNone)
		if err != nil {
			t.Fatalf("pointer with exactly a header of room was rejected: %v", err)
		}

		xsdt = buildXSDT(0x2000 + 0x40 - HeaderSize + 1)
		_, err = Admit([]Candidate{{
			Data: xsdt, PhysicalAddress: 0x1000, Source: SourceFirmware,
		}}, memoryMap, LockModeNone)
		requireIs(t, err, ErrOutOfRegion)
	})
}

func TestXSDTWithSuppliedTargetTable(t *testing.T) {
	target := reclaimBase + 0x400
	xsdt := Candidate{
		Data:            buildXSDT(target),
		PhysicalAddress: reclaimBase,
		Source:          SourceFirmware,
	}
	ssdt := candidate("SSDT", target, SourceFirmware)

	got, err := Admit([]Candidate{xsdt, ssdt}, testMemoryMap(), LockModeNone)
	if err != nil {
		t.Fatalf("Admit returned error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d admitted tables, want 2", len(got))
	}
	if len(got[0].ReferencedAddresses) != 1 || got[0].ReferencedAddresses[0] != target {
		t.Fatalf("referenced addresses = %#v", got[0].ReferencedAddresses)
	}
}

func TestRSDTPointers(t *testing.T) {
	entry := uint32(reclaimBase + 0x800)
	rsdt := buildRSDT(entry)
	got, err := Admit([]Candidate{{
		Data: rsdt, PhysicalAddress: reclaimBase, Source: SourceFirmware,
	}}, testMemoryMap(), LockModeNone)
	if err != nil {
		t.Fatalf("Admit returned error: %v", err)
	}
	if len(got[0].ReferencedAddresses) != 1 || got[0].ReferencedAddresses[0] != uint64(entry) {
		t.Fatalf("referenced addresses = %#v", got[0].ReferencedAddresses)
	}

	rsdt = buildRSDT(uint32(ramBase + 0x800))
	_, err = Admit([]Candidate{{
		Data: rsdt, PhysicalAddress: reclaimBase, Source: SourceFirmware,
	}}, testMemoryMap(), LockModeNone)
	requireIs(t, err, ErrOutOfRegion)
}

func TestRejectsUnknownModeAndSource(t *testing.T) {
	_, err := Admit(nil, testMemoryMap(), LockMode(9))
	requireIs(t, err, ErrInvalidLockMode)

	_, err = Admit([]Candidate{{
		Data:            buildTable("DSDT", 48),
		PhysicalAddress: reclaimBase,
		Source:          TableSource(9),
	}}, testMemoryMap(), LockModeNone)
	requireIs(t, err, ErrInvalidSource)
}

func TestParseHeaderMetadata(t *testing.T) {
	header, err := ParseHeader(buildTable("DSDT", 48))
	if err != nil {
		t.Fatalf("ParseHeader returned error: %v", err)
	}
	if header.Signature != "DSDT" || header.Length != 48 || header.Revision != 2 {
		t.Fatalf("unexpected header: %+v", header)
	}
}
