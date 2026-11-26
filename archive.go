package apkverify

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"sort"
)

const (
	localFileHeaderSignature  = 0x04034b50
	centralDirHeaderSignature = 0x02014b50
	endOfCentralDirSignature  = 0x06054b50
	zip64EndOfCentralDirSig   = 0x06064b50
	zip64LocatorSignature     = 0x07064b50
	zip64ExtraFieldID         = 0x0001
	maxUint16                 = 0xffff
	maxUint32                 = 0xffffffff
	localFileHeaderSize       = 30
	centralDirHeaderSize      = 46
	endOfCentralDirSize       = 22
	maxEOCDCommentSize        = 0xffff
)

// Entry describes an entry discovered in both the local header area and the
// central directory. All offsets are relative to the start of the APK bytes.
type Entry struct {
	Name                   string
	LocalHeaderOffset      int64
	DataOffset             int64
	DataEnd                int64
	CompressedSize         uint64
	UncompressedSize       uint64
	CRC32                  uint32
	CompressionMethod      uint16
	Flags                  uint16
	CentralDirectoryOffset int64
}

type localEntry struct {
	Entry
}

type centralEntry struct {
	name             []byte
	crc32            uint32
	compressedSize   uint64
	uncompressedSize uint64
	method           uint16
	flags            uint16
	localOffset      int64
	centralOffset    int64
}

// Archive is a structurally validated APK parsed directly from a raw byte
// slice. Offsets are never accepted from the caller.
type Archive struct {
	DataSize               int64
	Entries                []Entry
	SigningBlock           *SigningBlock
	CentralDirectory       []byte
	EOCD                   []byte
	CentralDirectoryOffset int64
	EOCDRecordOffset       int64

	data              []byte
	localEnd          int
	signingBlockStart int
}

// Parse validates the ZIP/APK container structure and the APK Signing Block,
// if present. It does not perform cryptographic signature verification.
func Parse(data []byte) (*Archive, error) {
	if len(data) < localFileHeaderSize {
		return nil, fmt.Errorf("%w: data is too short", ErrNotAPK)
	}
	if binary.LittleEndian.Uint32(data[:4]) != localFileHeaderSignature {
		return nil, fmt.Errorf("%w: first four bytes are not a local file header", ErrNotAPK)
	}

	eocdOffset, err := findEOCD(data)
	if err != nil {
		return nil, err
	}
	eocd := data[eocdOffset:]
	cdSize := binary.LittleEndian.Uint32(eocd[12:16])
	cdOffset64 := binary.LittleEndian.Uint32(eocd[16:20])
	totalEntries := binary.LittleEndian.Uint16(eocd[10:12])
	diskEntries := binary.LittleEndian.Uint16(eocd[8:10])
	diskNumber := binary.LittleEndian.Uint16(eocd[4:6])
	cdDisk := binary.LittleEndian.Uint16(eocd[6:8])

	if diskNumber != 0 || cdDisk != 0 || diskEntries != totalEntries {
		return nil, fmt.Errorf("%w: multi-disk archives are not supported", ErrMalformedZIP)
	}
	if totalEntries == maxUint16 || diskEntries == maxUint16 ||
		cdSize == maxUint32 || cdOffset64 == maxUint32 {
		return nil, fmt.Errorf("%w: ZIP64 sentinel in EOCD", ErrZIP64)
	}
	if eocdOffset >= 20 && binary.LittleEndian.Uint32(data[eocdOffset-20:eocdOffset-16]) == zip64LocatorSignature {
		return nil, fmt.Errorf("%w: ZIP64 locator found", ErrZIP64)
	}

	if uint64(cdOffset64) > uint64(eocdOffset) ||
		uint64(cdSize) > uint64(eocdOffset)-uint64(cdOffset64) {
		return nil, fmt.Errorf("%w: central directory is outside the archive", ErrMalformedZIP)
	}
	cdOffset := int(cdOffset64)
	cdEnd := cdOffset + int(cdSize)
	if cdEnd != eocdOffset {
		return nil, fmt.Errorf("%w: central directory does not end at EOCD", ErrMalformedZIP)
	}

	central, err := parseCentralDirectory(data[cdOffset:cdEnd], cdOffset, int(totalEntries))
	if err != nil {
		return nil, err
	}

	local, localEnd, err := parseLocalEntries(data, cdOffset)
	if err != nil {
		return nil, err
	}
	if len(local) != len(central) {
		return nil, fmt.Errorf("%w: local entry count %d differs from central directory count %d",
			ErrEntryMismatch, len(local), len(central))
	}

	signing, signingStart, err := parseSigningBlock(data, cdOffset)
	if err != nil {
		return nil, err
	}
	if signingStart < 0 {
		signingStart = cdOffset
	}
	if localEnd != signingStart {
		return nil, fmt.Errorf("%w: %d unaccounted bytes before central directory", ErrMalformedZIP, signingStart-localEnd)
	}

	entries, err := matchEntries(local, central)
	if err != nil {
		return nil, err
	}
	if err := rejectOverlaps(entries); err != nil {
		return nil, err
	}

	archive := &Archive{
		DataSize:               int64(len(data)),
		Entries:                entries,
		SigningBlock:           signing,
		CentralDirectory:       data[cdOffset:cdEnd],
		EOCD:                   data[eocdOffset:],
		CentralDirectoryOffset: int64(cdOffset),
		EOCDRecordOffset:       int64(eocdOffset),
		data:                   data,
		localEnd:               localEnd,
		signingBlockStart:      signingStart,
	}
	return archive, nil
}

func findEOCD(data []byte) (int, error) {
	if len(data) < endOfCentralDirSize {
		return 0, fmt.Errorf("%w: data is shorter than an EOCD record", ErrEOCD)
	}
	minOffset := len(data) - endOfCentralDirSize - maxEOCDCommentSize
	if minOffset < 0 {
		minOffset = 0
	}
	var matches []int
	var sawSignature bool
	for i := len(data) - endOfCentralDirSize; i >= minOffset; i-- {
		if binary.LittleEndian.Uint32(data[i:i+4]) != endOfCentralDirSignature {
			continue
		}
		sawSignature = true
		commentLen := int(binary.LittleEndian.Uint16(data[i+20 : i+22]))
		if i+endOfCentralDirSize+commentLen == len(data) {
			matches = append(matches, i)
		}
	}
	switch len(matches) {
	case 0:
		if sawSignature {
			return 0, fmt.Errorf("%w: EOCD comment length does not reach the end of the buffer", ErrEOCD)
		}
		return 0, fmt.Errorf("%w: EOCD signature not found", ErrEOCD)
	case 1:
		return matches[0], nil
	default:
		return 0, fmt.Errorf("%w: multiple EOCD candidates match the buffer end", ErrEOCD)
	}
}

func parseCentralDirectory(data []byte, baseOffset, count int) ([]centralEntry, error) {
	if count < 0 || count > len(data)/centralDirHeaderSize {
		return nil, fmt.Errorf("%w: impossible central directory entry count %d", ErrMalformedZIP, count)
	}
	entries := make([]centralEntry, 0, count)
	offset := 0
	for i := 0; i < count; i++ {
		if len(data)-offset < centralDirHeaderSize {
			return nil, fmt.Errorf("%w: truncated central directory header", ErrMalformedZIP)
		}
		header := data[offset : offset+centralDirHeaderSize]
		if binary.LittleEndian.Uint32(header[:4]) != centralDirHeaderSignature {
			return nil, fmt.Errorf("%w: bad central directory signature at offset %d", ErrMalformedZIP, baseOffset+offset)
		}

		flags := binary.LittleEndian.Uint16(header[8:10])
		method := binary.LittleEndian.Uint16(header[10:12])
		crc := binary.LittleEndian.Uint32(header[16:20])
		compressed := uint64(binary.LittleEndian.Uint32(header[20:24]))
		uncompressed := uint64(binary.LittleEndian.Uint32(header[24:28]))
		nameLen := int(binary.LittleEndian.Uint16(header[28:30]))
		extraLen := int(binary.LittleEndian.Uint16(header[30:32]))
		commentLen := int(binary.LittleEndian.Uint16(header[32:34]))
		diskStart := binary.LittleEndian.Uint16(header[34:36])
		localOffset := uint64(binary.LittleEndian.Uint32(header[42:46]))

		if compressed == maxUint32 || uncompressed == maxUint32 || localOffset == maxUint32 || diskStart == maxUint16 {
			return nil, fmt.Errorf("%w: ZIP64 sentinel in central directory entry", ErrZIP64)
		}
		if diskStart != 0 {
			return nil, fmt.Errorf("%w: multi-disk entry is not supported", ErrMalformedZIP)
		}

		recordLen := centralDirHeaderSize + nameLen + extraLen + commentLen
		if recordLen < centralDirHeaderSize || recordLen > len(data)-offset {
			return nil, fmt.Errorf("%w: truncated central directory entry", ErrMalformedZIP)
		}
		nameStart := offset + centralDirHeaderSize
		extraStart := nameStart + nameLen
		name := data[nameStart:extraStart]
		if len(name) == 0 {
			return nil, fmt.Errorf("%w: empty entry name", ErrMalformedZIP)
		}
		if err := rejectZIP64Extra(data[extraStart : extraStart+extraLen]); err != nil {
			return nil, err
		}
		entries = append(entries, centralEntry{
			name:             append([]byte(nil), name...),
			crc32:            crc,
			compressedSize:   compressed,
			uncompressedSize: uncompressed,
			method:           method,
			flags:            flags,
			localOffset:      int64(localOffset),
			centralOffset:    int64(baseOffset + offset),
		})
		offset += recordLen
	}
	if offset != len(data) {
		return nil, fmt.Errorf("%w: central directory has %d trailing bytes", ErrMalformedZIP, len(data)-offset)
	}
	return entries, nil
}

func parseLocalEntries(data []byte, cdOffset int) ([]localEntry, int, error) {
	entries := make([]localEntry, 0)
	offset := 0
	for offset < cdOffset {
		if cdOffset-offset < 4 || binary.LittleEndian.Uint32(data[offset:offset+4]) != localFileHeaderSignature {
			break
		}
		if cdOffset-offset < localFileHeaderSize {
			return nil, 0, fmt.Errorf("%w: truncated local file header at offset %d", ErrMalformedZIP, offset)
		}
		header := data[offset : offset+localFileHeaderSize]
		flags := binary.LittleEndian.Uint16(header[6:8])
		method := binary.LittleEndian.Uint16(header[8:10])
		crc := binary.LittleEndian.Uint32(header[14:18])
		compressed := uint64(binary.LittleEndian.Uint32(header[18:22]))
		uncompressed := uint64(binary.LittleEndian.Uint32(header[22:26]))
		nameLen := int(binary.LittleEndian.Uint16(header[26:28]))
		extraLen := int(binary.LittleEndian.Uint16(header[28:30]))

		if compressed == maxUint32 || uncompressed == maxUint32 {
			return nil, 0, fmt.Errorf("%w: ZIP64 sentinel in local header", ErrZIP64)
		}
		headerLen := localFileHeaderSize + nameLen + extraLen
		if headerLen < localFileHeaderSize || headerLen > cdOffset-offset {
			return nil, 0, fmt.Errorf("%w: truncated local header fields at offset %d", ErrMalformedZIP, offset)
		}
		nameStart := offset + localFileHeaderSize
		extraStart := nameStart + nameLen
		name := data[nameStart:extraStart]
		if len(name) == 0 {
			return nil, 0, fmt.Errorf("%w: empty local entry name", ErrMalformedZIP)
		}
		if err := rejectZIP64Extra(data[extraStart : extraStart+extraLen]); err != nil {
			return nil, 0, err
		}
		dataStart := offset + headerLen
		if compressed > uint64(cdOffset-dataStart) {
			return nil, 0, fmt.Errorf("%w: local file data exceeds the local header area", ErrMalformedZIP)
		}
		dataEnd := dataStart + int(compressed)
		entries = append(entries, localEntry{Entry: Entry{
			Name:                   string(name),
			LocalHeaderOffset:      int64(offset),
			DataOffset:             int64(dataStart),
			DataEnd:                int64(dataEnd),
			CompressedSize:         compressed,
			UncompressedSize:       uncompressed,
			CRC32:                  crc,
			CompressionMethod:      method,
			Flags:                  flags,
			CentralDirectoryOffset: -1,
		}})
		offset = dataEnd
	}
	return entries, offset, nil
}

func matchEntries(local []localEntry, central []centralEntry) ([]Entry, error) {
	localByName := make(map[string]Entry, len(local))
	for _, entry := range local {
		if _, exists := localByName[entry.Name]; exists {
			return nil, fmt.Errorf("%w: %q", ErrDuplicateEntry, entry.Name)
		}
		localByName[entry.Name] = entry.Entry
	}
	centralByName := make(map[string]centralEntry, len(central))
	for _, entry := range central {
		name := string(entry.name)
		if _, exists := centralByName[name]; exists {
			return nil, fmt.Errorf("%w: %q", ErrDuplicateEntry, name)
		}
		centralByName[name] = entry
	}
	if len(localByName) != len(centralByName) {
		return nil, fmt.Errorf("%w: entry sets differ", ErrEntryMismatch)
	}

	matched := make([]Entry, 0, len(local))
	for name, localEntry := range localByName {
		centralEntry, ok := centralByName[name]
		if !ok {
			return nil, fmt.Errorf("%w: local entry %q has no central directory record", ErrEntryMismatch, name)
		}
		if localEntry.LocalHeaderOffset != centralEntry.localOffset {
			return nil, fmt.Errorf("%w: %q local offset is %d in LFH and %d in CD",
				ErrEntryMismatch, name, localEntry.LocalHeaderOffset, centralEntry.localOffset)
		}
		if localEntry.CRC32 != centralEntry.crc32 {
			return nil, fmt.Errorf("%w: %q CRC32 differs", ErrEntryMismatch, name)
		}
		if localEntry.CompressedSize != centralEntry.compressedSize {
			return nil, fmt.Errorf("%w: %q compressed size differs", ErrEntryMismatch, name)
		}
		if localEntry.UncompressedSize != centralEntry.uncompressedSize {
			return nil, fmt.Errorf("%w: %q uncompressed size differs", ErrEntryMismatch, name)
		}
		if localEntry.CompressionMethod != centralEntry.method {
			return nil, fmt.Errorf("%w: %q compression method differs", ErrEntryMismatch, name)
		}
		if localEntry.Flags != centralEntry.flags {
			return nil, fmt.Errorf("%w: %q flags differ", ErrEntryMismatch, name)
		}
		localEntry.CentralDirectoryOffset = centralEntry.centralOffset
		matched = append(matched, localEntry)
	}
	sort.Slice(matched, func(i, j int) bool {
		return matched[i].LocalHeaderOffset < matched[j].LocalHeaderOffset
	})
	return matched, nil
}

func rejectOverlaps(entries []Entry) error {
	for i := 1; i < len(entries); i++ {
		if entries[i].LocalHeaderOffset < entries[i-1].DataEnd {
			return fmt.Errorf("%w: %q overlaps %q", ErrOverlappingEntry, entries[i].Name, entries[i-1].Name)
		}
	}
	return nil
}

func rejectZIP64Extra(extra []byte) error {
	for offset := 0; offset < len(extra); {
		if len(extra)-offset < 4 {
			return fmt.Errorf("%w: truncated extra field", ErrMalformedZIP)
		}
		id := binary.LittleEndian.Uint16(extra[offset : offset+2])
		size := int(binary.LittleEndian.Uint16(extra[offset+2 : offset+4]))
		offset += 4
		if size > len(extra)-offset {
			return fmt.Errorf("%w: truncated extra field value", ErrMalformedZIP)
		}
		if id == zip64ExtraFieldID {
			return fmt.Errorf("%w: ZIP64 extra field found", ErrZIP64)
		}
		offset += size
	}
	return nil
}

// Section1 returns the bytes preceding the APK Signing Block. For an unsigned
// APK this is the local-entry area up to the central directory.
func (a *Archive) Section1() []byte {
	if a == nil || a.signingBlockStart < 0 || a.signingBlockStart > len(a.data) {
		return nil
	}
	return a.data[:a.signingBlockStart]
}

// SigningBlockBytes returns the complete APK Signing Block including its
// leading and trailing size fields and magic.
func (a *Archive) SigningBlockBytes() []byte {
	if a == nil || a.SigningBlock == nil || a.data == nil {
		return nil
	}
	start := int(a.SigningBlock.Start)
	end := int(a.CentralDirectoryOffset)
	if start < 0 || end < start || end > len(a.data) {
		return nil
	}
	return a.data[start:end]
}

// NormalizedEOCD returns a copy of the EOCD with the central directory
// offset replaced by the APK Signing Block offset, as required by the APK
// signature scheme content digest.
func (a *Archive) NormalizedEOCD() []byte {
	if a == nil || len(a.EOCD) < endOfCentralDirSize {
		return nil
	}
	eocd := append([]byte(nil), a.EOCD...)
	binary.LittleEndian.PutUint32(eocd[16:20], uint32(a.signingBlockStart))
	return eocd
}

// CoveredBytes returns section 1, the complete central directory, and the
// normalized EOCD as one digest input.
func (a *Archive) CoveredBytes() []byte {
	if a == nil {
		return nil
	}
	section1 := a.Section1()
	eocd := a.NormalizedEOCD()
	result := make([]byte, 0, len(section1)+len(a.CentralDirectory)+len(eocd))
	result = append(result, section1...)
	result = append(result, a.CentralDirectory...)
	result = append(result, eocd...)
	return result
}

// EqualCoverage reports whether two archives have byte-identical signature
// coverage. It is intended for tests and audit tooling.
func (a *Archive) EqualCoverage(other *Archive) bool {
	if a == nil || other == nil {
		return a == other
	}
	return bytes.Equal(a.CoveredBytes(), other.CoveredBytes())
}
