package netsec

import (
	"bytes"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"fmt"
)

var errNoState = errors.New("netsec: empty state slot")

type volumeState struct {
	Sequence      uint64
	VolumeID      uint64
	KeyGeneration uint64
	ReservedEpoch uint64
	CommitEpoch   uint64
	RootRevision  uint64
	Root          physicalRef
	RootLevel     uint16
	DataStart     uint64
	SectorCount   uint64
}

func encodeState(metaKey []byte, state volumeState) []byte {
	record := make([]byte, PhysicalSectorSize)
	copy(record[0:8], stateMagic[:])
	binary.BigEndian.PutUint16(record[8:10], formatVersion)
	binary.BigEndian.PutUint16(record[10:12], PhysicalSectorSize)
	binary.BigEndian.PutUint64(record[12:20], state.Sequence)
	binary.BigEndian.PutUint64(record[20:28], state.VolumeID)
	binary.BigEndian.PutUint64(record[28:36], state.KeyGeneration)
	binary.BigEndian.PutUint64(record[36:44], state.ReservedEpoch)
	binary.BigEndian.PutUint64(record[44:52], state.CommitEpoch)
	binary.BigEndian.PutUint64(record[52:60], state.RootRevision)
	binary.BigEndian.PutUint64(record[60:68], state.Root.Sector)
	binary.BigEndian.PutUint64(record[68:76], state.Root.Generation)
	binary.BigEndian.PutUint16(record[76:78], state.RootLevel)
	binary.BigEndian.PutUint64(record[80:88], state.DataStart)
	binary.BigEndian.PutUint64(record[88:96], state.SectorCount)
	authenticateMetadata(metaKey, record)
	return record
}

func decodeState(metaKey, record []byte) (volumeState, error) {
	var state volumeState
	if len(record) != PhysicalSectorSize {
		return state, fmt.Errorf("%w: state record has %d bytes", ErrInvalidSize, len(record))
	}
	if subtle.ConstantTimeCompare(record[0:8], stateMagic[:]) != 1 {
		if bytes.Equal(record, make([]byte, PhysicalSectorSize)) {
			return state, errNoState
		}
		return state, errNoState
	}
	if !verifyMetadata(metaKey, record) {
		return state, ErrAuthentication
	}
	if binary.BigEndian.Uint16(record[8:10]) != formatVersion {
		return state, fmt.Errorf("%w: unsupported state version", ErrCorrupt)
	}
	if binary.BigEndian.Uint16(record[10:12]) != PhysicalSectorSize {
		return state, fmt.Errorf("%w: state sector size mismatch", ErrCorrupt)
	}
	state.Sequence = binary.BigEndian.Uint64(record[12:20])
	state.VolumeID = binary.BigEndian.Uint64(record[20:28])
	state.KeyGeneration = binary.BigEndian.Uint64(record[28:36])
	state.ReservedEpoch = binary.BigEndian.Uint64(record[36:44])
	state.CommitEpoch = binary.BigEndian.Uint64(record[44:52])
	state.RootRevision = binary.BigEndian.Uint64(record[52:60])
	state.Root.Sector = binary.BigEndian.Uint64(record[60:68])
	state.Root.Generation = binary.BigEndian.Uint64(record[68:76])
	state.RootLevel = binary.BigEndian.Uint16(record[76:78])
	state.DataStart = binary.BigEndian.Uint64(record[80:88])
	state.SectorCount = binary.BigEndian.Uint64(record[88:96])
	if state.ReservedEpoch < state.CommitEpoch {
		return volumeState{}, fmt.Errorf("%w: commit epoch exceeds reservation", ErrCorrupt)
	}
	if state.Root.Sector == 0 && (state.Root.Generation != 0 || state.RootLevel != 0) {
		return volumeState{}, fmt.Errorf("%w: empty root has non-zero fields", ErrCorrupt)
	}
	if state.Root.Sector != 0 && state.Root.Generation == 0 {
		return volumeState{}, fmt.Errorf("%w: root generation is zero", ErrCorrupt)
	}
	return state, nil
}

func sameState(a, b volumeState) bool {
	return a.Sequence == b.Sequence &&
		a.VolumeID == b.VolumeID &&
		a.KeyGeneration == b.KeyGeneration &&
		a.ReservedEpoch == b.ReservedEpoch &&
		a.CommitEpoch == b.CommitEpoch &&
		a.RootRevision == b.RootRevision &&
		a.Root == b.Root &&
		a.RootLevel == b.RootLevel &&
		a.DataStart == b.DataStart &&
		a.SectorCount == b.SectorCount
}
