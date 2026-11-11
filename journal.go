package bootjournal

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
)

// ErrPowerLoss is returned when an injected fault trips mid-mutation. The
// flash contents at that point are exactly what real hardware would retain:
// every operation before the fault is durable, the faulting one is either
// fully applied or fully lost at word/erase granularity, and nothing later
// happened.
var ErrPowerLoss = errors.New("bootjournal: simulated power loss")

// On-flash journal record. Records are appended until a page fills; a
// snapshot record then replaces the history (see journal.compact).
const RecordSize = 64

const (
	// Record layout (64 bytes):
	//   [0]     record type
	//   [1:58]  payload (57 bytes); fields use the f* offsets below
	//   [58:62] CRC32 over bytes [0:58]
	//   [62]    torn marker:   FF while erased, cleared to 00 in phase one
	//   [63]    commit marker: FF until the record commits, then cleared 1->0
	// A record is replayable only when both markers are clear. Both marker
	// writes are single 1 -> 0 transitions and lie outside the CRC region, so
	// programming them never invalidates the stored checksum.
	tornByte     = 0
	commitByte   = 0
	tornOffset   = 62
	commitOffset = 63
	crcOffset    = 58

	// Payload field offsets, relative to the start of the record. All record
	// types share these positions; unused fields stay erased (0xFF).
	fieldSlot         = 1
	fieldUnconfirmed  = 2
	fieldPendingSlot  = 3
	fieldMinVersion   = 4
	fieldVersion      = 8
	fieldPayloadBytes = fieldSlot // caller payload is copied starting here
)

// Offsets inside the caller-supplied payload buffer. The buffer is copied at
// fieldPayloadBytes, so these map onto fieldSlot / fieldVersion in the record.
const (
	payloadSlot = fieldSlot - fieldPayloadBytes
	// Version sits at fieldVersion in the record.
	payloadVersion = fieldVersion - fieldPayloadBytes
	maxPayload     = crcOffset - fieldPayloadBytes
)

// Record types. Every state transition on the device is exactly one record.
const (
	recSnapshot = 1 + iota // folded state replacing all earlier records
	recPrepared            // an update image was fully written and verified
	recActivate            // the active boot slot changed
	recBoot                // an unconfirmed image attempted to boot
	recConfirm             // the running image was confirmed good
	recRollback            // two failed boots forced a slot rollback
)

// logState is the folded journal state.
type logState struct {
	active         int    // current boot slot, -1 before provisioning
	unconfirmed    int    // consecutive unconfirmed boots of the active slot
	pendingSlot    int    // slot staged by a prepared update, -1 when none
	pendingVersion uint32 // version carried by the pending update
	minVersion     uint32 // anti-rollback floor, advanced only on confirm
}

func newLogState() logState {
	return logState{active: -1, pendingSlot: -1}
}

// journal is an append-only commit log stored in a fixed flash window.
type journal struct {
	flash   *FaultyFlash
	base    int
	records int
	state   logState
}

// openJournal replays the log window and returns the folded state.
func openJournal(flash *FaultyFlash, base, pages int) (*journal, error) {
	if pages < 2 || base%PageSize != 0 || pages*PageSize > flash.Size()-base {
		return nil, errors.New("bootjournal: journal window misaligned or too small")
	}
	j := &journal{
		flash:   flash,
		base:    base,
		records: pages * (PageSize / RecordSize),
		state:   newLogState(),
	}
	if err := j.replay(); err != nil {
		return nil, err
	}
	return j, nil
}

func (j *journal) recordAddress(index int) int {
	return j.base + index*RecordSize
}

// replay walks records in order. A pristine slot ends the log; a torn slot
// (power loss during its write) also ends it: nothing after it can be valid.
func (j *journal) replay() error {
	buf := make([]byte, RecordSize)
	for i := 0; i < j.records; i++ {
		if err := j.flash.Read(j.recordAddress(i), buf); err != nil {
			return err
		}
		if isAllOnes(buf) {
			return nil
		}
		if buf[tornOffset] != 0 || buf[commitOffset] != 0 {
			return nil // torn write: ignore it and everything beyond
		}
		want := binary.BigEndian.Uint32(buf[crcOffset : crcOffset+4])
		if crc32.ChecksumIEEE(buf[:58]) != want {
			return nil // corrupted record terminates the log
		}
		j.apply(buf[0], buf)
	}
	return nil
}

// apply folds one committed record into the in-memory state. The argument is
// the full record buffer so field offsets are absolute.
func (j *journal) apply(recType byte, rec []byte) {
	switch recType {
	case recSnapshot:
		pending := -1
		if rec[fieldPendingSlot] <= 1 {
			pending = int(rec[fieldPendingSlot])
		}
		j.state = logState{
			active:         int(rec[fieldSlot]),
			unconfirmed:    int(rec[fieldUnconfirmed]),
			pendingSlot:    pending,
			pendingVersion: binary.BigEndian.Uint32(rec[fieldVersion : fieldVersion+4]),
			minVersion:     binary.BigEndian.Uint32(rec[fieldMinVersion : fieldMinVersion+4]),
		}
	case recPrepared:
		j.state.pendingSlot = int(rec[fieldSlot])
		j.state.pendingVersion = binary.BigEndian.Uint32(rec[fieldVersion : fieldVersion+4])
	case recActivate:
		j.state.active = int(rec[fieldSlot])
		j.state.unconfirmed = 0
		j.state.pendingSlot = -1
	case recBoot:
		if int(rec[fieldSlot]) == j.state.active {
			j.state.unconfirmed++
		}
	case recConfirm:
		if int(rec[fieldSlot]) == j.state.active {
			j.state.unconfirmed = 0
			confirmedVersion := binary.BigEndian.Uint32(rec[fieldVersion : fieldVersion+4])
			if confirmedVersion > j.state.minVersion {
				j.state.minVersion = confirmedVersion
			}
		}
	case recRollback:
		j.state.active = int(rec[fieldSlot])
		j.state.unconfirmed = 0
		j.state.pendingSlot = -1
	}
}

// append commits one transition. The record lands in two phases: payload and
// CRC first (with a torn status byte), then the status byte is programmed to
// commit it. A power loss in phase one leaves an ignorable torn record; a
// loss in phase two either way yields the same recoverable outcome.
func (j *journal) append(recType byte, payload []byte) error {
	index, err := j.freeSlot()
	if err != nil {
		return err
	}
	rec, err := j.buildRecord(recType, payload)
	if err != nil {
		return err
	}
	addr := j.recordAddress(index)
	if err := j.flash.Program(addr, rec); err != nil {
		return err
	}
	if err := j.flash.Program(addr+tornOffset, []byte{tornByte}); err != nil {
		return err
	}
	if err := j.flash.Program(addr+commitOffset, []byte{commitByte}); err != nil {
		return err
	}
	j.apply(recType, rec)
	return nil
}

// buildRecord encodes one transition into an erased 64-byte record buffer.
func (j *journal) buildRecord(recType byte, payload []byte) ([]byte, error) {
	rec := make([]byte, RecordSize)
	for i := range rec {
		rec[i] = 0xFF
	}
	rec[0] = recType
	if len(payload) > maxPayload {
		return nil, errors.New("bootjournal: journal payload too large")
	}
	copy(rec[fieldPayloadBytes:], payload)
	binary.BigEndian.PutUint32(rec[crcOffset:crcOffset+4], crc32.ChecksumIEEE(rec[:crcOffset]))
	return rec, nil
}

// snapshotPayload encodes logState into a snapshot-record payload using the
// absolute snapshot field layout.
func snapshotPayload(state logState) []byte {
	payload := make([]byte, maxPayload)
	for i := range payload {
		payload[i] = 0xFF
	}
	if state.active >= 0 {
		payload[fieldSlot-fieldPayloadBytes] = byte(state.active)
	}
	payload[fieldUnconfirmed-fieldPayloadBytes] = byte(state.unconfirmed)
	if state.pendingSlot >= 0 {
		payload[fieldPendingSlot-fieldPayloadBytes] = byte(state.pendingSlot)
	}
	binary.BigEndian.PutUint32(payload[fieldMinVersion-fieldPayloadBytes:fieldMinVersion-fieldPayloadBytes+4], state.minVersion)
	binary.BigEndian.PutUint32(payload[fieldVersion-fieldPayloadBytes:fieldVersion-fieldPayloadBytes+4], state.pendingVersion)
	return payload
}

// freeSlot returns the first pristine record slot, compacting first when the
// log is full or blocked by a torn record.
func (j *journal) freeSlot() (int, error) {
	buf := make([]byte, RecordSize)
	tail := 0 // index one past the last committed record
	for i := 0; i < j.records; i++ {
		if err := j.flash.Read(j.recordAddress(i), buf); err != nil {
			return -1, err
		}
		if isAllOnes(buf) {
			break // erased tail (or a hole followed by nothing)
		}
		if buf[tornOffset] != 0 || buf[commitOffset] != 0 {
			// A torn record blocks the tail: reclaim via compaction.
			if err := j.compact(); err != nil {
				return -1, err
			}
			return 1, nil // snapshot now occupies index 0
		}
		tail = i + 1
	}
	if tail < j.records {
		return tail, nil
	}
	if err := j.compact(); err != nil {
		return -1, err
	}
	return 1, nil // snapshot now occupies index 0
}

// compact folds the whole log into one snapshot record at index 0. The first
// page is erased and the snapshot is committed before any other page is
// erased, so a power loss at any point still leaves either the old history or
// the new snapshot sufficient to reconstruct the state.
func (j *journal) compact() error {
	perPage := PageSize / RecordSize
	if err := j.flash.ErasePage(j.recordAddress(0)); err != nil {
		return err
	}
	payload := snapshotPayload(j.state)

	rec, err := j.buildRecord(recSnapshot, payload)
	if err != nil {
		return err
	}
	addr := j.recordAddress(0)
	if err := j.flash.Program(addr, rec); err != nil {
		return err
	}
	if err := j.flash.Program(addr+tornOffset, []byte{tornByte}); err != nil {
		return err
	}
	if err := j.flash.Program(addr+commitOffset, []byte{commitByte}); err != nil {
		return err
	}
	// Snapshot is durable at index 0; the older history is now redundant.
	for page := 1; page*perPage < j.records; page++ {
		index := page * perPage
		if err := j.flash.ErasePage(j.recordAddress(index)); err != nil {
			return err
		}
	}
	j.apply(recSnapshot, rec)
	return nil
}

func isAllOnes(p []byte) bool {
	for _, b := range p {
		if b != 0xFF {
			return false
		}
	}
	return true
}
