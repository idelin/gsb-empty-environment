package safeunpack

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"hash/crc32"
	"io"
	"math"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	zipLocalSignature      = 0x04034b50
	zipCentralSignature    = 0x02014b50
	zipEOCDSignature       = 0x06054b50
	zip64EOCDSignature     = 0x06064b50
	zip64LocatorSignature  = 0x07064b50
	zipDataDescriptorSig   = 0x08074b50
	zipDigitalSignatureSig = 0x05054b50
	zip64ExtraID           = 0x0001
	zipUnicodePathExtraID  = 0x7075
)

type zipEOCD struct {
	pos      int
	entries  uint64
	cdSize   uint64
	cdOffset uint64
}

type zipRecord struct {
	nameBytes []byte
	name      string
	flags     uint16
	method    uint16
	crc       uint32
	compSize  uint64
	uncomp    uint64
	localOff  uint64
	madeBy    uint16
	extAttrs  uint32
	zip64     bool

	dataStart uint64
	dataEnd   uint64
	recordEnd uint64
}

type zipFile struct {
	data []byte
}

func scanZIP(data []byte, lim Limits) (*Result, error) {
	z := &zipFile{data: data}
	eocd, err := z.findEOCD()
	if err != nil {
		return nil, err
	}
	if eocd.entries > uint64(math.MaxInt) {
		return nil, wrapError(ErrLimitExceeded, "", 0, "ZIP entry count does not fit in memory")
	}
	if lim.MaxEntries >= 0 && eocd.entries > uint64(lim.MaxEntries) {
		return nil, wrapError(ErrLimitExceeded, "", 0,
			"ZIP entry count %d exceeds limit %d", eocd.entries, lim.MaxEntries)
	}
	if eocd.cdOffset > uint64(len(data)) || eocd.cdSize > uint64(len(data)) ||
		eocd.cdOffset+eocd.cdSize < eocd.cdOffset ||
		eocd.cdOffset+eocd.cdSize > uint64(len(data)) {
		return nil, wrapError(ErrZIPConsistency, "", 0, "central directory is outside the archive")
	}
	cdEnd := eocd.cdOffset + eocd.cdSize
	if cdEnd > uint64(eocd.pos) {
		return nil, wrapError(ErrZIPConsistency, "", 0, "central directory overlaps end-of-directory data")
	}

	records := make([]zipRecord, 0, int(eocd.entries))
	cursor := eocd.cdOffset
	for i := uint64(0); i < eocd.entries; i++ {
		rec, next, err := z.parseCentral(cursor, cdEnd, lim)
		if err != nil {
			return nil, err
		}
		records = append(records, rec)
		cursor = next
	}
	if cursor != cdEnd {
		// The optional central-directory digital signature record is allowed.
		if cursor+6 <= cdEnd && binary.LittleEndian.Uint32(data[cursor:cursor+4]) == zipDigitalSignatureSig {
			size := uint64(binary.LittleEndian.Uint16(data[cursor+4 : cursor+6]))
			if cursor+6+size == cdEnd {
				cursor = cdEnd
			}
		}
	}
	if cursor != cdEnd {
		return nil, wrapError(ErrZIPConsistency, "", int64(cursor), "central directory size does not match its entries")
	}

	for i := range records {
		if err := z.parseLocal(&records[i], eocd.cdOffset, lim); err != nil {
			return nil, err
		}
	}
	if err := z.checkOverlap(records); err != nil {
		return nil, err
	}

	t := newTree()
	var compressedTotal uint64
	var maxRatio float64
	for _, rec := range records {
		kind, err := zipEntryType(rec)
		if err != nil {
			return nil, err
		}
		path, parts, trailingSlash, err := normalizeEntryPath(rec.name, lim.MaxPathBytes)
		if err != nil {
			return nil, decorate(err, rec.name, int64(rec.localOff))
		}
		if kind != TypeDirectory && trailingSlash {
			return nil, wrapError(ErrUnsupported, path, int64(rec.localOff),
				"non-directory ZIP entry has a trailing slash")
		}
		if kind == TypeDirectory {
			if rec.compSize != 0 || rec.uncomp != 0 {
				return nil, wrapError(ErrZIPConsistency, path, int64(rec.localOff),
					"directory entry carries file data")
			}
			if err := t.addDirectory(path, parts, rec.mode(), int64(rec.localOff)); err != nil {
				return nil, decorate(err, path, int64(rec.localOff))
			}
			setLastCompressed(t, rec.compSize)
			continue
		}
		if lim.MaxFileBytes >= 0 && rec.uncomp > uint64(lim.MaxFileBytes) {
			return nil, wrapError(ErrLimitExceeded, path, int64(rec.localOff),
				"member size %d exceeds per-file limit %d", rec.uncomp, lim.MaxFileBytes)
		}
		if err := t.preflightNonDirectory(path, parts, int64(rec.localOff)); err != nil {
			return nil, decorate(err, path, int64(rec.localOff))
		}
		if err := checkZIPRatio(rec.uncomp, rec.compSize, lim.MaxCompressionRatio); err != nil {
			return nil, decorate(err, path, int64(rec.localOff))
		}
		if err := t.charge(int64(rec.uncomp), lim.MaxFileBytes, lim.MaxTotalBytes); err != nil {
			return nil, decorate(err, path, int64(rec.localOff))
		}
		payload, err := z.readPayload(rec)
		if err != nil {
			return nil, decorate(err, path, int64(rec.localOff))
		}
		switch kind {
		case TypeRegular:
			if err := t.addRegular(path, parts, rec.mode(), payload, int64(rec.localOff)); err != nil {
				return nil, decorate(err, path, int64(rec.localOff))
			}
		case TypeSymlink:
			target := string(payload)
			resolved, err := t.resolveSymlinkTarget(path, parts[:len(parts)-1], target, lim.MaxPathBytes)
			if err != nil {
				return nil, decorate(err, path, int64(rec.localOff))
			}
			if err := t.addSymlink(path, parts, rec.mode(), target, resolved, int64(rec.uncomp), int64(rec.localOff)); err != nil {
				return nil, decorate(err, path, int64(rec.localOff))
			}
		default:
			return nil, wrapError(ErrUnsupported, path, int64(rec.localOff), "unsupported ZIP entry type")
		}
		setLastCompressed(t, rec.compSize)
	}

	compressedTotal = 0
	for _, rec := range records {
		compressedTotal += rec.compSize
		if rec.uncomp == 0 || rec.compSize == 0 {
			continue
		}
		ratio := float64(rec.uncomp) / float64(rec.compSize)
		if ratio > maxRatio {
			maxRatio = ratio
		}
	}
	if compressedTotal > 0 {
		global := float64(t.totalBytes) / float64(compressedTotal)
		if global > maxRatio {
			maxRatio = global
		}
	}
	return t.result(FormatZIP, int64(compressedTotal), maxRatio), nil
}

func setLastCompressed(t *treeState, compressed uint64) {
	if len(t.entries) == 0 {
		return
	}
	t.entries[len(t.entries)-1].CompressedSize = int64(compressed)
}

func (z *zipFile) findEOCD() (zipEOCD, error) {
	data := z.data
	if len(data) < 22 {
		return zipEOCD{}, wrapError(ErrMalformed, "", 0, "ZIP is shorter than the end-of-directory record")
	}
	lower := len(data) - 22 - 65535
	if lower < 0 {
		lower = 0
	}
	found := -1
	for i := len(data) - 22; i >= lower; i-- {
		if binary.LittleEndian.Uint32(data[i:i+4]) != zipEOCDSignature {
			continue
		}
		comment := int(binary.LittleEndian.Uint16(data[i+22 : i+24]))
		if i+22+comment == len(data) {
			found = i
			break
		}
	}
	if found < 0 {
		return zipEOCD{}, wrapError(ErrMalformed, "", 0, "ZIP end-of-directory record not found")
	}

	e := zipEOCD{pos: found}
	disk := binary.LittleEndian.Uint16(data[found+4 : found+6])
	cdDisk := binary.LittleEndian.Uint16(data[found+6 : found+8])
	onDisk := binary.LittleEndian.Uint16(data[found+8 : found+10])
	total := binary.LittleEndian.Uint16(data[found+10 : found+12])
	cdSize32 := binary.LittleEndian.Uint32(data[found+12 : found+16])
	cdOff32 := binary.LittleEndian.Uint32(data[found+16 : found+20])
	if disk != 0 || cdDisk != 0 {
		return zipEOCD{}, wrapError(ErrUnsupported, "", int64(found), "multi-disk ZIP archives are not supported")
	}
	if onDisk != total {
		return zipEOCD{}, wrapError(ErrZIPConsistency, "", int64(found),
			"entries-on-disk does not match total entry count")
	}

	e.entries = uint64(total)
	e.cdSize = uint64(cdSize32)
	e.cdOffset = uint64(cdOff32)
	if total != 0xffff && cdSize32 != math.MaxUint32 && cdOff32 != math.MaxUint32 {
		return e, nil
	}

	if found < 20 {
		return zipEOCD{}, wrapError(ErrZIPConsistency, "", int64(found), "ZIP64 locator is missing")
	}
	loc := found - 20
	if binary.LittleEndian.Uint32(data[loc:loc+4]) != zip64LocatorSignature {
		return zipEOCD{}, wrapError(ErrZIPConsistency, "", int64(loc), "ZIP64 locator is missing")
	}
	if binary.LittleEndian.Uint32(data[loc+4:loc+8]) != 0 ||
		binary.LittleEndian.Uint32(data[loc+16:loc+20]) != 1 {
		return zipEOCD{}, wrapError(ErrUnsupported, "", int64(loc), "multi-disk ZIP64 archives are not supported")
	}
	off := binary.LittleEndian.Uint64(data[loc+8 : loc+16])
	if off > uint64(len(data)) || off+56 > uint64(loc) {
		return zipEOCD{}, wrapError(ErrZIPConsistency, "", int64(loc), "ZIP64 end-of-directory is outside the archive")
	}
	if binary.LittleEndian.Uint32(data[off:off+4]) != zip64EOCDSignature {
		return zipEOCD{}, wrapError(ErrZIPConsistency, "", int64(off), "ZIP64 end-of-directory signature mismatch")
	}
	size := binary.LittleEndian.Uint64(data[off+4 : off+12])
	if size < 44 || off+12+size > uint64(loc) {
		return zipEOCD{}, wrapError(ErrZIPConsistency, "", int64(off), "invalid ZIP64 end-of-directory size")
	}
	if binary.LittleEndian.Uint32(data[off+16:off+20]) != 0 ||
		binary.LittleEndian.Uint32(data[off+20:off+24]) != 0 {
		return zipEOCD{}, wrapError(ErrUnsupported, "", int64(off), "multi-disk ZIP64 archives are not supported")
	}
	e.entries = binary.LittleEndian.Uint64(data[off+32 : off+40])
	e.cdSize = binary.LittleEndian.Uint64(data[off+40 : off+48])
	e.cdOffset = binary.LittleEndian.Uint64(data[off+48 : off+56])
	return e, nil
}

func (z *zipFile) parseCentral(cursor, cdEnd uint64, lim Limits) (zipRecord, uint64, error) {
	data := z.data
	if cursor+46 > cdEnd || cursor+46 > uint64(len(data)) {
		return zipRecord{}, 0, wrapError(ErrZIPConsistency, "", int64(cursor), "truncated central directory entry")
	}
	if binary.LittleEndian.Uint32(data[cursor:cursor+4]) != zipCentralSignature {
		return zipRecord{}, 0, wrapError(ErrZIPConsistency, "", int64(cursor), "invalid central directory signature")
	}
	var rec zipRecord
	rec.madeBy = binary.LittleEndian.Uint16(data[cursor+4 : cursor+6])
	rec.flags = binary.LittleEndian.Uint16(data[cursor+8 : cursor+10])
	rec.method = binary.LittleEndian.Uint16(data[cursor+10 : cursor+12])
	rec.crc = binary.LittleEndian.Uint32(data[cursor+16 : cursor+20])
	comp32 := binary.LittleEndian.Uint32(data[cursor+20 : cursor+24])
	uncomp32 := binary.LittleEndian.Uint32(data[cursor+24 : cursor+28])
	nameLen := uint64(binary.LittleEndian.Uint16(data[cursor+28 : cursor+30]))
	extraLen := uint64(binary.LittleEndian.Uint16(data[cursor+30 : cursor+32]))
	commentLen := uint64(binary.LittleEndian.Uint16(data[cursor+32 : cursor+34]))
	diskStart := binary.LittleEndian.Uint16(data[cursor+34 : cursor+36])
	rec.extAttrs = binary.LittleEndian.Uint32(data[cursor+38 : cursor+42])
	local32 := binary.LittleEndian.Uint32(data[cursor+42 : cursor+46])
	if diskStart != 0 && diskStart != 0xffff {
		return zipRecord{}, 0, wrapError(ErrUnsupported, "", int64(cursor), "multi-disk ZIP entry")
	}
	next := cursor + 46 + nameLen + extraLen + commentLen
	if next < cursor || next > cdEnd || next > uint64(len(data)) {
		return zipRecord{}, 0, wrapError(ErrZIPConsistency, "", int64(cursor), "central directory lengths exceed the directory")
	}
	rec.nameBytes = append([]byte(nil), data[cursor+46:cursor+46+nameLen]...)
	extraStart := cursor + 46 + nameLen
	extra := data[extraStart : extraStart+extraLen]
	rec.name = string(rec.nameBytes)
	if len(rec.nameBytes) == 0 {
		return zipRecord{}, 0, wrapError(ErrZIPConsistency, "", int64(cursor), "empty ZIP entry name")
	}
	fields, err := parseZIPExtra(extra)
	if err != nil {
		return zipRecord{}, 0, wrapError(ErrMalformed, "", int64(extraStart), "invalid ZIP extra field: %v", err)
	}
	rec.compSize = uint64(comp32)
	rec.uncomp = uint64(uncomp32)
	rec.localOff = uint64(local32)
	zip64Data, hasZip64 := fields[zip64ExtraID]
	if comp32 == math.MaxUint32 || uncomp32 == math.MaxUint32 || local32 == math.MaxUint32 || diskStart == 0xffff {
		if !hasZip64 {
			return zipRecord{}, 0, wrapError(ErrZIPConsistency, "", int64(cursor), "ZIP64 entry is missing its extra field")
		}
		var err error
		rec.uncomp, rec.compSize, rec.localOff, err = decodeZIP64(zip64Data, uncomp32, comp32, local32, diskStart)
		if err != nil {
			return zipRecord{}, 0, wrapError(ErrZIPConsistency, "", int64(cursor), "%v", err)
		}
	}
	rec.zip64 = comp32 == math.MaxUint32 || uncomp32 == math.MaxUint32 ||
		rec.compSize > math.MaxUint32 || rec.uncomp > math.MaxUint32
	if err := validateZIPUnicodePath(fields, rec.nameBytes, lim.MaxPathBytes); err != nil {
		return zipRecord{}, 0, decorate(err, rec.name, int64(cursor))
	}
	if rec.flags&1 != 0 || rec.flags&0x40 != 0 {
		return zipRecord{}, 0, wrapError(ErrUnsupported, rec.name, int64(cursor), "encrypted ZIP entries are not supported")
	}
	if rec.method != 0 && rec.method != 8 {
		return zipRecord{}, 0, wrapError(ErrUnsupported, rec.name, int64(cursor),
			"unsupported ZIP compression method %d", rec.method)
	}
	if lim.MaxFileBytes >= 0 && rec.uncomp > uint64(lim.MaxFileBytes) {
		return zipRecord{}, 0, wrapError(ErrLimitExceeded, rec.name, int64(cursor),
			"member size %d exceeds per-file limit %d", rec.uncomp, lim.MaxFileBytes)
	}
	if err := checkZIPRatio(rec.uncomp, rec.compSize, lim.MaxCompressionRatio); err != nil {
		return zipRecord{}, 0, decorate(err, rec.name, int64(cursor))
	}
	return rec, next, nil
}

func parseZIPExtra(extra []byte) (map[uint16][]byte, error) {
	fields := make(map[uint16][]byte)
	for pos := 0; pos < len(extra); {
		if len(extra)-pos < 4 {
			return nil, wrapError(ErrMalformed, "", -1, "truncated extra field header")
		}
		id := binary.LittleEndian.Uint16(extra[pos : pos+2])
		size := int(binary.LittleEndian.Uint16(extra[pos+2 : pos+4]))
		pos += 4
		if size > len(extra)-pos {
			return nil, wrapError(ErrMalformed, "", -1, "extra field extends beyond its container")
		}
		if _, exists := fields[id]; exists {
			return nil, wrapError(ErrMalformed, "", -1, "duplicate extra field id 0x%04x", id)
		}
		fields[id] = extra[pos : pos+size]
		pos += size
	}
	return fields, nil
}

func decodeZIP64(data []byte, uncomp32, comp32, local32 uint32, diskStart uint16) (uint64, uint64, uint64, error) {
	outUncomp := uint64(uncomp32)
	outComp := uint64(comp32)
	outOff := uint64(local32)
	var outDisk uint32
	pos := 0
	read64 := func() (uint64, error) {
		if len(data)-pos < 8 {
			return 0, wrapError(ErrZIPConsistency, "", -1, "truncated ZIP64 extra field")
		}
		v := binary.LittleEndian.Uint64(data[pos : pos+8])
		pos += 8
		return v, nil
	}
	if uncomp32 == math.MaxUint32 {
		v, err := read64()
		if err != nil {
			return 0, 0, 0, err
		}
		outUncomp = v
	}
	if comp32 == math.MaxUint32 {
		v, err := read64()
		if err != nil {
			return 0, 0, 0, err
		}
		outComp = v
	}
	if local32 == math.MaxUint32 {
		v, err := read64()
		if err != nil {
			return 0, 0, 0, err
		}
		outOff = v
	}
	if diskStart == 0xffff {
		if len(data)-pos < 4 {
			return 0, 0, 0, wrapError(ErrZIPConsistency, "", -1, "truncated ZIP64 disk field")
		}
		outDisk = binary.LittleEndian.Uint32(data[pos : pos+4])
		pos += 4
		if outDisk != 0 {
			return 0, 0, 0, wrapError(ErrUnsupported, "", -1, "multi-disk ZIP64 entry")
		}
	}
	return outUncomp, outComp, outOff, nil
}

func validateZIPUnicodePath(fields map[uint16][]byte, legacy []byte, maxPath int) error {
	data, ok := fields[zipUnicodePathExtraID]
	if !ok {
		return nil
	}
	if len(data) < 5 || data[0] != 1 {
		return wrapError(ErrMalformed, "", -1, "invalid Unicode path extra field")
	}
	if crc32.ChecksumIEEE(legacy) != binary.LittleEndian.Uint32(data[1:5]) {
		return wrapError(ErrZIPConsistency, "", -1, "Unicode path extra CRC does not match the entry name")
	}
	unicodeName := string(data[5:])
	if unicodeName == "" || !utf8.ValidString(unicodeName) {
		return wrapError(ErrMalformed, "", -1, "Unicode path extra field has an invalid name")
	}
	uPath, _, _, err := normalizeEntryPath(unicodeName, maxPath)
	if err != nil {
		return decorate(err, unicodeName, -1)
	}
	lPath, _, _, err := normalizeEntryPath(string(legacy), maxPath)
	if err != nil {
		return decorate(err, string(legacy), -1)
	}
	if lPath != uPath {
		return wrapError(ErrZIPConsistency, unicodeName, -1,
			"Unicode path extra field conflicts with the legacy name %q", lPath)
	}
	return nil
}

func (z *zipFile) parseLocal(rec *zipRecord, cdOffset uint64, lim Limits) error {
	data := z.data
	off := rec.localOff
	if off >= cdOffset || off+30 > uint64(len(data)) {
		return wrapError(ErrZIPConsistency, rec.name, int64(off), "local header is outside local-data area")
	}
	if binary.LittleEndian.Uint32(data[off:off+4]) != zipLocalSignature {
		return wrapError(ErrZIPConsistency, rec.name, int64(off), "local header signature mismatch")
	}
	flags := binary.LittleEndian.Uint16(data[off+6 : off+8])
	method := binary.LittleEndian.Uint16(data[off+8 : off+10])
	modTime := binary.LittleEndian.Uint16(data[off+10 : off+12])
	modDate := binary.LittleEndian.Uint16(data[off+12 : off+14])
	crc := binary.LittleEndian.Uint32(data[off+14 : off+18])
	comp32 := binary.LittleEndian.Uint32(data[off+18 : off+22])
	uncomp32 := binary.LittleEndian.Uint32(data[off+22 : off+26])
	nameLen := uint64(binary.LittleEndian.Uint16(data[off+26 : off+28]))
	extraLen := uint64(binary.LittleEndian.Uint16(data[off+28 : off+30]))
	if flags != rec.flags || method != rec.method {
		return wrapError(ErrZIPConsistency, rec.name, int64(off),
			"local flags or compression method differ from central directory")
	}
	// Central and local timestamps are both part of the ZIP metadata contract.
	_ = modTime
	_ = modDate
	headerEnd := off + 30 + nameLen + extraLen
	if headerEnd < off || headerEnd > uint64(len(data)) || headerEnd > cdOffset {
		return wrapError(ErrZIPConsistency, rec.name, int64(off), "local header extends outside local-data area")
	}
	localName := data[off+30 : off+30+nameLen]
	if !bytes.Equal(localName, rec.nameBytes) {
		return wrapError(ErrZIPConsistency, rec.name, int64(off), "local filename differs from central directory")
	}
	extra := data[off+30+nameLen : headerEnd]
	fields, err := parseZIPExtra(extra)
	if err != nil {
		return wrapError(ErrMalformed, rec.name, int64(off), "invalid local extra field: %v", err)
	}
	if rec.flags&8 == 0 {
		localComp := uint64(comp32)
		localUncomp := uint64(uncomp32)
		if comp32 == math.MaxUint32 || uncomp32 == math.MaxUint32 {
			zip64Data, ok := fields[zip64ExtraID]
			if !ok {
				return wrapError(ErrZIPConsistency, rec.name, int64(off),
					"local ZIP64 sizes are missing")
			}
			localUncomp, localComp, _, err = decodeZIP64(zip64Data, uncomp32, comp32, 0, 0)
			if err != nil {
				return wrapError(ErrZIPConsistency, rec.name, int64(off), "%v", err)
			}
		}
		if crc != rec.crc || localComp != rec.compSize || localUncomp != rec.uncomp {
			return wrapError(ErrZIPConsistency, rec.name, int64(off),
				"local CRC or sizes differ from central directory")
		}
	} else {
		if crc != 0 && crc != rec.crc {
			return wrapError(ErrZIPConsistency, rec.name, int64(off), "local CRC differs from central directory")
		}
		if comp32 != 0 && comp32 != math.MaxUint32 && uint64(comp32) != rec.compSize {
			return wrapError(ErrZIPConsistency, rec.name, int64(off), "local compressed size differs from central directory")
		}
		if uncomp32 != 0 && uncomp32 != math.MaxUint32 && uint64(uncomp32) != rec.uncomp {
			return wrapError(ErrZIPConsistency, rec.name, int64(off), "local uncompressed size differs from central directory")
		}
	}

	rec.dataStart = headerEnd
	if rec.compSize > uint64(len(data)) || headerEnd > uint64(len(data))-rec.compSize {
		return wrapError(ErrZIPConsistency, rec.name, int64(off), "compressed data extends outside the archive")
	}
	rec.dataEnd = headerEnd + rec.compSize
	if rec.dataEnd > cdOffset {
		return wrapError(ErrZIPConsistency, rec.name, int64(off), "compressed data overlaps the central directory")
	}
	rec.recordEnd = rec.dataEnd

	if rec.flags&8 != 0 {
		if err := z.parseDataDescriptor(rec); err != nil {
			return err
		}
	}
	if rec.recordEnd > cdOffset {
		return wrapError(ErrZIPConsistency, rec.name, int64(off),
			"local record overlaps the central directory")
	}
	return nil
}

func (z *zipFile) parseDataDescriptor(rec *zipRecord) error {
	data := z.data
	fieldWidth := uint64(4)
	if rec.zip64 || rec.compSize > math.MaxUint32 || rec.uncomp > math.MaxUint32 {
		fieldWidth = 8
	}
	needed := 4 + fieldWidth*2
	// The data descriptor signature is optional and may itself collide with a
	// valid CRC32 value. Try the signed form first, then the unsigned form.
	for _, signed := range []bool{true, false} {
		pos := rec.dataEnd
		if signed {
			if pos+4 > uint64(len(data)) ||
				binary.LittleEndian.Uint32(data[pos:pos+4]) != zipDataDescriptorSig {
				continue
			}
			pos += 4
		}
		if pos+needed > uint64(len(data)) {
			continue
		}
		crc := binary.LittleEndian.Uint32(data[pos : pos+4])
		var comp, uncomp uint64
		if fieldWidth == 8 {
			comp = binary.LittleEndian.Uint64(data[pos+4 : pos+12])
			uncomp = binary.LittleEndian.Uint64(data[pos+12 : pos+20])
		} else {
			comp = uint64(binary.LittleEndian.Uint32(data[pos+4 : pos+8]))
			uncomp = uint64(binary.LittleEndian.Uint32(data[pos+8 : pos+12]))
		}
		if crc == rec.crc && comp == rec.compSize && uncomp == rec.uncomp {
			rec.recordEnd = pos + needed
			return nil
		}
	}
	return wrapError(ErrZIPConsistency, rec.name, int64(rec.dataEnd),
		"missing or mismatched ZIP data descriptor")
}

func (z *zipFile) checkOverlap(records []zipRecord) error {
	if len(records) < 2 {
		return nil
	}
	type span struct {
		start uint64
		end   uint64
		name  string
	}
	spans := make([]span, 0, len(records))
	for _, rec := range records {
		spans = append(spans, span{start: rec.localOff, end: rec.recordEnd, name: rec.name})
	}
	sort.Slice(spans, func(i, j int) bool {
		if spans[i].start == spans[j].start {
			return spans[i].end < spans[j].end
		}
		return spans[i].start < spans[j].start
	})
	var prevEnd uint64
	var prevName string
	for i, s := range spans {
		if i > 0 && s.start < prevEnd {
			return wrapError(ErrZIPConsistency, s.name, int64(s.start),
				"local record overlaps %q", prevName)
		}
		if s.end < s.start {
			return wrapError(ErrZIPConsistency, s.name, int64(s.start), "invalid local record range")
		}
		prevEnd = s.end
		prevName = s.name
	}
	return nil
}

func (z *zipFile) readPayload(rec zipRecord) ([]byte, error) {
	size := int64(rec.uncomp)
	if size < 0 || uint64(size) != rec.uncomp {
		return nil, wrapError(ErrLimitExceeded, rec.name, int64(rec.localOff), "decoded size does not fit in memory")
	}
	if size > int64(math.MaxInt) {
		return nil, wrapError(ErrLimitExceeded, rec.name, int64(rec.localOff), "decoded size does not fit in memory")
	}
	section := z.data[rec.dataStart:rec.dataEnd]
	var reader io.Reader
	if rec.method == 0 {
		if rec.compSize != rec.uncomp {
			return nil, wrapError(ErrZIPConsistency, rec.name, int64(rec.localOff),
				"stored entry has different compressed and uncompressed sizes")
		}
		reader = bytes.NewReader(section)
	} else {
		reader = flate.NewReader(bytes.NewReader(section))
		defer reader.(io.Closer).Close()
	}

	out := make([]byte, 0, int(size))
	hasher := crc32.NewIEEE()
	var total uint64
	buf := make([]byte, readChunk)
	for {
		n, err := reader.Read(buf)
		if n > 0 {
			total += uint64(n)
			if total > rec.uncomp {
				return nil, wrapError(ErrZIPConsistency, rec.name, int64(rec.localOff),
					"decoded data exceeds the declared uncompressed size")
			}
			out = append(out, buf[:n]...)
			_, _ = hasher.Write(buf[:n])
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, wrapError(ErrMalformed, rec.name, int64(rec.localOff),
				"cannot decompress ZIP entry: %v", err)
		}
	}
	if total != rec.uncomp {
		return nil, wrapError(ErrZIPConsistency, rec.name, int64(rec.localOff),
			"decoded size %d differs from central directory size %d", total, rec.uncomp)
	}
	if hasher.Sum32() != rec.crc {
		return nil, wrapError(ErrZIPConsistency, rec.name, int64(rec.localOff),
			"CRC32 mismatch: got 0x%08x, expected 0x%08x", hasher.Sum32(), rec.crc)
	}
	return out, nil
}

func checkZIPRatio(uncomp, comp uint64, max float64) error {
	if max < 0 || uncomp == 0 {
		return nil
	}
	if comp == 0 {
		return wrapError(ErrCompressionBomb, "", -1, "non-empty entry has zero compressed size")
	}
	ratio := float64(uncomp) / float64(comp)
	if ratio > max {
		return wrapError(ErrCompressionBomb, "", -1,
			"ZIP compression ratio %.1f exceeds limit %.1f", ratio, max)
	}
	return nil
}

func zipEntryType(rec zipRecord) (EntryType, error) {
	unixMode := uint32(rec.extAttrs >> 16)
	host := rec.madeBy >> 8
	if strings.HasSuffix(rec.name, "/") {
		if unixMode&0xf000 == 0xa000 {
			return 0, wrapError(ErrUnsupported, rec.name, int64(rec.localOff),
				"symbolic link entry has a trailing slash")
		}
		return TypeDirectory, nil
	}
	if host == 3 || host == 19 || unixMode != 0 {
		switch unixMode & 0xf000 {
		case 0xa000:
			return TypeSymlink, nil
		case 0x4000:
			return TypeDirectory, nil
		case 0, 0x8000:
			return TypeRegular, nil
		default:
			return 0, wrapError(ErrUnsupported, rec.name, int64(rec.localOff),
				"special ZIP file mode 0x%08x is not supported", unixMode)
		}
	}
	if rec.extAttrs&0x10 != 0 {
		return TypeDirectory, nil
	}
	return TypeRegular, nil
}

func (r zipRecord) mode() uint32 {
	if mode := uint32(r.extAttrs >> 16); mode != 0 {
		return mode
	}
	return 0o644
}
