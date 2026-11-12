package netsec

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// HeaderSize is the size of the common ACPI table header.
const HeaderSize = 36

const (
	xsdtSignature = "XSDT"
	rsdtSignature = "RSDT"
)

// Sentinel errors returned by ParseHeader, Admit, and Validate. Callers
// should test them with errors.Is because the package adds table and address
// context to the returned error.
var (
	ErrHeaderTooShort     = errors.New("acpi: table is shorter than the common header")
	ErrLengthTooSmall     = errors.New("acpi: table length is smaller than the common header")
	ErrTruncated          = errors.New("acpi: declared table length exceeds supplied bytes")
	ErrBadChecksum        = errors.New("acpi: table checksum does not sum to zero")
	ErrInvalidSignature   = errors.New("acpi: invalid table signature")
	ErrInvalidSource      = errors.New("acpi: unknown table source")
	ErrInvalidLockMode    = errors.New("acpi: unknown lock mode")
	ErrInitRDOverride     = errors.New("acpi: initrd override rejected while integrity lock is enabled")
	ErrDuplicateSignature = errors.New("acpi: duplicate table signature")
	ErrOutOfRegion        = errors.New("acpi: table is not wholly inside ACPI reclaim or ACPI NVS memory")
	ErrAddressWrap        = errors.New("acpi: address arithmetic wraps around")
	ErrMalformedXSDT      = errors.New("acpi: malformed XSDT or RSDT entry array")
)

// TableSource identifies where a candidate table came from.
type TableSource uint8

const (
	// SourceFirmware is the zero value: the table was supplied by firmware.
	SourceFirmware TableSource = iota
	// SourceInitRD marks an override supplied by the initrd.
	SourceInitRD
)

// String returns a stable name suitable for diagnostics.
func (s TableSource) String() string {
	switch s {
	case SourceFirmware:
		return "firmware"
	case SourceInitRD:
		return "initrd"
	default:
		return fmt.Sprintf("table-source(%d)", uint8(s))
	}
}

// LockMode controls whether initrd overrides are admitted.
type LockMode uint8

const (
	// LockModeNone permits initrd tables after their contents are checked.
	LockModeNone LockMode = iota
	// LockModeIntegrity rejects every initrd-supplied table.
	LockModeIntegrity
)

// String returns a stable name suitable for diagnostics.
func (m LockMode) String() string {
	switch m {
	case LockModeNone:
		return "none"
	case LockModeIntegrity:
		return "integrity"
	default:
		return fmt.Sprintf("lock-mode(%d)", uint8(m))
	}
}

// Header is a parsed ACPI common table header.
type Header struct {
	Signature       string
	Length          uint32
	Revision        uint8
	Checksum        uint8
	OEMID           string
	OEMTableID      string
	OEMRevision     uint32
	CreatorID       string
	CreatorRevision uint32
}

// Candidate is a table offered to the boot flow for admission. Data must
// contain the complete raw table, including the 36-byte common header.
// PhysicalAddress is the address at which the table currently resides.
type Candidate struct {
	Data            []byte
	PhysicalAddress uint64
	Source          TableSource
}

// AdmittedTable is a table that passed all admission checks. Data is a copy
// of the declared-length portion of the candidate, so later mutation of the
// caller's slice cannot change the admitted table.
type AdmittedTable struct {
	Header
	Data                []byte
	PhysicalAddress     uint64
	Source              TableSource
	ReferencedAddresses []uint64
}

// AdmissionError adds table context to a sentinel error.
type AdmissionError struct {
	TableIndex int
	EntryIndex int
	Signature  string
	Address    uint64
	Err        error
}

func (e *AdmissionError) Error() string {
	if e == nil {
		return "<nil>"
	}
	context := ""
	if e.TableIndex >= 0 {
		context += fmt.Sprintf(" table[%d]", e.TableIndex)
	}
	if e.EntryIndex >= 0 {
		context += fmt.Sprintf(" entry[%d]", e.EntryIndex)
	}
	if e.Signature != "" {
		context += fmt.Sprintf(" signature=%q", e.Signature)
	}
	if e.Address != 0 {
		context += fmt.Sprintf(" address=%#x", e.Address)
	}
	if context == "" {
		return e.Err.Error()
	}
	return "acpi:" + context + ": " + e.Err.Error()
}

// Unwrap makes errors.Is work with the exported sentinel errors.
func (e *AdmissionError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// ParseHeader validates the common header, declared length, and checksum of a
// raw ACPI table. Checksum bytes beyond the declared length are padding and
// are intentionally ignored.
func ParseHeader(data []byte) (Header, error) {
	if len(data) < HeaderSize {
		return Header{}, fmt.Errorf("%w: got %d bytes", ErrHeaderTooShort, len(data))
	}

	header := Header{
		Signature:       string(data[0:4]),
		Length:          binary.LittleEndian.Uint32(data[4:8]),
		Revision:        data[8],
		Checksum:        data[9],
		OEMID:           string(data[10:16]),
		OEMTableID:      string(data[16:24]),
		OEMRevision:     binary.LittleEndian.Uint32(data[24:28]),
		CreatorID:       string(data[28:32]),
		CreatorRevision: binary.LittleEndian.Uint32(data[32:36]),
	}

	if header.Length < HeaderSize {
		return Header{}, fmt.Errorf("%w: declared %d bytes", ErrLengthTooSmall, header.Length)
	}
	if uint64(header.Length) > uint64(len(data)) {
		return Header{}, fmt.Errorf("%w: declared %d bytes, got %d",
			ErrTruncated, header.Length, len(data))
	}
	if !validSignature(header.Signature) {
		return Header{}, fmt.Errorf("%w: %q", ErrInvalidSignature, header.Signature)
	}
	if checksum(data[:header.Length]) != 0 {
		return Header{}, fmt.Errorf("%w: declared length %d", ErrBadChecksum, header.Length)
	}
	return header, nil
}

// Admit validates the candidate tables and returns the admitted subset in the
// same order. All-or-nothing semantics apply: on error no tables are returned.
//
// The checks are:
//   - integrity lock rejects every initrd-sourced candidate;
//   - every table header checksum sums to zero over its declared length;
//   - the declared length is at least 36 and fits the supplied data;
//   - the whole table lies inside one ACPI reclaim or ACPI NVS region;
//   - no two tables share a signature, and an XSDT/RSDT cannot reference the
//     same address twice;
//   - every XSDT/RSDT pointer and every table range is free of address wrap.
//
// A pointer's target bytes cannot be inspected through this API. If a table
// with the same physical address is supplied, its full range has already
// been checked; otherwise only the pointer's ACPI header-sized footprint is
// checked against the memory map.
func Admit(candidates []Candidate, memoryMap MemoryMap, mode LockMode) ([]AdmittedTable, error) {
	if mode != LockModeNone && mode != LockModeIntegrity {
		return nil, fmt.Errorf("%w: %d", ErrInvalidLockMode, uint8(mode))
	}
	if err := memoryMap.Validate(); err != nil {
		return nil, err
	}

	if mode == LockModeIntegrity {
		for i := range candidates {
			if candidates[i].Source == SourceInitRD {
				return nil, &AdmissionError{
					TableIndex: i,
					EntryIndex: -1,
					Address:    candidates[i].PhysicalAddress,
					Err:        ErrInitRDOverride,
				}
			}
		}
	}

	admitted := make([]AdmittedTable, 0, len(candidates))
	seenSignatures := make(map[string]int, len(candidates))

	for i, candidate := range candidates {
		header, err := ParseHeader(candidate.Data)
		if err != nil {
			return nil, &AdmissionError{
				TableIndex: i,
				EntryIndex: -1,
				Address:    candidate.PhysicalAddress,
				Err:        err,
			}
		}
		if candidate.Source != SourceFirmware && candidate.Source != SourceInitRD {
			return nil, &AdmissionError{
				TableIndex: i,
				EntryIndex: -1,
				Signature:  header.Signature,
				Address:    candidate.PhysicalAddress,
				Err:        ErrInvalidSource,
			}
		}
		if previous, ok := seenSignatures[header.Signature]; ok {
			return nil, &AdmissionError{
				TableIndex: i,
				EntryIndex: -1,
				Signature:  header.Signature,
				Address:    candidate.PhysicalAddress,
				Err: fmt.Errorf("%w: first seen at table[%d]",
					ErrDuplicateSignature, previous),
			}
		}

		if _, ok := addWithoutWrap(candidate.PhysicalAddress, uint64(header.Length)); !ok {
			return nil, &AdmissionError{
				TableIndex: i,
				EntryIndex: -1,
				Signature:  header.Signature,
				Address:    candidate.PhysicalAddress,
				Err:        ErrAddressWrap,
			}
		}
		if _, found := memoryMap.findRegion(candidate.PhysicalAddress, uint64(header.Length), true); !found {
			return nil, &AdmissionError{
				TableIndex: i,
				EntryIndex: -1,
				Signature:  header.Signature,
				Address:    candidate.PhysicalAddress,
				Err:        ErrOutOfRegion,
			}
		}

		var referenced []uint64
		if header.Signature == xsdtSignature || header.Signature == rsdtSignature {
			referenced, err = parseEntryArray(candidate.Data[:header.Length], header)
			if err != nil {
				return nil, &AdmissionError{
					TableIndex: i,
					EntryIndex: -1,
					Signature:  header.Signature,
					Address:    candidate.PhysicalAddress,
					Err:        err,
				}
			}
			seenReferences := make(map[uint64]int, len(referenced))
			for j, address := range referenced {
				if err := validatePointer(memoryMap, address); err != nil {
					return nil, &AdmissionError{
						TableIndex: i,
						EntryIndex: j,
						Signature:  header.Signature,
						Address:    address,
						Err:        err,
					}
				}
				if previous, ok := seenReferences[address]; ok {
					return nil, &AdmissionError{
						TableIndex: i,
						EntryIndex: j,
						Signature:  header.Signature,
						Address:    address,
						Err: fmt.Errorf("%w: pointer repeats entry[%d]",
							ErrDuplicateSignature, previous),
					}
				}
				seenReferences[address] = j
			}
		}

		data := append([]byte(nil), candidate.Data[:header.Length]...)
		admitted = append(admitted, AdmittedTable{
			Header:              header,
			Data:                data,
			PhysicalAddress:     candidate.PhysicalAddress,
			Source:              candidate.Source,
			ReferencedAddresses: referenced,
		})
		seenSignatures[header.Signature] = i
	}

	return admitted, nil
}

// Validate is the decision-only form of Admit for boot paths that do not
// need the admitted table descriptors.
func Validate(candidates []Candidate, memoryMap MemoryMap, mode LockMode) error {
	_, err := Admit(candidates, memoryMap, mode)
	return err
}

func validatePointer(memoryMap MemoryMap, address uint64) error {
	if address == 0 {
		return ErrOutOfRegion
	}
	if _, ok := addWithoutWrap(address, HeaderSize); !ok {
		return ErrAddressWrap
	}
	if _, found := memoryMap.findRegion(address, HeaderSize, true); !found {
		return ErrOutOfRegion
	}
	return nil
}

func parseEntryArray(data []byte, header Header) ([]uint64, error) {
	width := 8
	if header.Signature == rsdtSignature {
		width = 4
	}
	body := data[HeaderSize:header.Length]
	if len(body)%width != 0 {
		return nil, fmt.Errorf("%w: %s body has %d bytes", ErrMalformedXSDT,
			header.Signature, len(body))
	}

	entries := make([]uint64, 0, len(body)/width)
	for offset := 0; offset < len(body); offset += width {
		if width == 8 {
			entries = append(entries, binary.LittleEndian.Uint64(body[offset:offset+8]))
		} else {
			entries = append(entries, uint64(binary.LittleEndian.Uint32(body[offset:offset+4])))
		}
	}
	return entries, nil
}

func validSignature(signature string) bool {
	if len(signature) != 4 {
		return false
	}
	for i := 0; i < len(signature); i++ {
		if signature[i] < 0x20 || signature[i] > 0x7e {
			return false
		}
	}
	return true
}

func checksum(data []byte) uint8 {
	var sum uint8
	for _, b := range data {
		sum += b
	}
	return sum
}
