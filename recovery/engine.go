package recovery

import (
	"crypto/cipher"
	"crypto/hmac"
	"errors"
	"fmt"
	"io"
	"sync"
)

// Engine is the embeddable recovery protocol implementation. It serializes
// operations and never trusts a process-local copy of an index as commit
// evidence; every mutation first re-reads the fixed lane.
type Engine struct {
	mu       sync.Mutex
	store    StableStore
	keys     Keyring
	cfg      Config
	rng      io.Reader
	aeads    map[TenantID]cipher.AEAD
	observer Observer

	openBatch *Batch
	applied   map[TenantID]CommitReceipt
}

// Option customizes an Engine.
type Option func(*Engine)

// WithObserver installs a public I/O observer.
func WithObserver(observer Observer) Option {
	return func(e *Engine) { e.observer = observer }
}

// WithRand overrides the nonce and transaction nonce source.
func WithRand(rng io.Reader) Option {
	return func(e *Engine) {
		if rng != nil {
			e.rng = rng
		}
	}
}

// New validates the bounded layout and constructs an engine.
func New(store StableStore, keys Keyring, cfg Config, opts ...Option) (*Engine, error) {
	if store == nil {
		return nil, fmt.Errorf("%w: nil store", ErrConfig)
	}
	if cfg.FrameSize == 0 {
		cfg.FrameSize = defaultFrameSize
	}
	if cfg.MaxRetries < 0 {
		return nil, fmt.Errorf("%w: negative MaxRetries", ErrConfig)
	}
	if cfg.MaxRetries == 0 {
		cfg.MaxRetries = defaultMaxRetries
	}
	if cfg.MaxRetries > 8 {
		cfg.MaxRetries = 8
	}
	cfg.Layout.Lanes = append([]LaneSpec(nil), cfg.Layout.Lanes...)
	keyCopy := make(Keyring, len(keys))
	for tenant, pair := range keys {
		keyCopy[tenant] = pair
	}
	if err := cfg.Layout.validate(cfg.FrameSize); err != nil {
		return nil, err
	}
	if geometry, ok := store.(StorageGeometry); ok {
		if geometry.FrameSize() != cfg.FrameSize {
			return nil, fmt.Errorf("%w: store frame size %d, config %d", ErrGeometry, geometry.FrameSize(), cfg.FrameSize)
		}
		for region := RegionA; region <= RegionB; region++ {
			if geometry.RegionSlots(region) < cfg.Layout.RegionSlots {
				return nil, fmt.Errorf("%w: store has %d slots, layout needs %d", ErrGeometry, geometry.RegionSlots(region), cfg.Layout.RegionSlots)
			}
		}
	}
	engine := &Engine{
		store:   store,
		keys:    keyCopy,
		cfg:     cfg,
		rng:     cfg.Rand,
		aeads:   make(map[TenantID]cipher.AEAD, len(keys)),
		applied: make(map[TenantID]CommitReceipt),
	}
	if engine.rng == nil {
		engine.rng = defaultRand()
	}
	for _, opt := range opts {
		opt(engine)
	}
	return engine, nil
}

// SetObserver changes the public I/O observer.
func (e *Engine) SetObserver(observer Observer) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.observer = observer
}

// Plan returns the fixed upper bound for recovering one tenant.
func (e *Engine) Plan(tenant TenantID) (RecoveryPlan, error) {
	lane, err := e.cfg.Layout.lane(tenant)
	if err != nil {
		return RecoveryPlan{}, err
	}
	reads := 2 * lane.Slots
	return RecoveryPlan{
		Tenant:          tenant,
		LaneStart:       lane.Start,
		LaneSlots:       lane.Slots,
		FrameSize:       e.cfg.FrameSize,
		Regions:         2,
		MaxReads:        reads,
		MaxReadAttempts: reads * (e.cfg.MaxRetries + 1),
		MaxWrites:       0,
		MaxRetries:      e.cfg.MaxRetries,
		MaxBytes:        reads * e.cfg.FrameSize,
	}, nil
}

func (e *Engine) aead(tenant TenantID) (cipher.AEAD, error) {
	aead, ok := e.aeads[tenant]
	if ok {
		return aead, nil
	}
	pair, ok := e.keys.pair(tenant)
	if !ok {
		return nil, fmt.Errorf("%w: tenant %s", ErrKey, tenant)
	}
	aead, err := newAEAD(pair.Encryption)
	if err != nil {
		return nil, fmt.Errorf("recovery: AEAD for %s: %w", tenant, err)
	}
	e.aeads[tenant] = aead
	return aead, nil
}

func (e *Engine) emit(trace *[]Event, ev Event) {
	if trace != nil {
		*trace = append(*trace, ev)
	}
	if e.observer != nil {
		e.observer.Observe(ev)
	}
}

func (e *Engine) readSlot(trace *[]Event, phase Phase, region Region, slot int) ([]byte, error) {
	var last error
	for attempt := 0; attempt <= e.cfg.MaxRetries; attempt++ {
		ev := Event{
			Op:      OpRead,
			Region:  region,
			Slot:    slot,
			Length:  e.cfg.FrameSize,
			Phase:   phase,
			Attempt: attempt + 1,
			Retry:   attempt,
		}
		frame, err := e.store.Read(region, slot, e.cfg.FrameSize)
		if err == nil && len(frame) != e.cfg.FrameSize {
			err = fmt.Errorf("%w: read returned %d bytes", ErrGeometry, len(frame))
		}
		if err != nil {
			ev.Failed = true
			last = err
			e.emit(trace, ev)
			continue
		}
		e.emit(trace, ev)
		return frame, nil
	}
	if last == nil {
		last = ErrIO
	}
	return nil, last
}

func (e *Engine) writeSlot(trace *[]Event, phase Phase, region Region, slot int, frame []byte) error {
	var last error
	for attempt := 0; attempt <= e.cfg.MaxRetries; attempt++ {
		ev := Event{
			Op:      OpWrite,
			Region:  region,
			Slot:    slot,
			Length:  len(frame),
			Phase:   phase,
			Attempt: attempt + 1,
			Retry:   attempt,
		}
		err := e.store.Write(region, slot, frame)
		if err != nil {
			ev.Failed = true
			last = err
			e.emit(trace, ev)
			continue
		}
		e.emit(trace, ev)
		return nil
	}
	if last == nil {
		last = ErrIO
	}
	return last
}

func (e *Engine) sealRecord(region Region, slot int, tenant TenantID, rec record) ([]byte, error) {
	aead, err := e.aead(tenant)
	if err != nil {
		return nil, err
	}
	payload, err := rec.encode()
	if err != nil {
		return nil, err
	}
	return sealFrame(aead, e.rng, region, slot, tenant, payload, e.cfg.FrameSize)
}

func (e *Engine) openRecord(region Region, slot int, tenant TenantID, frame []byte) (record, error) {
	aead, err := e.aead(tenant)
	if err != nil {
		return record{}, err
	}
	payload, err := openFrame(aead, region, slot, tenant, frame, e.cfg.FrameSize)
	if err != nil {
		return record{}, err
	}
	return decodeRecord(payload)
}

// Batch is one optimistic, signed snapshot transaction.
type Batch struct {
	engine   *Engine
	txn      SignedTxn
	lease    Lease
	deleted  bool
	region   Region
	start    int
	next     int
	blocks   []Block
	blockIDs map[BlockID]struct{}
	frames   [][]byte
	lastHash Hash
	phase    int
	done     bool
	failed   error
	trace    []Event
}

// Events returns a copy of this batch's public I/O trace.
func (b *Batch) Events() []Event {
	b.engine.mu.Lock()
	defer b.engine.mu.Unlock()
	return append([]Event(nil), b.trace...)
}

// Begin starts a signed snapshot transaction. It durably writes only a header
// into the inactive region; no data is visible to recovery until Commit writes
// a signed decision and receipt.
func (e *Engine) Begin(txn SignedTxn) (*Batch, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.beginLocked(txn, false, nil)
}

func (e *Engine) beginLocked(txn SignedTxn, deleted bool, trace *[]Event) (*Batch, error) {
	if err := txn.validate(); err != nil {
		return nil, err
	}
	if !e.keys.VerifyTransaction(txn) {
		return nil, ErrSignature
	}
	lease := txn.lease()
	if err := lease.validate(txn.Tenant); err != nil {
		return nil, err
	}
	if e.openBatch != nil {
		return nil, fmt.Errorf("recovery: another batch is open")
	}
	lane, err := e.cfg.Layout.lane(txn.Tenant)
	if err != nil {
		return nil, err
	}
	scan, err := e.scanLane(txn.Tenant, PhaseBegin, trace)
	if err != nil {
		return nil, err
	}
	latest, _, err := selectCandidate(scan.candidates, lease, 0)
	if err != nil {
		return nil, err
	}
	base := uint64(0)
	region := RegionA
	if latest != nil {
		base = latest.receipt.Generation
		if latest.region == RegionA {
			region = RegionB
		} else {
			region = RegionA
		}
	}
	if txn.BaseGeneration != base {
		return nil, fmt.Errorf("%w: base %d, current %d", ErrConflict, txn.BaseGeneration, base)
	}
	if base == ^uint64(0) {
		return nil, fmt.Errorf("%w: generation counter exhausted", ErrCapacity)
	}
	generation := base + 1
	header := record{
		Kind:           recordHeader,
		Deleted:        deleted,
		Tenant:         txn.Tenant,
		LeaseEpoch:     txn.LeaseEpoch,
		LeaseID:        txn.LeaseID,
		Generation:     generation,
		BaseGeneration: base,
		Txn:            txn.ID,
		TxnNonce:       txn.Nonce,
		TxnSignature:   txn.Signature,
		Index:          0,
	}
	frame, err := e.sealRecord(region, lane.Start, txn.Tenant, header)
	if err != nil {
		return nil, err
	}
	if err := e.writeSlot(trace, PhaseBegin, region, lane.Start, frame); err != nil {
		return nil, fmt.Errorf("%w: header: %v", ErrIO, err)
	}
	batch := &Batch{
		engine:   e,
		txn:      txn,
		lease:    lease,
		deleted:  deleted,
		region:   region,
		start:    lane.Start,
		next:     lane.Start + 1,
		blockIDs: make(map[BlockID]struct{}),
		frames:   [][]byte{frame},
		lastHash: hashBytes(frame),
	}
	e.openBatch = batch
	return batch, nil
}

// Stage appends one complete block to the uncommitted inactive snapshot.
func (b *Batch) Stage(block Block) error {
	b.engine.mu.Lock()
	defer b.engine.mu.Unlock()
	if b.done || b.engine.openBatch != b {
		return ErrBatchDone
	}
	if b.failed != nil {
		return b.failed
	}
	if b.deleted {
		return b.fail(fmt.Errorf("recovery: delete transaction cannot stage blocks"))
	}
	if block.Tenant != b.txn.Tenant {
		return b.fail(ErrBlockTenant)
	}
	if _, exists := b.blockIDs[block.ID]; exists {
		return b.fail(ErrDuplicateID)
	}
	lane, err := b.engine.cfg.Layout.lane(b.txn.Tenant)
	if err != nil {
		return err
	}
	if len(b.blocks)+1 > lane.Slots-minLaneSlots {
		return b.fail(ErrCapacity)
	}
	rec := record{
		Kind:           recordBlock,
		Deleted:        false,
		Tenant:         b.txn.Tenant,
		LeaseEpoch:     b.txn.LeaseEpoch,
		LeaseID:        b.txn.LeaseID,
		Generation:     b.txn.BaseGeneration + 1,
		BaseGeneration: b.txn.BaseGeneration,
		Txn:            b.txn.ID,
		TxnNonce:       b.txn.Nonce,
		TxnSignature:   b.txn.Signature,
		Index:          uint32(len(b.blocks)),
		Prev:           b.lastHash,
		Block:          block,
	}
	frame, err := b.engine.sealRecord(b.region, b.next, b.txn.Tenant, rec)
	if err != nil {
		if errors.Is(err, ErrBlockTooLarge) {
			return b.fail(fmt.Errorf("%w: block %s", ErrBlockTooLarge, block.ID))
		}
		return b.fail(err)
	}
	if err := b.engine.writeSlot(&b.trace, PhaseStage, b.region, b.next, frame); err != nil {
		return b.fail(fmt.Errorf("%w: block frame: %v", ErrIO, err))
	}
	b.blocks = append(b.blocks, cloneBlock(block))
	b.blockIDs[block.ID] = struct{}{}
	b.frames = append(b.frames, frame)
	b.lastHash = hashBytes(frame)
	b.next++
	return nil
}

// Commit writes prepare, a transaction-signed decision, and the durable ack
// receipt. The snapshot becomes generally recoverable only after the receipt
// write is acknowledged.
func (b *Batch) Commit() (CommitReceipt, error) {
	b.engine.mu.Lock()
	defer b.engine.mu.Unlock()
	if b.done || b.engine.openBatch != b {
		return CommitReceipt{}, ErrBatchDone
	}
	if b.failed != nil {
		return CommitReceipt{}, b.failed
	}
	return b.commitLocked()
}

func (b *Batch) fail(err error) error {
	b.failed = err
	b.done = true
	if b.engine.openBatch == b {
		b.engine.openBatch = nil
	}
	return err
}

func (b *Batch) commitLocked() (CommitReceipt, error) {
	lane, err := b.engine.cfg.Layout.lane(b.txn.Tenant)
	if err != nil {
		return CommitReceipt{}, err
	}
	if len(b.blocks)+minLaneSlots > lane.Slots {
		return CommitReceipt{}, ErrCapacity
	}
	count := uint32(len(b.blocks))
	snapHash := snapshotHash(b.blocks)

	if b.phase == 0 {
		prepare := record{
			Kind:           recordPrepare,
			Deleted:        b.deleted,
			Tenant:         b.txn.Tenant,
			LeaseEpoch:     b.txn.LeaseEpoch,
			LeaseID:        b.txn.LeaseID,
			Generation:     b.txn.BaseGeneration + 1,
			BaseGeneration: b.txn.BaseGeneration,
			Txn:            b.txn.ID,
			TxnNonce:       b.txn.Nonce,
			TxnSignature:   b.txn.Signature,
			Index:          count,
			BlockCount:     count,
			Prev:           b.lastHash,
			Root:           hashBytes(b.frames...),
			SnapshotHash:   snapHash,
		}
		frame, err := b.engine.sealRecord(b.region, b.next, b.txn.Tenant, prepare)
		if err != nil {
			return CommitReceipt{}, err
		}
		if err := b.engine.writeSlot(&b.trace, PhasePrepare, b.region, b.next, frame); err != nil {
			return CommitReceipt{}, fmt.Errorf("%w: prepare: %v", ErrIO, err)
		}
		b.frames = append(b.frames, frame)
		b.lastHash = hashBytes(frame)
		b.next++
		b.phase = 1
	}

	if b.phase == 1 {
		pair := b.engine.keys[b.txn.Tenant]
		decision := record{
			Kind:           recordDecision,
			Deleted:        b.deleted,
			Tenant:         b.txn.Tenant,
			LeaseEpoch:     b.txn.LeaseEpoch,
			LeaseID:        b.txn.LeaseID,
			Generation:     b.txn.BaseGeneration + 1,
			BaseGeneration: b.txn.BaseGeneration,
			Txn:            b.txn.ID,
			TxnNonce:       b.txn.Nonce,
			TxnSignature:   b.txn.Signature,
			Index:          count + 1,
			BlockCount:     count,
			Prev:           b.lastHash,
			Root:           hashBytes(b.frames...),
			SnapshotHash:   snapHash,
		}
		decision.DecisionSig = signBytes(pair.Signing, decisionCanonical(decision))
		frame, err := b.engine.sealRecord(b.region, b.next, b.txn.Tenant, decision)
		if err != nil {
			return CommitReceipt{}, err
		}
		if err := b.engine.writeSlot(&b.trace, PhaseDecision, b.region, b.next, frame); err != nil {
			return CommitReceipt{}, fmt.Errorf("%w: decision: %v", ErrIO, err)
		}
		b.frames = append(b.frames, frame)
		b.lastHash = hashBytes(frame)
		b.next++
		b.phase = 2
	}

	if b.phase == 2 {
		pair := b.engine.keys[b.txn.Tenant]
		decisionFrame := b.frames[len(b.frames)-1]
		receipt := record{
			Kind:           recordReceipt,
			Deleted:        b.deleted,
			Tenant:         b.txn.Tenant,
			LeaseEpoch:     b.txn.LeaseEpoch,
			LeaseID:        b.txn.LeaseID,
			Generation:     b.txn.BaseGeneration + 1,
			BaseGeneration: b.txn.BaseGeneration,
			Txn:            b.txn.ID,
			TxnNonce:       b.txn.Nonce,
			TxnSignature:   b.txn.Signature,
			Index:          count + 2,
			BlockCount:     count,
			Prev:           b.lastHash,
			Root:           hashBytes(b.frames...),
			SnapshotHash:   snapHash,
			DecisionHash:   hashBytes(decisionFrame),
		}
		receipt.ReceiptSig = signBytes(pair.Signing, receiptCanonical(receipt))
		frame, err := b.engine.sealRecord(b.region, b.next, b.txn.Tenant, receipt)
		if err != nil {
			return CommitReceipt{}, err
		}
		if err := b.engine.writeSlot(&b.trace, PhaseAck, b.region, b.next, frame); err != nil {
			return CommitReceipt{}, fmt.Errorf("%w: receipt: %v", ErrIO, err)
		}
		b.frames = append(b.frames, frame)
		b.lastHash = hashBytes(frame)
		b.next++
		b.phase = 3
	}

	info := b.receiptInfo()
	b.done = true
	b.engine.openBatch = nil
	return info, nil
}

func (b *Batch) receiptInfo() CommitReceipt {
	return CommitReceipt{
		Tenant:       b.txn.Tenant,
		Lease:        b.lease,
		Generation:   b.txn.BaseGeneration + 1,
		Txn:          b.txn.ID,
		BlockCount:   len(b.blocks),
		Deleted:      b.deleted,
		SnapshotHash: snapshotHash(b.blocks),
		Region:       b.region,
		FirstSlot:    b.start,
		FrameCount:   len(b.frames),
	}
}

// Abort makes a batch invisible. No destructive writes are needed because the
// next Begin overwrites the inactive lane from its first slot.
func (b *Batch) Abort() {
	b.engine.mu.Lock()
	defer b.engine.mu.Unlock()
	if b.engine.openBatch == b {
		b.engine.openBatch = nil
	}
	b.done = true
}

// Delete commits a signed tombstone under the supplied transaction.
func (e *Engine) Delete(txn SignedTxn) (CommitReceipt, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	batch, err := e.beginLocked(txn, true, nil)
	if err != nil {
		return CommitReceipt{}, err
	}
	receipt, err := batch.commitLocked()
	if err != nil {
		batch.done = true
		e.openBatch = nil
	}
	return receipt, err
}

func cloneBlock(block Block) Block {
	block.Data = append([]byte(nil), block.Data...)
	return block
}

type decodedFrame struct {
	region Region
	slot   int
	raw    []byte
	rec    record
}

type laneScan struct {
	frames     []decodedFrame
	headers    []decodedFrame
	candidates []commitCandidate
	readErr    error
}

type commitCandidate struct {
	frames   []decodedFrame
	header   record
	prepare  record
	decision record
	receipt  record
	txn      SignedTxn
	snapshot *Snapshot
	region   Region
	first    int
}

func (e *Engine) scanLane(tenant TenantID, phase Phase, trace *[]Event) (laneScan, error) {
	lane, err := e.cfg.Layout.lane(tenant)
	if err != nil {
		return laneScan{}, err
	}
	var scan laneScan
	bySlot := make([][]decodedFrame, 2)
	for region := RegionA; region <= RegionB; region++ {
		bySlot[region] = make([]decodedFrame, lane.Slots)
		for slot := lane.Start; slot < lane.Start+lane.Slots; slot++ {
			frame, err := e.readSlot(trace, phase, region, slot)
			if err != nil {
				if scan.readErr == nil {
					scan.readErr = err
				}
				continue
			}
			rec, err := e.openRecord(region, slot, tenant, frame)
			if err != nil {
				continue
			}
			if rec.Tenant != tenant {
				continue
			}
			decoded := decodedFrame{region: region, slot: slot, raw: frame, rec: rec}
			scan.frames = append(scan.frames, decoded)
			bySlot[region][slot-lane.Start] = decoded
			if rec.Kind == recordHeader {
				scan.headers = append(scan.headers, decoded)
			}
		}
	}
	if scan.readErr != nil {
		return scan, fmt.Errorf("%w: %v", ErrIO, scan.readErr)
	}

	seenTxn := make(map[TxID]commitCandidate)
	for _, header := range scan.headers {
		candidate, err := e.validateChain(tenant, lane, header, bySlot, true)
		if err != nil {
			// A partial or unacknowledged chain is expected after a crash and
			// is deliberately invisible.
			continue
		}
		if previous, ok := seenTxn[candidate.txn.ID]; ok {
			if previous.receipt.Root != candidate.receipt.Root || previous.region != candidate.region || previous.first != candidate.first {
				return laneScan{}, ErrAmbiguous
			}
			continue
		}
		seenTxn[candidate.txn.ID] = candidate
		scan.candidates = append(scan.candidates, candidate)
	}
	return scan, nil
}

func (e *Engine) validateChain(tenant TenantID, lane LaneSpec, header decodedFrame, bySlot [][]decodedFrame, requireReceipt bool) (commitCandidate, error) {
	if header.rec.Kind != recordHeader {
		return commitCandidate{}, fmt.Errorf("not a header")
	}
	if header.slot < lane.Start || header.slot >= lane.Start+lane.Slots {
		return commitCandidate{}, fmt.Errorf("header outside lane")
	}
	txn := SignedTxn{
		ID:             header.rec.Txn,
		Tenant:         header.rec.Tenant,
		LeaseEpoch:     header.rec.LeaseEpoch,
		LeaseID:        header.rec.LeaseID,
		BaseGeneration: header.rec.BaseGeneration,
		Nonce:          header.rec.TxnNonce,
		Signature:      header.rec.TxnSignature,
	}
	if txn.Tenant != tenant || !e.keys.VerifyTransaction(txn) {
		return commitCandidate{}, ErrSignature
	}
	if header.rec.Generation != header.rec.BaseGeneration+1 {
		return commitCandidate{}, fmt.Errorf("generation gap")
	}

	var prepare, decision, receipt decodedFrame
	var havePrepare, haveDecision, haveReceipt bool
	for _, frame := range bySlot[header.region] {
		if frame.rec.Txn != header.rec.Txn {
			continue
		}
		switch frame.rec.Kind {
		case recordPrepare:
			if havePrepare {
				return commitCandidate{}, fmt.Errorf("duplicate prepare")
			}
			prepare, havePrepare = frame, true
		case recordDecision:
			if haveDecision {
				return commitCandidate{}, fmt.Errorf("duplicate decision")
			}
			decision, haveDecision = frame, true
		case recordReceipt:
			if haveReceipt {
				return commitCandidate{}, fmt.Errorf("duplicate receipt")
			}
			receipt, haveReceipt = frame, true
		}
	}
	if !havePrepare || !haveDecision {
		return commitCandidate{}, fmt.Errorf("incomplete chain")
	}
	if requireReceipt && !haveReceipt {
		return commitCandidate{}, fmt.Errorf("missing receipt")
	}
	count := prepare.rec.BlockCount
	if count > uint32(lane.Slots) {
		return commitCandidate{}, fmt.Errorf("block count outside lane")
	}
	lastOffset := 2 + int(count)
	if requireReceipt {
		lastOffset++
	}
	lastSlot := header.slot + lastOffset
	if lastSlot >= lane.Start+lane.Slots || prepare.slot != header.slot+1+int(count) ||
		decision.slot != prepare.slot+1 || (requireReceipt && receipt.slot != decision.slot+1) {
		return commitCandidate{}, fmt.Errorf("non-contiguous chain")
	}
	if !requireReceipt && receipt.slot != 0 {
		return commitCandidate{}, fmt.Errorf("unexpected receipt")
	}

	frames := make([]decodedFrame, 0, int(count)+4)
	frames = append(frames, header)
	blocks := make([]Block, 0, count)
	blockIDs := make(map[BlockID]struct{}, count)
	for i := uint32(0); i < count; i++ {
		frame := bySlot[header.region][header.slot+1+int(i)-lane.Start]
		if frame.rec.Kind != recordBlock || frame.rec.Index != i {
			return commitCandidate{}, fmt.Errorf("missing block %d", i)
		}
		if frame.rec.Block.Tenant != tenant {
			return commitCandidate{}, ErrBlockTenant
		}
		if _, exists := blockIDs[frame.rec.Block.ID]; exists {
			return commitCandidate{}, ErrDuplicateID
		}
		blockIDs[frame.rec.Block.ID] = struct{}{}
		frames = append(frames, frame)
		blocks = append(blocks, cloneBlock(frame.rec.Block))
	}
	frames = append(frames, prepare, decision)
	if requireReceipt {
		frames = append(frames, receipt)
	}
	for i := 1; i < len(frames); i++ {
		if frames[i].rec.Prev != hashBytes(frames[i-1].raw) {
			return commitCandidate{}, fmt.Errorf("broken frame chain")
		}
	}
	for i := 1; i < len(frames); i++ {
		if !sameMeta(frames[0].rec, frames[i].rec) {
			return commitCandidate{}, fmt.Errorf("metadata mismatch")
		}
	}
	if prepare.rec.Index != count || decision.rec.Index != count+1 || (requireReceipt && receipt.rec.Index != count+2) {
		return commitCandidate{}, fmt.Errorf("trailer index mismatch")
	}
	if prepare.rec.Root != hashFrameBytes(frames[:1+int(count)]) {
		return commitCandidate{}, fmt.Errorf("prepare root mismatch")
	}
	if decision.rec.Root != hashFrameBytes(frames[:2+int(count)]) {
		return commitCandidate{}, fmt.Errorf("decision root mismatch")
	}
	if requireReceipt && receipt.rec.Root != hashFrameBytes(frames[:3+int(count)]) {
		return commitCandidate{}, fmt.Errorf("receipt root mismatch")
	}
	snapHash := snapshotHash(blocks)
	if prepare.rec.SnapshotHash != snapHash || decision.rec.SnapshotHash != snapHash ||
		(requireReceipt && receipt.rec.SnapshotHash != snapHash) {
		return commitCandidate{}, fmt.Errorf("snapshot hash mismatch")
	}
	if prepare.rec.BlockCount != decision.rec.BlockCount ||
		(requireReceipt && receipt.rec.BlockCount != prepare.rec.BlockCount) {
		return commitCandidate{}, fmt.Errorf("block count mismatch")
	}
	if prepare.rec.Deleted != header.rec.Deleted || decision.rec.Deleted != header.rec.Deleted ||
		(requireReceipt && receipt.rec.Deleted != header.rec.Deleted) {
		return commitCandidate{}, fmt.Errorf("delete flag mismatch")
	}
	if header.rec.Deleted && count != 0 {
		return commitCandidate{}, fmt.Errorf("tombstone has blocks")
	}
	pair, ok := e.keys.pair(tenant)
	if !ok {
		return commitCandidate{}, ErrKey
	}
	wantDecision := signBytes(pair.Signing, decisionCanonical(decision.rec))
	if !hmac.Equal(decision.rec.DecisionSig[:], wantDecision[:]) {
		return commitCandidate{}, ErrSignature
	}
	if requireReceipt {
		if receipt.rec.DecisionHash != hashBytes(decision.raw) {
			return commitCandidate{}, fmt.Errorf("decision hash mismatch")
		}
		wantReceipt := signBytes(pair.Signing, receiptCanonical(receipt.rec))
		if !hmac.Equal(receipt.rec.ReceiptSig[:], wantReceipt[:]) {
			return commitCandidate{}, ErrSignature
		}
	}
	snapshot := &Snapshot{
		Tenant:     tenant,
		Lease:      Lease{Tenant: tenant, Epoch: header.rec.LeaseEpoch, ID: header.rec.LeaseID},
		Generation: header.rec.Generation,
		Txn:        header.rec.Txn,
		Blocks:     blocks,
	}
	if header.rec.Deleted {
		snapshot = nil
	}
	return commitCandidate{
		frames:   frames,
		header:   header.rec,
		prepare:  prepare.rec,
		decision: decision.rec,
		receipt:  receipt.rec,
		txn:      txn,
		snapshot: snapshot,
		region:   header.region,
		first:    header.slot,
	}, nil
}

func sameMeta(a, b record) bool {
	return a.Tenant == b.Tenant &&
		a.LeaseEpoch == b.LeaseEpoch &&
		a.LeaseID == b.LeaseID &&
		a.Generation == b.Generation &&
		a.BaseGeneration == b.BaseGeneration &&
		a.Txn == b.Txn &&
		a.TxnNonce == b.TxnNonce &&
		a.TxnSignature == b.TxnSignature
}

func hashFrameBytes(frames []decodedFrame) Hash {
	chunks := make([][]byte, len(frames))
	for i := range frames {
		chunks[i] = frames[i].raw
	}
	return hashBytes(chunks...)
}

func selectCandidate(candidates []commitCandidate, lease Lease, minGeneration uint64) (*commitCandidate, bool, error) {
	var best *commitCandidate
	staleSeen := false
	for i := range candidates {
		candidate := &candidates[i]
		if candidate.header.Tenant != lease.Tenant ||
			candidate.header.LeaseEpoch != lease.Epoch ||
			candidate.header.LeaseID != lease.ID {
			continue
		}
		if candidate.receipt.Generation < minGeneration {
			staleSeen = true
			continue
		}
		if best == nil || candidate.receipt.Generation > best.receipt.Generation {
			best = candidate
			continue
		}
		if candidate.receipt.Generation == best.receipt.Generation {
			if candidate.txn.ID == best.txn.ID && candidate.receipt.Root == best.receipt.Root {
				continue
			}
			return nil, staleSeen, ErrAmbiguous
		}
	}
	return best, best == nil && staleSeen, nil
}

func (c commitCandidate) publicReceipt() CommitReceipt {
	return CommitReceipt{
		Tenant:       c.header.Tenant,
		Lease:        Lease{Tenant: c.header.Tenant, Epoch: c.header.LeaseEpoch, ID: c.header.LeaseID},
		Generation:   c.header.Generation,
		Txn:          c.header.Txn,
		BlockCount:   int(c.prepare.BlockCount),
		Deleted:      c.header.Deleted,
		SnapshotHash: c.prepare.SnapshotHash,
		Region:       c.region,
		FirstSlot:    c.first,
		FrameCount:   len(c.frames),
	}
}
