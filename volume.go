package netsec

import (
	"bytes"
	"crypto/cipher"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"fmt"
	"math"
	"sort"
	"sync"
)

// Options configures a volume. MasterKey is expanded independently for each
// volume, key generation, and purpose. A 32-byte random key is recommended.
//
// InitialWriteEpoch applies only when formatting an empty device.
// MinimumWriteEpoch is checked when opening an existing volume and is useful
// when the host has a trusted epoch anchor outside this device.
type Options struct {
	VolumeID          uint64
	KeyGeneration     uint64
	MasterKey         []byte
	InitialWriteEpoch uint64
	MinimumWriteEpoch uint64
}

// VolumeState is a snapshot of the committed volume metadata.
type VolumeState struct {
	VolumeID      uint64
	KeyGeneration uint64
	WriteEpoch    uint64
	CommitEpoch   uint64
	RootRevision  uint64
	SectorCount   uint64
}

// Mapping exposes one committed logical-to-physical mapping. Physical
// generation must not be altered by the caller.
type Mapping struct {
	Logical            uint64
	Physical           uint64
	PhysicalGeneration uint64
}

// Volume is a serialized, authenticated sector host.
type Volume struct {
	mu sync.Mutex

	device      Device
	sectorCount uint64

	masterKey []byte
	metaKey   []byte
	aeadKey   []byte
	nonceKey  []byte
	aead      cipher.AEAD

	volumeID      uint64
	keyGeneration uint64
	writeEpoch    uint64
	commitEpoch   uint64
	rootRevision  uint64
	root          physicalRef
	rootLevel     uint16

	stateSeq      uint64
	latestState   int
	metadataClean bool
	needsReload   bool
	closed        bool

	used        map[uint64]struct{}
	free        []uint64
	freeSet     map[uint64]struct{}
	pendingFree map[uint64]struct{}
}

type stateAtSlot struct {
	State volumeState
	Slot  int
}

type writeTx struct {
	allocations map[uint64]struct{}
}

func newWriteTx() *writeTx {
	return &writeTx{allocations: make(map[uint64]struct{})}
}

// NewVolume formats an empty device or opens an existing volume.
func NewVolume(device Device, options Options) (*Volume, error) {
	return OpenVolume(device, options)
}

// OpenVolume opens an existing volume, formatting it only when all state slots
// are empty.
func OpenVolume(device Device, options Options) (*Volume, error) {
	if device == nil {
		return nil, fmt.Errorf("%w: nil device", ErrCorrupt)
	}
	if device.SectorSize() != PhysicalSectorSize {
		return nil, fmt.Errorf("%w: device sector size %d", ErrInvalidSize, device.SectorSize())
	}
	sectorCount := device.SectorCount()
	if sectorCount <= DataStartSector {
		return nil, fmt.Errorf("%w: device has %d sectors", ErrNoSpace, sectorCount)
	}
	if len(options.MasterKey) == 0 {
		return nil, ErrKeyRequired
	}

	v := &Volume{
		device:        device,
		sectorCount:   sectorCount,
		masterKey:     append([]byte(nil), options.MasterKey...),
		volumeID:      options.VolumeID,
		latestState:   -1,
		metadataClean: true,
		used:          make(map[uint64]struct{}),
		freeSet:       make(map[uint64]struct{}),
		pendingFree:   make(map[uint64]struct{}),
	}
	v.metaKey = deriveMetaKey(v.masterKey, v.volumeID)

	states, sawNonEmpty, sawCorrupt, err := v.scanStatesLocked()
	if err != nil {
		return nil, err
	}
	if len(states) == 0 {
		if sawNonEmpty || sawCorrupt {
			return nil, ErrAuthentication
		}
		if err := v.formatLocked(options); err != nil {
			return nil, err
		}
		if err := v.rebuildFreeLocked(nil); err != nil {
			return nil, err
		}
		return v, nil
	}

	latest, err := chooseLatestState(states)
	if err != nil {
		return nil, err
	}
	if latest.State.VolumeID != options.VolumeID {
		return nil, ErrAuthentication
	}
	if latest.State.DataStart != DataStartSector || latest.State.SectorCount != sectorCount {
		return nil, fmt.Errorf("%w: device geometry mismatch", ErrCorrupt)
	}
	if options.MinimumWriteEpoch > latest.State.ReservedEpoch {
		return nil, fmt.Errorf("%w: persisted %d < minimum %d",
			ErrWriteEpochRollback, latest.State.ReservedEpoch, options.MinimumWriteEpoch)
	}
	observedEpoch, err := v.maxPhysicalEpochLocked()
	if err != nil {
		return nil, err
	}
	if observedEpoch > latest.State.ReservedEpoch {
		return nil, fmt.Errorf("%w: physical epoch %d exceeds state reservation %d",
			ErrWriteEpochRollback, observedEpoch, latest.State.ReservedEpoch)
	}
	if len(states) > 1 || sawCorrupt {
		v.metadataClean = false
	}
	if err := v.installStateLocked(latest.State, latest.Slot); err != nil {
		return nil, err
	}

	_ = v.invalidateOtherStatesLocked()
	protected := map[uint64]struct{}(nil)
	if !v.metadataClean {
		protected, err = v.collectAllStateReferencesLocked()
		if err != nil {
			return nil, err
		}
	}
	if err := v.rebuildFreeLocked(protected); err != nil {
		return nil, err
	}
	return v, nil
}

// Open is an alias for OpenVolume.
func Open(device Device, options Options) (*Volume, error) {
	return OpenVolume(device, options)
}

func (v *Volume) formatLocked(options Options) error {
	state := volumeState{
		Sequence:      1,
		VolumeID:      options.VolumeID,
		KeyGeneration: options.KeyGeneration,
		ReservedEpoch: options.InitialWriteEpoch,
		CommitEpoch:   options.InitialWriteEpoch,
		RootRevision:  0,
		Root:          physicalRef{},
		RootLevel:     0,
		DataStart:     DataStartSector,
		SectorCount:   v.sectorCount,
	}
	record := encodeState(v.metaKey, state)
	if err := v.device.WriteSector(0, record); err != nil {
		return err
	}
	if err := v.device.Sync(); err != nil {
		return err
	}
	readback := make([]byte, PhysicalSectorSize)
	if err := v.device.ReadSector(0, readback); err != nil {
		return err
	}
	if !equalRecordDigest(record, readback) {
		return ErrReadbackMismatch
	}
	if err := v.installStateLocked(state, 0); err != nil {
		return err
	}
	v.metadataClean = true
	return nil
}

func (v *Volume) scanStatesLocked() ([]stateAtSlot, bool, bool, error) {
	states := make([]stateAtSlot, 0, StateSectorCount)
	sawNonEmpty := false
	sawCorrupt := false
	for slot := 0; slot < StateSectorCount; slot++ {
		record := make([]byte, PhysicalSectorSize)
		if err := v.device.ReadSector(uint64(slot), record); err != nil {
			return nil, false, false, err
		}
		if !isZero(record) {
			sawNonEmpty = true
		}
		state, err := decodeState(v.metaKey, record)
		switch {
		case err == nil:
			states = append(states, stateAtSlot{State: state, Slot: slot})
		case err == errNoState:
			continue
		default:
			sawCorrupt = true
		}
	}
	return states, sawNonEmpty, sawCorrupt, nil
}

func (v *Volume) maxPhysicalEpochLocked() (uint64, error) {
	var maxEpoch uint64
	record := make([]byte, PhysicalSectorSize)
	for sector := uint64(DataStartSector); sector < v.sectorCount; sector++ {
		if err := v.device.ReadSector(sector, record); err != nil {
			return 0, err
		}
		var epoch uint64
		switch {
		case bytes.Equal(record[0:8], dataMagic[:]):
			epoch = binary.BigEndian.Uint64(record[44:52])
		case bytes.Equal(record[0:8], nodeMagic[:]) && verifyMetadata(v.metaKey, record):
			epoch = binary.BigEndian.Uint64(record[44:52])
		}
		if epoch > maxEpoch {
			maxEpoch = epoch
		}
	}
	return maxEpoch, nil
}

func chooseLatestState(states []stateAtSlot) (stateAtSlot, error) {
	if len(states) == 0 {
		return stateAtSlot{}, ErrNotInitialized
	}
	latest := states[0]
	for _, candidate := range states[1:] {
		switch {
		case candidate.State.Sequence > latest.State.Sequence:
			latest = candidate
		case candidate.State.Sequence == latest.State.Sequence:
			if !sameState(candidate.State, latest.State) {
				return stateAtSlot{}, fmt.Errorf("%w: conflicting state sequence %d", ErrCorrupt, candidate.State.Sequence)
			}
		}
	}
	return latest, nil
}

func (v *Volume) installStateLocked(state volumeState, slot int) error {
	if state.VolumeID != v.volumeID {
		return ErrAuthentication
	}
	if state.DataStart != DataStartSector || state.SectorCount != v.sectorCount {
		return fmt.Errorf("%w: state geometry mismatch", ErrCorrupt)
	}
	if slot < 0 {
		return fmt.Errorf("%w: state slot not found", ErrCorrupt)
	}
	aeadKey, nonceKey := deriveDataKeys(v.masterKey, state.VolumeID, state.KeyGeneration)
	aead, err := newAEAD(aeadKey)
	if err != nil {
		return err
	}
	used := make(map[uint64]struct{})
	if state.Root.Sector != 0 {
		node, err := v.loadNodeOnDevice(state.Root)
		if err != nil {
			return err
		}
		if node.Level != state.RootLevel {
			return fmt.Errorf("%w: root level mismatch", ErrCorrupt)
		}
		used, err = v.collectReferenced(state.Root, state.RootLevel)
		if err != nil {
			return err
		}
	} else if state.RootLevel != 0 {
		return fmt.Errorf("%w: empty root has level", ErrCorrupt)
	}

	wipeBytes(v.aeadKey)
	wipeBytes(v.nonceKey)
	v.aeadKey = aeadKey
	v.nonceKey = nonceKey
	v.aead = aead
	v.keyGeneration = state.KeyGeneration
	v.writeEpoch = state.ReservedEpoch
	v.commitEpoch = state.CommitEpoch
	v.rootRevision = state.RootRevision
	v.root = state.Root
	v.rootLevel = state.RootLevel
	v.stateSeq = state.Sequence
	v.latestState = slot
	v.used = used
	v.needsReload = false
	return nil
}

// loadNodeOnDevice is used before temporary volume fields are installed.
func (v *Volume) loadNodeOnDevice(ref physicalRef) (*mapNode, error) {
	record := make([]byte, PhysicalSectorSize)
	if err := v.device.ReadSector(ref.Sector, record); err != nil {
		return nil, err
	}
	return v.decodeNode(record, ref)
}

// Write stores exactly one 4096-byte logical sector.
func (v *Volume) Write(logicalSector uint64, plaintext []byte) error {
	if len(plaintext) != LogicalSectorSize {
		return fmt.Errorf("%w: got %d bytes", ErrInvalidSize, len(plaintext))
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := v.ensureWritableLocked(); err != nil {
		return err
	}
	if len(v.free) < int(v.rootLevel)*2+2 {
		return ErrNoSpace
	}

	epoch, err := v.reserveEpochLocked()
	if err != nil {
		return err
	}
	tx := newWriteTx()
	root, level, err := v.writeOneMappingLocked(tx, v.root, v.rootLevel, logicalSector, plaintext, epoch, v.keyGeneration, v.aead, v.nonceKey)
	if err != nil {
		v.abortTxLocked(tx)
		return err
	}
	return v.commitTxLocked(tx, root, level, v.keyGeneration, epoch)
}

// WriteSector is an alias for Write.
func (v *Volume) WriteSector(logicalSector uint64, plaintext []byte) error {
	return v.Write(logicalSector, plaintext)
}

// Read authenticates and returns exactly one logical sector from the mapping
// root captured for this operation.
func (v *Volume) Read(logicalSector uint64) ([]byte, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := v.ensureUsableLocked(); err != nil {
		return nil, err
	}
	root := v.root
	rootLevel := v.rootLevel
	entry, found, err := v.lookupInTree(root, rootLevel, logicalSector)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, ErrSectorNotFound
	}
	return v.readMappingLocked(entry, v.keyGeneration, v.aead, v.nonceKey)
}

// ReadSector is an alias for Read.
func (v *Volume) ReadSector(logicalSector uint64) ([]byte, error) {
	return v.Read(logicalSector)
}

func (v *Volume) readMappingLocked(
	entry mappingEntry,
	keyGeneration uint64,
	aead cipher.AEAD,
	nonceKey []byte,
) ([]byte, error) {
	record := make([]byte, PhysicalSectorSize)
	if err := v.device.ReadSector(entry.Physical, record); err != nil {
		return nil, err
	}
	return openDataRecord(
		aead,
		nonceKey,
		record,
		v.volumeID,
		keyGeneration,
		entry.Logical,
		entry.Physical,
		entry.PhysicalGeneration,
	)
}

// Relocate decrypts one logical sector and writes a fresh ciphertext under a
// new physical sector and a strictly larger write epoch.
func (v *Volume) Relocate(logicalSector uint64) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := v.ensureWritableLocked(); err != nil {
		return err
	}
	entry, found, err := v.lookupInTree(v.root, v.rootLevel, logicalSector)
	if err != nil {
		return err
	}
	if !found {
		return ErrSectorNotFound
	}
	plaintext, err := v.readMappingLocked(entry, v.keyGeneration, v.aead, v.nonceKey)
	if err != nil {
		return err
	}
	defer wipeBytes(plaintext)
	if len(v.free) < int(v.rootLevel)*2+2 {
		return ErrNoSpace
	}

	epoch, err := v.reserveEpochLocked()
	if err != nil {
		return err
	}
	tx := newWriteTx()
	root, level, err := v.writeOneMappingLocked(
		tx,
		v.root,
		v.rootLevel,
		logicalSector,
		plaintext,
		epoch,
		v.keyGeneration,
		v.aead,
		v.nonceKey,
	)
	if err != nil {
		v.abortTxLocked(tx)
		return err
	}
	return v.commitTxLocked(tx, root, level, v.keyGeneration, epoch)
}

// RelocateSector is an alias for Relocate.
func (v *Volume) RelocateSector(logicalSector uint64) error {
	return v.Relocate(logicalSector)
}

// RelocateAll relocates every currently committed logical sector.
func (v *Volume) RelocateAll() error {
	v.mu.Lock()
	mappings, err := v.mappingsLocked()
	v.mu.Unlock()
	if err != nil {
		return err
	}
	for _, mapping := range mappings {
		if err := v.Relocate(mapping.Logical); err != nil {
			return err
		}
	}
	return nil
}

// RotateKey re-encrypts every mapping under a strictly newer key generation.
// It never copies old ciphertext. If the rotation is interrupted, the previous
// key generation and root remain readable.
func (v *Volume) RotateKey(newGeneration uint64) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := v.ensureWritableLocked(); err != nil {
		return err
	}
	if newGeneration <= v.keyGeneration {
		if v.keyGeneration == math.MaxUint64 {
			return ErrKeyGenerationExhausted
		}
		return fmt.Errorf("%w: %d -> %d", ErrKeyGenerationRollback, v.keyGeneration, newGeneration)
	}
	aeadKey, nonceKey := deriveDataKeys(v.masterKey, v.volumeID, newGeneration)
	defer wipeBytes(aeadKey)
	defer wipeBytes(nonceKey)
	newAEAD, err := newAEAD(aeadKey)
	if err != nil {
		return err
	}
	mappings, err := v.mappingsLocked()
	if err != nil {
		return err
	}

	tx := newWriteTx()
	root := v.root
	level := v.rootLevel
	lastEpoch := v.writeEpoch
	for _, mapping := range mappings {
		entry := mappingEntry{
			Logical:            mapping.Logical,
			Physical:           mapping.Physical,
			PhysicalGeneration: mapping.PhysicalGeneration,
		}
		plaintext, err := v.readMappingLocked(entry, v.keyGeneration, v.aead, v.nonceKey)
		if err != nil {
			v.abortTxLocked(tx)
			return err
		}
		epoch, err := v.reserveEpochLocked()
		if err != nil {
			wipeBytes(plaintext)
			v.abortTxLocked(tx)
			return err
		}
		lastEpoch = epoch
		root, level, err = v.writeOneMappingLocked(
			tx,
			root,
			level,
			mapping.Logical,
			plaintext,
			epoch,
			newGeneration,
			newAEAD,
			nonceKey,
		)
		wipeBytes(plaintext)
		if err != nil {
			v.abortTxLocked(tx)
			return err
		}
	}
	if len(mappings) == 0 {
		epoch, err := v.reserveEpochLocked()
		if err != nil {
			v.abortTxLocked(tx)
			return err
		}
		lastEpoch = epoch
	}
	return v.commitTxLocked(tx, root, level, newGeneration, lastEpoch)
}

func (v *Volume) writeOneMappingLocked(
	tx *writeTx,
	oldRoot physicalRef,
	oldLevel uint16,
	logical uint64,
	plaintext []byte,
	epoch uint64,
	keyGeneration uint64,
	aead cipher.AEAD,
	nonceKey []byte,
) (physicalRef, uint16, error) {
	physical, err := v.allocPhysical(tx, epoch)
	if err != nil {
		return physicalRef{}, 0, err
	}
	record, err := sealDataRecord(
		aead,
		nonceKey,
		v.volumeID,
		keyGeneration,
		logical,
		physical.Sector,
		physical.Generation,
		epoch,
		plaintext,
	)
	if err != nil {
		return physicalRef{}, 0, err
	}
	if err := v.writePhysical(physical, record); err != nil {
		return physicalRef{}, 0, err
	}
	ref, level, leftMax, split, err := v.insertIntoTree(
		tx,
		oldRoot,
		oldLevel,
		logical,
		physical.Sector,
		physical.Generation,
		epoch,
	)
	if err != nil {
		return physicalRef{}, 0, err
	}
	return v.finalizeRoot(tx, ref, level, leftMax, split, epoch)
}

func (v *Volume) reserveEpochLocked() (uint64, error) {
	if v.writeEpoch == math.MaxUint64 {
		return 0, ErrWriteEpochExhausted
	}
	next := v.writeEpoch + 1
	state := v.currentStateLocked()
	state.ReservedEpoch = next
	if err := v.writeStateLocked(state); err != nil {
		return 0, err
	}
	v.writeEpoch = next
	return next, nil
}

func (v *Volume) currentStateLocked() volumeState {
	return volumeState{
		VolumeID:      v.volumeID,
		KeyGeneration: v.keyGeneration,
		ReservedEpoch: v.writeEpoch,
		CommitEpoch:   v.commitEpoch,
		RootRevision:  v.rootRevision,
		Root:          v.root,
		RootLevel:     v.rootLevel,
		DataStart:     DataStartSector,
		SectorCount:   v.sectorCount,
	}
}

func (v *Volume) commitTxLocked(
	tx *writeTx,
	newRoot physicalRef,
	newLevel uint16,
	newKeyGeneration uint64,
	commitEpoch uint64,
) error {
	newUsed, err := v.collectReferenced(newRoot, newLevel)
	if err != nil {
		v.abortTxLocked(tx)
		return err
	}
	if err := v.device.Sync(); err != nil {
		v.abortTxLocked(tx)
		return err
	}

	oldUsed := v.used
	if v.rootRevision == math.MaxUint64 {
		v.abortTxLocked(tx)
		return ErrRootRevisionExhausted
	}
	state := v.currentStateLocked()
	state.KeyGeneration = newKeyGeneration
	state.CommitEpoch = commitEpoch
	state.RootRevision++
	state.Root = newRoot
	state.RootLevel = newLevel
	if err := v.writeStateLocked(state); err != nil {
		if !v.needsReload {
			v.abortTxLocked(tx)
		}
		return err
	}

	aeadKey, nonceKey := deriveDataKeys(v.masterKey, v.volumeID, newKeyGeneration)
	aead, err := newAEAD(aeadKey)
	if err != nil {
		v.needsReload = true
		return err
	}
	wipeBytes(v.aeadKey)
	wipeBytes(v.nonceKey)
	v.aeadKey = aeadKey
	v.nonceKey = nonceKey
	v.aead = aead
	v.keyGeneration = newKeyGeneration
	v.commitEpoch = commitEpoch
	v.rootRevision++
	v.root = newRoot
	v.rootLevel = newLevel
	v.used = newUsed

	stale := make([]uint64, 0)
	for sector := range oldUsed {
		if _, ok := newUsed[sector]; !ok {
			stale = append(stale, sector)
		}
	}
	for sector := range tx.allocations {
		if _, ok := newUsed[sector]; !ok {
			stale = append(stale, sector)
		}
	}
	sort.Slice(stale, func(i, j int) bool { return stale[i] < stale[j] })
	if v.metadataClean {
		for _, sector := range stale {
			if err := v.zeroPhysical(sector); err == nil {
				v.addFreeLocked(sector)
			} else {
				v.pendingFree[sector] = struct{}{}
			}
		}
	} else {
		for _, sector := range stale {
			v.pendingFree[sector] = struct{}{}
		}
	}
	_ = v.cleanPendingFreeLocked()
	return nil
}

func (v *Volume) abortTxLocked(tx *writeTx) {
	if v.needsReload {
		return
	}
	for sector := range tx.allocations {
		if err := v.zeroPhysical(sector); err == nil {
			v.addFreeLocked(sector)
		} else {
			v.pendingFree[sector] = struct{}{}
		}
	}
}

func (v *Volume) allocPhysical(tx *writeTx, generation uint64) (physicalRef, error) {
	for len(v.free) > 0 {
		sector := v.free[0]
		v.free = v.free[1:]
		if _, ok := v.freeSet[sector]; !ok {
			continue
		}
		delete(v.freeSet, sector)
		if _, used := v.used[sector]; used {
			continue
		}
		if generation == 0 {
			return physicalRef{}, fmt.Errorf("%w: zero generation", ErrCorrupt)
		}
		if _, duplicate := tx.allocations[sector]; duplicate {
			continue
		}
		tx.allocations[sector] = struct{}{}
		return physicalRef{Sector: sector, Generation: generation}, nil
	}
	return physicalRef{}, ErrNoSpace
}

func (v *Volume) addFreeLocked(sector uint64) {
	if sector < DataStartSector || sector >= v.sectorCount {
		return
	}
	if _, used := v.used[sector]; used {
		return
	}
	if _, already := v.freeSet[sector]; already {
		return
	}
	v.freeSet[sector] = struct{}{}
	v.free = append(v.free, sector)
	sort.Slice(v.free, func(i, j int) bool { return v.free[i] < v.free[j] })
	delete(v.pendingFree, sector)
}

func (v *Volume) writePhysical(ref physicalRef, record []byte) error {
	if len(record) != PhysicalSectorSize {
		return fmt.Errorf("%w: physical record has %d bytes", ErrInvalidSize, len(record))
	}
	if err := v.device.WriteSector(ref.Sector, record); err != nil {
		return err
	}
	readback := make([]byte, PhysicalSectorSize)
	if err := v.device.ReadSector(ref.Sector, readback); err != nil {
		return err
	}
	if !equalRecordDigest(record, readback) {
		return ErrReadbackMismatch
	}
	return nil
}

func (v *Volume) zeroPhysical(sector uint64) error {
	zeros := make([]byte, PhysicalSectorSize)
	if err := v.device.WriteSector(sector, zeros); err != nil {
		return err
	}
	readback := make([]byte, PhysicalSectorSize)
	if err := v.device.ReadSector(sector, readback); err != nil {
		return err
	}
	if !isZero(readback) {
		return ErrReadbackMismatch
	}
	return nil
}

func equalRecordDigest(a, b []byte) bool {
	want := sha256.Sum256(a)
	got := sha256.Sum256(b)
	return subtle.ConstantTimeCompare(want[:], got[:]) == 1
}

func isZero(p []byte) bool {
	return bytes.Equal(p, make([]byte, len(p)))
}

func wipeBytes(p []byte) {
	for i := range p {
		p[i] = 0
	}
}

func (v *Volume) ensureUsableLocked() error {
	if v.closed {
		return ErrClosed
	}
	if v.needsReload {
		return ErrStateUncertain
	}
	return nil
}

func (v *Volume) ensureWritableLocked() error {
	if err := v.ensureUsableLocked(); err != nil {
		return err
	}
	if v.writeEpoch == math.MaxUint64 {
		return ErrWriteEpochExhausted
	}
	return nil
}

// State returns the last committed metadata snapshot.
func (v *Volume) State() (VolumeState, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := v.ensureUsableLocked(); err != nil {
		return VolumeState{}, err
	}
	return VolumeState{
		VolumeID:      v.volumeID,
		KeyGeneration: v.keyGeneration,
		WriteEpoch:    v.writeEpoch,
		CommitEpoch:   v.commitEpoch,
		RootRevision:  v.rootRevision,
		SectorCount:   v.sectorCount,
	}, nil
}

// WriteEpoch returns the reserved, non-decreasing write epoch.
func (v *Volume) WriteEpoch() uint64 {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.writeEpoch
}

// Mappings returns all committed mappings in logical-sector order.
func (v *Volume) Mappings() ([]Mapping, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := v.ensureUsableLocked(); err != nil {
		return nil, err
	}
	return v.mappingsLocked()
}

func (v *Volume) mappingsLocked() ([]Mapping, error) {
	entries := make([]mappingEntry, 0)
	if err := v.walkMappings(v.root, v.rootLevel, &entries); err != nil {
		return nil, err
	}
	mappings := make([]Mapping, len(entries))
	for i, entry := range entries {
		mappings[i] = Mapping{
			Logical:            entry.Logical,
			Physical:           entry.Physical,
			PhysicalGeneration: entry.PhysicalGeneration,
		}
	}
	return mappings, nil
}

func (v *Volume) walkMappings(ref physicalRef, level uint16, out *[]mappingEntry) error {
	if ref.Sector == 0 {
		return nil
	}
	node, err := v.loadNode(ref)
	if err != nil {
		return err
	}
	if node.Level != level {
		return fmt.Errorf("%w: mapping walk level mismatch", ErrCorrupt)
	}
	if node.Kind == nodeLeaf {
		*out = append(*out, node.Leaf...)
		return nil
	}
	for _, child := range node.Internal {
		if err := v.walkMappings(child.Ref, level-1, out); err != nil {
			return err
		}
	}
	return nil
}

// Reclaim clears unreferenced physical sectors and retries pending frees. If
// any older state records remain valid, their roots are treated as live until
// those records are invalidated.
func (v *Volume) Reclaim() error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := v.ensureUsableLocked(); err != nil {
		return err
	}
	_ = v.invalidateOtherStatesLocked()
	protected := map[uint64]struct{}(nil)
	if !v.metadataClean {
		var err error
		protected, err = v.collectAllStateReferencesLocked()
		if err != nil {
			return err
		}
	}
	var firstErr error
	for sector := uint64(DataStartSector); sector < v.sectorCount; sector++ {
		if _, used := v.used[sector]; used {
			continue
		}
		if protected != nil {
			if _, hold := protected[sector]; hold {
				continue
			}
		}
		if _, ok := v.freeSet[sector]; ok {
			continue
		}
		if err := v.zeroPhysical(sector); err != nil {
			v.pendingFree[sector] = struct{}{}
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		v.addFreeLocked(sector)
	}
	if err := v.device.Sync(); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

func (v *Volume) cleanPendingFreeLocked() error {
	if !v.metadataClean {
		return nil
	}
	pending := make([]uint64, 0, len(v.pendingFree))
	for sector := range v.pendingFree {
		if _, used := v.used[sector]; used {
			delete(v.pendingFree, sector)
			continue
		}
		pending = append(pending, sector)
	}
	sort.Slice(pending, func(i, j int) bool { return pending[i] < pending[j] })
	var firstErr error
	for _, sector := range pending {
		if err := v.zeroPhysical(sector); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		v.addFreeLocked(sector)
	}
	return firstErr
}

func (v *Volume) rebuildFreeLocked(protected map[uint64]struct{}) error {
	v.free = v.free[:0]
	v.freeSet = make(map[uint64]struct{})
	v.pendingFree = make(map[uint64]struct{})
	for sector := uint64(DataStartSector); sector < v.sectorCount; sector++ {
		if _, used := v.used[sector]; used {
			continue
		}
		if protected != nil {
			if _, hold := protected[sector]; hold {
				v.pendingFree[sector] = struct{}{}
				continue
			}
		}
		zero, err := v.sectorIsZero(sector)
		if err != nil {
			return err
		}
		if !zero {
			if err := v.zeroPhysical(sector); err != nil {
				v.pendingFree[sector] = struct{}{}
				continue
			}
		}
		v.freeSet[sector] = struct{}{}
		v.free = append(v.free, sector)
	}
	sort.Slice(v.free, func(i, j int) bool { return v.free[i] < v.free[j] })
	return nil
}

func (v *Volume) sectorIsZero(sector uint64) (bool, error) {
	record := make([]byte, PhysicalSectorSize)
	if err := v.device.ReadSector(sector, record); err != nil {
		return false, err
	}
	return isZero(record), nil
}

func (v *Volume) writeStateLocked(state volumeState) error {
	if v.stateSeq == math.MaxUint64 {
		return fmt.Errorf("%w: metadata sequence exhausted", ErrCorrupt)
	}
	state.Sequence = v.stateSeq + 1
	record := encodeState(v.metaKey, state)
	target := (v.latestState + 1) % StateSectorCount
	if target < 0 {
		target += StateSectorCount
	}
	if err := v.device.WriteSector(uint64(target), record); err != nil {
		visible, readOK := v.stateSlotVisibility(target, state)
		if !readOK || visible {
			v.needsReload = true
			return fmt.Errorf("%w: state write: %v", ErrStateUncertain, err)
		}
		return err
	}
	if err := v.device.Sync(); err != nil {
		visible, readOK := v.stateSlotVisibility(target, state)
		if !readOK || visible {
			v.needsReload = true
		}
		return fmt.Errorf("%w: state sync: %v", ErrStateUncertain, err)
	}
	readback := make([]byte, PhysicalSectorSize)
	if err := v.device.ReadSector(uint64(target), readback); err != nil {
		v.needsReload = true
		return fmt.Errorf("%w: state readback: %v", ErrStateUncertain, err)
	}
	if !equalRecordDigest(record, readback) {
		v.needsReload = true
		return fmt.Errorf("%w: state record", ErrReadbackMismatch)
	}

	oldLatest := v.latestState
	v.latestState = target
	v.stateSeq = state.Sequence
	if oldLatest >= 0 && oldLatest != target {
		if err := v.invalidateStateSlotLocked(oldLatest); err != nil {
			v.metadataClean = false
		}
	}
	return nil
}

func (v *Volume) stateSlotVisibility(slot int, expected volumeState) (visible, readOK bool) {
	record := make([]byte, PhysicalSectorSize)
	if err := v.device.ReadSector(uint64(slot), record); err != nil {
		return false, false
	}
	state, err := decodeState(v.metaKey, record)
	return err == nil && sameState(state, expected), true
}

func (v *Volume) invalidateStateSlotLocked(slot int) error {
	if slot < 0 || slot >= StateSectorCount {
		return fmt.Errorf("%w: state slot %d", ErrCorrupt, slot)
	}
	record := make([]byte, PhysicalSectorSize)
	if err := v.device.WriteSector(uint64(slot), record); err != nil {
		return err
	}
	if err := v.device.Sync(); err != nil {
		return err
	}
	readback := make([]byte, PhysicalSectorSize)
	if err := v.device.ReadSector(uint64(slot), readback); err != nil {
		return err
	}
	if !isZero(readback) {
		return ErrReadbackMismatch
	}
	return nil
}

func (v *Volume) invalidateOtherStatesLocked() error {
	var firstErr error
	for slot := 0; slot < StateSectorCount; slot++ {
		if slot == v.latestState {
			continue
		}
		record := make([]byte, PhysicalSectorSize)
		if err := v.device.ReadSector(uint64(slot), record); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if isZero(record) {
			continue
		}
		if err := v.invalidateStateSlotLocked(slot); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if firstErr == nil {
		v.metadataClean = true
	} else {
		v.metadataClean = false
	}
	return firstErr
}

func (v *Volume) collectAllStateReferencesLocked() (map[uint64]struct{}, error) {
	protected := make(map[uint64]struct{})
	for slot := 0; slot < StateSectorCount; slot++ {
		record := make([]byte, PhysicalSectorSize)
		if err := v.device.ReadSector(uint64(slot), record); err != nil {
			return nil, err
		}
		state, err := decodeState(v.metaKey, record)
		if err != nil {
			continue
		}
		if state.Root.Sector == 0 {
			continue
		}
		used, err := v.collectReferenced(state.Root, state.RootLevel)
		if err != nil {
			return nil, err
		}
		for sector := range used {
			protected[sector] = struct{}{}
		}
	}
	return protected, nil
}

// Reload re-reads the authoritative state log. It rejects a root, epoch, or key
// generation older than the values already observed by this Volume.
func (v *Volume) Reload() error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.closed {
		return ErrClosed
	}
	states, _, _, err := v.scanStatesLocked()
	if err != nil {
		return err
	}
	latest, err := chooseLatestState(states)
	if err != nil {
		return err
	}
	if latest.State.ReservedEpoch < v.writeEpoch {
		return ErrWriteEpochRollback
	}
	if latest.State.KeyGeneration < v.keyGeneration {
		return ErrKeyGenerationRollback
	}
	if latest.State.RootRevision < v.rootRevision ||
		(latest.State.RootRevision == v.rootRevision && latest.State.Root != v.root) {
		return ErrRootRollback
	}
	if latest.State.Sequence < v.stateSeq {
		return ErrRootRollback
	}
	observedEpoch, err := v.maxPhysicalEpochLocked()
	if err != nil {
		return err
	}
	if observedEpoch > latest.State.ReservedEpoch {
		return fmt.Errorf("%w: physical epoch %d exceeds state reservation %d",
			ErrWriteEpochRollback, observedEpoch, latest.State.ReservedEpoch)
	}
	oldSeq := v.stateSeq
	if err := v.installStateLocked(latest.State, latest.Slot); err != nil {
		return err
	}
	if latest.State.Sequence < oldSeq {
		return ErrRootRollback
	}
	_ = v.invalidateOtherStatesLocked()
	protected := map[uint64]struct{}(nil)
	if !v.metadataClean {
		var err error
		protected, err = v.collectAllStateReferencesLocked()
		if err != nil {
			return err
		}
	}
	return v.rebuildFreeLocked(protected)
}

// Close releases key material. It does not discard committed state.
func (v *Volume) Close() error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.closed {
		return nil
	}
	err := v.device.Sync()
	v.closed = true
	wipeBytes(v.masterKey)
	wipeBytes(v.metaKey)
	wipeBytes(v.aeadKey)
	wipeBytes(v.nonceKey)
	v.aead = nil
	return err
}
