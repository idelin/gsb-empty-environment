package sintcol

import (
	"encoding/binary"
	"hash/crc32"
)

func (d *frameDecoder) consumeIndex() (bool, error) {
	if len(d.buf) > d.cfg.maxFooterBytes {
		return false, ErrCorrupt
	}
	if len(d.buf) < 4+uvarintLen(1)+len(indexMagic) {
		return false, nil
	}
	rest := d.buf[1:]
	sum := binary.LittleEndian.Uint32(rest[:4])
	rest = rest[4:]
	declaredSize, sizeLen, err := strictUvarint(rest)
	if err == ErrCorrupt {
		return false, ErrCorrupt
	}
	if err != nil {
		return false, nil
	}
	entriesEnd := sizeLen + int(declaredSize) - 1
	totalLen := 1 + 4 + entriesEnd + len(indexMagic)
	if declaredSize < uint64(sizeLen) {
		return false, ErrCorrupt
	}
	if uint64(len(d.buf)) < uint64(totalLen) {
		return false, nil
	}
	if string(d.buf[totalLen-len(indexMagic):totalLen]) != indexMagic {
		return false, ErrCorrupt
	}
	entryBytes := rest[:entriesEnd-sizeLen]
	if crc32.ChecksumIEEE(rest[:entriesEnd]) != sum {
		return false, ErrCorrupt
	}

	pos := 0
	var entries []indexEntry
	for pos < len(entryBytes) {
		if len(entries) >= d.cfg.maxIndexEntries {
			return false, ErrCorrupt
		}
		first, n1, err1 := strictUvarint(entryBytes[pos:])
		offsetRaw, n2, err2 := strictUvarint(entryBytes[pos+n1:])
		count, n3, err3 := strictUvarint(entryBytes[pos+n1+n2:])
		if pos+n1+n2+n3 >= len(entryBytes) || err1 != nil || err2 != nil || err3 != nil {
			return false, ErrCorrupt
		}
		flags := entryBytes[pos+n1+n2+n3]
		enc, chained, err := parseEntryFlags(flags)
		if err != nil || count == 0 {
			return false, ErrCorrupt
		}
		next, overflow := saturatingAdd(first, count)
		if overflow || next > uint64(d.cfg.maxTotalElements) {
			return false, ErrCorrupt
		}
		var offset int64
		if offsetRaw > uint64(^uint64(0)>>1) {
			return false, ErrCorrupt
		}
		offset = int64(offsetRaw)
		entries = append(entries, indexEntry{
			firstIndex: first,
			offset:     offset,
			count:      count,
			enc:        enc,
			chained:    chained,
		})
		pos += n1 + n2 + n3 + 1
	}
	if pos != len(entryBytes) {
		return false, ErrCorrupt
	}
	if len(entries) != d.ordinal {
		return false, ErrCorrupt
	}
	for i, entry := range entries {
		seen := d.entries[i]
		if entry != seen {
			return false, ErrCorrupt
		}
	}
	d.phase = phaseDone
	d.buf = d.buf[:0]
	return true, nil
}
