package ftl

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"sort"
	"sync"
)

const (
	pageMetaSize = 48
	pageMagic    = 0x47505446 // "FTPG" little endian
	pageVersion  = 1
	pageTail     = 0x4E454750 // "PGEN"

	journalMagic   = 0x4E4A5446 // "FTJN"
	journalVersion = 1
	journalTrim    = 1

	checkpointMagic   = 0x4B435446 // "FTCK"
	checkpointVersion = 1
)

var castagnoli = crc32.MakeTable(crc32.Castagnoli)

// PageKind identifies the role of a physical-page OOB record.
type PageKind uint8

const (
	// PageData is a normal user or GC-migrated data page.
	PageData PageKind = 1
)

// PageMeta is the decoded Out-of-Band metadata of a physical page.
type PageMeta struct {
	Kind   PageKind
	LBA    int
	Seq    uint64
	Length int
	Block  int
	Page   int
}

func pageCRC(oob []byte, data []byte) uint32 {
	crc := crc32.New(castagnoli)
	_, _ = crc.Write(oob[:36])
	_, _ = crc.Write(data)
	return crc.Sum32()
}

func encodePageMeta(meta PageMeta, data []byte) []byte {
	oob := make([]byte, pageMetaSize)
	binary.LittleEndian.PutUint32(oob[0:4], pageMagic)
	oob[4] = pageVersion
	oob[5] = byte(meta.Kind)
	binary.LittleEndian.PutUint16(oob[6:8], 0)
	binary.LittleEndian.PutUint64(oob[8:16], uint64(meta.LBA))
	binary.LittleEndian.PutUint64(oob[16:24], meta.Seq)
	binary.LittleEndian.PutUint32(oob[24:28], uint32(meta.Length))
	binary.LittleEndian.PutUint32(oob[28:32], uint32(meta.Block))
	binary.LittleEndian.PutUint32(oob[32:36], uint32(meta.Page))
	binary.LittleEndian.PutUint32(oob[36:40], pageCRC(oob, data))
	binary.LittleEndian.PutUint32(oob[40:44], pageTail)
	return oob
}

// decodePageMeta validates the magic, trailer, geometry, and checksum of an
// OOB record.  A false result means the page must be treated as torn.
func decodePageMeta(oob, data []byte, block, page, pageSize int) (PageMeta, bool) {
	if len(oob) < pageMetaSize || len(data) != pageSize {
		return PageMeta{}, false
	}
	if binary.LittleEndian.Uint32(oob[0:4]) != pageMagic ||
		oob[4] != pageVersion ||
		binary.LittleEndian.Uint32(oob[40:44]) != pageTail {
		return PageMeta{}, false
	}
	kind := PageKind(oob[5])
	if kind != PageData {
		return PageMeta{}, false
	}
	lba := int(int64(binary.LittleEndian.Uint64(oob[8:16])))
	if lba < 0 {
		return PageMeta{}, false
	}
	length := int(binary.LittleEndian.Uint32(oob[24:28]))
	storedBlock := int(binary.LittleEndian.Uint32(oob[28:32]))
	storedPage := int(binary.LittleEndian.Uint32(oob[32:36]))
	if length < 0 || length > pageSize || storedBlock != block || storedPage != page {
		return PageMeta{}, false
	}
	if binary.LittleEndian.Uint32(oob[36:40]) != pageCRC(oob, data) {
		return PageMeta{}, false
	}
	return PageMeta{
		Kind:   kind,
		LBA:    lba,
		Seq:    binary.LittleEndian.Uint64(oob[16:24]),
		Length: length,
		Block:  block,
		Page:   page,
	}, true
}

// PageMeta decodes and validates one physical page's OOB record.
func (f *Flash) PageMeta(block, page int) (PageMeta, bool) {
	data, oob, programmed, err := f.ReadRaw(block, page)
	if err != nil || !programmed {
		return PageMeta{}, false
	}
	return decodePageMeta(oob, data, block, page, f.pageSize)
}

// TrimRecord is one durable TRIM/Deallocate journal entry.  Start is inclusive
// and Count is the number of logical pages released.
type TrimRecord struct {
	Seq   uint64
	Start int
	Count int
}

func (r TrimRecord) encode() []byte {
	buf := make([]byte, 40)
	binary.LittleEndian.PutUint32(buf[0:4], journalMagic)
	buf[4] = journalVersion
	buf[5] = journalTrim
	binary.LittleEndian.PutUint64(buf[8:16], r.Seq)
	binary.LittleEndian.PutUint64(buf[16:24], uint64(int64(r.Start)))
	binary.LittleEndian.PutUint64(buf[24:32], uint64(r.Count))
	binary.LittleEndian.PutUint32(buf[32:36], crc32.Checksum(buf[:32], castagnoli))
	binary.LittleEndian.PutUint32(buf[36:40], journalMagic)
	return buf
}

func decodeTrimRecord(buf []byte) (TrimRecord, bool) {
	if len(buf) != 40 ||
		binary.LittleEndian.Uint32(buf[0:4]) != journalMagic ||
		buf[4] != journalVersion ||
		buf[5] != journalTrim ||
		binary.LittleEndian.Uint32(buf[36:40]) != journalMagic {
		return TrimRecord{}, false
	}
	if binary.LittleEndian.Uint32(buf[32:36]) != crc32.Checksum(buf[:32], castagnoli) {
		return TrimRecord{}, false
	}
	start := int(int64(binary.LittleEndian.Uint64(buf[16:24])))
	count := int(binary.LittleEndian.Uint64(buf[24:32]))
	if start < 0 || count < 0 {
		return TrimRecord{}, false
	}
	return TrimRecord{
		Seq:   binary.LittleEndian.Uint64(buf[8:16]),
		Start: start,
		Count: count,
	}, true
}

type snapEntry struct {
	LBA   int
	Block int
	Page  int
	Seq   uint64
}

type snapTomb struct {
	LBA int
	Seq uint64
}

type checkpointSnapshot struct {
	Seq     uint64
	Entries []snapEntry
	Tombs   []snapTomb
}

func (s checkpointSnapshot) encodePayload() []byte {
	entries := append([]snapEntry(nil), s.Entries...)
	tombs := append([]snapTomb(nil), s.Tombs...)
	sort.Slice(entries, func(i, j int) bool { return entries[i].LBA < entries[j].LBA })
	sort.Slice(tombs, func(i, j int) bool { return tombs[i].LBA < tombs[j].LBA })

	buf := new(bytes.Buffer)
	var word [8]byte
	_ = binary.Write(buf, binary.LittleEndian, uint32(len(entries)))
	for _, entry := range entries {
		binary.LittleEndian.PutUint64(word[:], uint64(int64(entry.LBA)))
		buf.Write(word[:])
		_ = binary.Write(buf, binary.LittleEndian, uint32(entry.Block))
		_ = binary.Write(buf, binary.LittleEndian, uint32(entry.Page))
		binary.LittleEndian.PutUint64(word[:], entry.Seq)
		buf.Write(word[:])
	}
	_ = binary.Write(buf, binary.LittleEndian, uint32(len(tombs)))
	for _, tomb := range tombs {
		binary.LittleEndian.PutUint64(word[:], uint64(int64(tomb.LBA)))
		buf.Write(word[:])
		binary.LittleEndian.PutUint64(word[:], tomb.Seq)
		buf.Write(word[:])
	}
	return buf.Bytes()
}

func decodeCheckpointPayload(payload []byte) (checkpointSnapshot, bool) {
	if len(payload) < 8 {
		return checkpointSnapshot{}, false
	}
	offset := 0
	readU32 := func() (uint32, bool) {
		if offset+4 > len(payload) {
			return 0, false
		}
		value := binary.LittleEndian.Uint32(payload[offset : offset+4])
		offset += 4
		return value, true
	}
	readU64 := func() (uint64, bool) {
		if offset+8 > len(payload) {
			return 0, false
		}
		value := binary.LittleEndian.Uint64(payload[offset : offset+8])
		offset += 8
		return value, true
	}

	entryCount, ok := readU32()
	if !ok || entryCount > 1<<24 {
		return checkpointSnapshot{}, false
	}
	snapshot := checkpointSnapshot{}
	snapshot.Entries = make([]snapEntry, 0, entryCount)
	for i := uint32(0); i < entryCount; i++ {
		lba, ok := readU64()
		if !ok {
			return checkpointSnapshot{}, false
		}
		block, ok := readU32()
		if !ok {
			return checkpointSnapshot{}, false
		}
		page, ok := readU32()
		if !ok {
			return checkpointSnapshot{}, false
		}
		seq, ok := readU64()
		if !ok {
			return checkpointSnapshot{}, false
		}
		snapshot.Entries = append(snapshot.Entries, snapEntry{
			LBA: int(int64(lba)), Block: int(block), Page: int(page), Seq: seq,
		})
	}
	tombCount, ok := readU32()
	if !ok || tombCount > 1<<24 {
		return checkpointSnapshot{}, false
	}
	snapshot.Tombs = make([]snapTomb, 0, tombCount)
	for i := uint32(0); i < tombCount; i++ {
		lba, ok := readU64()
		if !ok {
			return checkpointSnapshot{}, false
		}
		seq, ok := readU64()
		if !ok {
			return checkpointSnapshot{}, false
		}
		snapshot.Tombs = append(snapshot.Tombs, snapTomb{LBA: int(int64(lba)), Seq: seq})
	}
	if offset != len(payload) {
		return checkpointSnapshot{}, false
	}
	return snapshot, true
}

func encodeCheckpoint(s checkpointSnapshot) []byte {
	payload := s.encodePayload()
	buf := make([]byte, 24+len(payload))
	binary.LittleEndian.PutUint32(buf[0:4], checkpointMagic)
	buf[4] = checkpointVersion
	binary.LittleEndian.PutUint64(buf[8:16], s.Seq)
	binary.LittleEndian.PutUint32(buf[16:20], uint32(len(payload)))
	binary.LittleEndian.PutUint32(buf[20:24], crc32.Checksum(payload, castagnoli))
	copy(buf[24:], payload)
	return buf
}

func decodeCheckpoint(buf []byte) (checkpointSnapshot, bool) {
	if len(buf) < 24 ||
		binary.LittleEndian.Uint32(buf[0:4]) != checkpointMagic ||
		buf[4] != checkpointVersion {
		return checkpointSnapshot{}, false
	}
	length := int(binary.LittleEndian.Uint32(buf[16:20]))
	if length < 0 || len(buf) != 24+length {
		return checkpointSnapshot{}, false
	}
	payload := buf[24:]
	if binary.LittleEndian.Uint32(buf[20:24]) != crc32.Checksum(payload, castagnoli) {
		return checkpointSnapshot{}, false
	}
	snapshot, ok := decodeCheckpointPayload(payload)
	if !ok {
		return checkpointSnapshot{}, false
	}
	snapshot.Seq = binary.LittleEndian.Uint64(buf[8:16])
	return snapshot, true
}

// MetaArea models the controller's persistent metadata region.  It survives
// controller power loss independently of the data flash.  Checkpoints occupy
// two alternating slots; a slot is usable only if its length, trailer, and CRC
// are all complete.  TRIM records form an append-only journal that is pruned
// only after a complete checkpoint has captured the corresponding tombstones.
type MetaArea struct {
	mu sync.RWMutex

	checkpoints [2][]byte
	journal     [][]byte
	trimBytes   int

	checkpointWrites uint64
	journalAppends   uint64
}

// NewMetaArea returns an empty persistent metadata region.
func NewMetaArea() *MetaArea { return &MetaArea{} }

// Clone returns a deep copy, primarily for deterministic fault injection.
func (m *MetaArea) Clone() *MetaArea {
	m.mu.RLock()
	defer m.mu.RUnlock()
	clone := &MetaArea{
		journal:          make([][]byte, len(m.journal)),
		trimBytes:        m.trimBytes,
		checkpointWrites: m.checkpointWrites,
		journalAppends:   m.journalAppends,
	}
	for i, record := range m.journal {
		clone.journal[i] = append([]byte(nil), record...)
	}
	for i := range m.checkpoints {
		clone.checkpoints[i] = append([]byte(nil), m.checkpoints[i]...)
	}
	return clone
}

// AppendTrim durably appends a TRIM record.
func (m *MetaArea) AppendTrim(record TrimRecord) error {
	if record.Start < 0 || record.Count < 0 {
		return fmt.Errorf("%w: trim start=%d count=%d", ErrOutOfRange, record.Start, record.Count)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	encoded := record.encode()
	m.journal = append(m.journal, encoded)
	m.trimBytes += len(encoded)
	m.journalAppends++
	return nil
}

// TrimJournal returns a copy of all checksum-valid journal records.  A
// half-written or bit-flipped record is silently discarded, exactly like a
// torn physical page.
func (m *MetaArea) TrimJournal() []TrimRecord {
	m.mu.RLock()
	defer m.mu.RUnlock()
	records := make([]TrimRecord, 0, len(m.journal))
	for _, raw := range m.journal {
		if record, ok := decodeTrimRecord(raw); ok {
			records = append(records, record)
		}
	}
	return records
}

// JournalBytes returns the encoded size of the in-memory journal.
func (m *MetaArea) JournalBytes() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.trimBytes
}

// pruneJournalLocked removes records fully covered by a complete checkpoint.
func (m *MetaArea) pruneJournalLocked(seq uint64) {
	kept := m.journal[:0]
	for _, raw := range m.journal {
		record, ok := decodeTrimRecord(raw)
		if ok && record.Seq > seq {
			kept = append(kept, raw)
		}
	}
	m.journal = kept
	m.trimBytes = len(kept) * 40
}

// CorruptJournalRecord flips a byte in one stored TRIM record.  Recovery must
// ignore it rather than trust a half-written journal entry.
func (m *MetaArea) CorruptJournalRecord(index int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if index < 0 || index >= len(m.journal) {
		return fmt.Errorf("%w: journal index=%d", ErrOutOfRange, index)
	}
	m.journal[index][20] ^= 0x01
	return nil
}

// WriteCheckpoint atomically publishes a complete checkpoint into the slot
// that does not contain the current newest checkpoint.  The previous slot is
// left untouched, so a tear during this write cannot destroy the last
// recoverable checkpoint.
func (m *MetaArea) WriteCheckpoint(snapshot checkpointSnapshot) error {
	encoded := encodeCheckpoint(snapshot)
	m.mu.Lock()
	defer m.mu.Unlock()

	latestSlot := -1
	var latestSeq uint64
	minSeq := snapshot.Seq
	for index, slot := range m.checkpoints {
		if slot == nil {
			continue
		}
		decoded, valid := decodeCheckpoint(slot)
		if !valid {
			continue
		}
		if latestSlot < 0 || decoded.Seq > latestSeq {
			latestSlot = index
			latestSeq = decoded.Seq
		}
		if decoded.Seq < minSeq {
			minSeq = decoded.Seq
		}
	}
	if latestSlot >= 0 && latestSeq >= snapshot.Seq {
		return fmt.Errorf("%w: checkpoint seq must increase", ErrInvalidConfig)
	}
	target := 0
	if latestSlot == 0 {
		target = 1
	}
	m.checkpoints[target] = encoded
	m.checkpointWrites++
	// Keep redo records newer than the older of the two valid checkpoints.
	// If the newest slot later proves torn, the older checkpoint can still be
	// combined with those records.  A sole checkpoint is not used to prune the
	// journal at all, so even its corruption leaves a complete redo trail.
	if latestSlot >= 0 && m.checkpoints[1-target] != nil {
		if _, valid := decodeCheckpoint(m.checkpoints[1-target]); valid {
			m.pruneJournalLocked(minSeq)
		}
	}
	return nil
}

func (m *MetaArea) latestCheckpointLocked() (checkpointSnapshot, bool) {
	var best checkpointSnapshot
	found := false
	for _, slot := range m.checkpoints {
		if slot == nil {
			continue
		}
		snapshot, ok := decodeCheckpoint(slot)
		if !ok {
			continue
		}
		if !found || snapshot.Seq > best.Seq {
			best = snapshot
			found = true
		}
	}
	return best, found
}

// latest returns the newest complete checkpoint snapshot.  It is used by
// recovery and takes the metadata lock.
func (m *MetaArea) latest() (checkpointSnapshot, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.latestCheckpointLocked()
}

// LatestCheckpoint returns the newest fully valid checkpoint.  A torn slot is
// ignored.
func (m *MetaArea) LatestCheckpoint() (uint64, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	snapshot, ok := m.latestCheckpointLocked()
	return snapshot.Seq, ok
}

// CheckpointCount returns how many checkpoints have been published.
func (m *MetaArea) CheckpointCount() uint64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.checkpointWrites
}

// CheckpointSlotSeqs returns the sequence stored in each slot, or zero when a
// slot is empty or corrupt.  It is intended for recovery tests.
func (m *MetaArea) CheckpointSlotSeqs() [2]uint64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var seqs [2]uint64
	for index, slot := range m.checkpoints {
		if slot == nil {
			continue
		}
		if snapshot, ok := decodeCheckpoint(slot); ok {
			seqs[index] = snapshot.Seq
		}
	}
	return seqs
}

// CorruptCheckpointSlot flips a byte inside one checkpoint slot.  It is used
// to verify that recovery ignores a torn checkpoint and falls back to the
// previous complete one.
func (m *MetaArea) CorruptCheckpointSlot(index int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if index < 0 || index > 1 || len(m.checkpoints[index]) == 0 {
		return fmt.Errorf("%w: checkpoint slot=%d", ErrOutOfRange, index)
	}
	m.checkpoints[index][len(m.checkpoints[index])/2] ^= 0xFF
	return nil
}
