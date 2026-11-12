package netsec

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"sync"
)

// ErrCorruptState means neither journal slot contained a valid committed
// record. The card fails closed instead of guessing a retry budget.
var ErrCorruptState = errors.New("netsec: corrupt persistent PIN state")

const (
	recordSize    = 80
	recordVersion = uint16(1)
	crcOffset     = 76
)

var recordMagic = [4]byte{'N', 'P', 'I', 'N'}

// persistedState is the complete transactionally committed card state. Every
// operation that changes retry counters, the PIN, or both is committed as one
// A/B journal record.
type persistedState struct {
	Generation uint64
	PINRetries uint8
	PUKRetries uint8
	Salt       [16]byte
	PINHash    [32]byte
}

func (s persistedState) encode() []byte {
	buf := make([]byte, recordSize)
	copy(buf[0:4], recordMagic[:])
	binary.LittleEndian.PutUint16(buf[4:6], recordVersion)
	binary.LittleEndian.PutUint64(buf[8:16], s.Generation)
	buf[16] = s.PINRetries
	buf[17] = s.PUKRetries
	copy(buf[24:40], s.Salt[:])
	copy(buf[40:72], s.PINHash[:])
	binary.LittleEndian.PutUint32(buf[crcOffset:crcOffset+4], crc32.ChecksumIEEE(buf[:crcOffset]))
	return buf
}

func decodeRecord(data []byte) (persistedState, bool) {
	if len(data) != recordSize {
		return persistedState{}, false
	}
	if !bytes.Equal(data[0:4], recordMagic[:]) {
		return persistedState{}, false
	}
	if binary.LittleEndian.Uint16(data[4:6]) != recordVersion {
		return persistedState{}, false
	}
	want := binary.LittleEndian.Uint32(data[crcOffset : crcOffset+4])
	if crc32.ChecksumIEEE(data[:crcOffset]) != want {
		return persistedState{}, false
	}

	var state persistedState
	state.Generation = binary.LittleEndian.Uint64(data[8:16])
	state.PINRetries = data[16]
	state.PUKRetries = data[17]
	copy(state.Salt[:], data[24:40])
	copy(state.PINHash[:], data[40:72])
	if state.Generation == 0 {
		return persistedState{}, false
	}
	return state, true
}

// journal stores the newest valid record in one of two slots. A commit writes
// only the slot that does not currently hold the newest record, so a torn or
// failed write can never destroy the committed state. The generation number
// makes the newest valid slot unambiguous after a reboot.
type journal struct {
	nvm SlotNVM

	mu         sync.Mutex
	generation uint64
	lastSlot   int
}

func newJournal(nvm SlotNVM) *journal {
	return &journal{nvm: nvm, lastSlot: -1}
}

// load returns the newest valid committed record. A completely blank NVM is
// reported as not present. If bytes exist but neither slot is valid, load
// fails closed.
func (j *journal) load() (persistedState, bool, error) {
	j.mu.Lock()
	defer j.mu.Unlock()

	var (
		best      persistedState
		bestSlot  = -1
		dataSlots int
	)
	for slot := 0; slot < slotCount; slot++ {
		data, present, err := j.nvm.ReadSlot(slot)
		if err != nil {
			return persistedState{}, false, err
		}
		if !present {
			continue
		}
		dataSlots++
		state, ok := decodeRecord(data)
		if !ok {
			continue
		}
		if bestSlot < 0 || state.Generation > best.Generation {
			best = state
			bestSlot = slot
		}
	}
	if bestSlot < 0 {
		// A single non-empty invalid slot can only be an interrupted factory
		// initialisation: every later commit alternates slots, so a committed
		// card always retains a valid record while the other slot is written.
		// Treat that state as blank and let NewPINHandler initialise again.
		if dataSlots >= slotCount {
			return persistedState{}, false, ErrCorruptState
		}
		j.generation = 0
		j.lastSlot = -1
		return persistedState{}, false, nil
	}

	j.generation = best.Generation
	j.lastSlot = bestSlot
	return best, true, nil
}

// commit writes a new generation to the inactive slot. It returns an error
// whenever the write may not be durable; callers must stop immediately and
// must not perform a comparison or continue with the returned state.
func (j *journal) commit(state persistedState) (persistedState, error) {
	j.mu.Lock()
	defer j.mu.Unlock()

	state.Generation = j.generation + 1
	target := 0
	if j.lastSlot >= 0 {
		target = 1 - j.lastSlot
	}
	if err := j.nvm.WriteSlot(target, state.encode()); err != nil {
		return persistedState{}, err
	}
	j.generation = state.Generation
	j.lastSlot = target
	return state, nil
}
