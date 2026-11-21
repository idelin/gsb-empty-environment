package recovery

import "fmt"

// RecoveryResult contains the one selected acknowledged snapshot (if any) and
// the complete public I/O trace. A deleted receipt yields Deleted=true and a
// nil Snapshot.
type RecoveryResult struct {
	Snapshot         *Snapshot
	Receipt          *CommitReceipt
	Deleted          bool
	RollbackDetected bool
	Trace            []Event
}

// ApplyFunc receives exactly one selected acknowledged result. The receipt's
// Txn field is the stable idempotency key for the host's apply operation. The
// callback runs while the engine lock is held and must not call back into the
// same Engine.
type ApplyFunc func(RecoveryResult) error

// Recover scans only the tenant's fixed lane in both regions, validates
// authenticated receipt chains, and selects the newest acknowledged generation
// for the exact lease. It performs no writes and always executes the same
// bounded 2*LaneSlots read plan, even when an early frame is sufficient.
func (e *Engine) Recover(req RecoveryRequest) (RecoveryResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.recoverLocked(req)
}

func (e *Engine) recoverLocked(req RecoveryRequest) (RecoveryResult, error) {
	if err := req.Lease.validate(req.Tenant); err != nil {
		return RecoveryResult{}, err
	}
	var trace []Event
	scan, err := e.scanLane(req.Tenant, PhaseRecoverScan, &trace)
	result := RecoveryResult{Trace: trace}
	if err != nil {
		return result, err
	}
	best, stale, err := selectCandidate(scan.candidates, req.Lease, req.MinGeneration)
	if err != nil {
		result.RollbackDetected = stale
		return result, err
	}
	result.RollbackDetected = stale
	if best == nil {
		return result, nil
	}
	receipt := best.publicReceipt()
	result.Receipt = &receipt
	result.Deleted = best.header.Deleted
	if best.snapshot != nil {
		result.Snapshot = cloneSnapshot(best.snapshot)
	}
	return result, nil
}

// RecoverAndApply invokes apply once for the selected acknowledged result. No
// apply occurs when the lane contains no acknowledged receipt for the lease.
// Within one Engine instance, replaying the same or an older generation under
// the same lease is suppressed. The host can make this exactly-once across
// process restarts by committing its own state and receipt.Txn atomically.
func (e *Engine) RecoverAndApply(req RecoveryRequest, apply ApplyFunc) (RecoveryResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if apply == nil {
		return RecoveryResult{}, fmt.Errorf("%w: nil apply callback", ErrConfig)
	}
	result, err := e.recoverLocked(req)
	if err != nil {
		return result, err
	}
	if result.Receipt == nil {
		return result, nil
	}
	if previous, ok := e.applied[req.Tenant]; ok &&
		previous.Lease == result.Receipt.Lease &&
		result.Receipt.Generation <= previous.Generation {
		return result, nil
	}
	if err := apply(result); err != nil {
		return result, fmt.Errorf("recovery: apply: %w", err)
	}
	e.applied[req.Tenant] = *result.Receipt
	return result, nil
}

// Resolve handles a transaction whose outcome was not acknowledged. If a
// complete transaction-signed decision exists, Resolve durably writes the ack
// receipt and returns the result. Otherwise the partial batch is reported as
// rolled back and remains invisible. A different token for the same transaction
// id is rejected.
func (e *Engine) Resolve(txn SignedTxn) (ResolveResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := txn.validate(); err != nil {
		return ResolveResult{}, err
	}
	if !e.keys.VerifyTransaction(txn) {
		return ResolveResult{}, ErrSignature
	}
	if err := txn.lease().validate(txn.Tenant); err != nil {
		return ResolveResult{}, err
	}
	lane, err := e.cfg.Layout.lane(txn.Tenant)
	if err != nil {
		return ResolveResult{}, err
	}
	var trace []Event
	scan, err := e.scanLane(txn.Tenant, PhaseResolveScan, &trace)
	if err != nil {
		return ResolveResult{}, err
	}
	for i := range scan.candidates {
		candidate := &scan.candidates[i]
		if candidate.txn.ID != txn.ID {
			continue
		}
		if !sameSignedTxn(candidate.txn, txn) {
			return ResolveResult{}, ErrTxnMismatch
		}
		e.finishOpenBatch(txn)
		receipt := candidate.publicReceipt()
		return ResolveResult{
			State:    ResolveCommitted,
			Snapshot: cloneSnapshot(candidate.snapshot),
			Receipt:  &receipt,
		}, nil
	}

	for _, header := range scan.headers {
		if header.rec.Txn != txn.ID {
			continue
		}
		bySlot := make([][]decodedFrame, 2)
		for region := RegionA; region <= RegionB; region++ {
			bySlot[region] = make([]decodedFrame, lane.Slots)
			for _, frame := range scan.frames {
				if frame.region == region {
					bySlot[region][frame.slot-lane.Start] = frame
				}
			}
		}
		candidate, err := e.validateChain(txn.Tenant, lane, header, bySlot, false)
		if err != nil {
			continue
		}
		if !sameSignedTxn(candidate.txn, txn) {
			return ResolveResult{}, ErrTxnMismatch
		}
		count := uint32(candidate.prepare.BlockCount)
		receiptSlot := header.slot + 1 + int(count) + 2
		if receiptSlot >= lane.Start+lane.Slots {
			return ResolveResult{}, ErrCapacity
		}
		decision := candidate.frames[len(candidate.frames)-1]
		pair := e.keys[txn.Tenant]
		receipt := record{
			Kind:           recordReceipt,
			Deleted:        header.rec.Deleted,
			Tenant:         txn.Tenant,
			LeaseEpoch:     txn.LeaseEpoch,
			LeaseID:        txn.LeaseID,
			Generation:     txn.BaseGeneration + 1,
			BaseGeneration: txn.BaseGeneration,
			Txn:            txn.ID,
			TxnNonce:       txn.Nonce,
			TxnSignature:   txn.Signature,
			Index:          count + 2,
			BlockCount:     count,
			Prev:           hashBytes(decision.raw),
			Root:           hashFrameBytes(candidate.frames),
			SnapshotHash:   candidate.prepare.SnapshotHash,
			DecisionHash:   hashBytes(decision.raw),
		}
		receipt.ReceiptSig = signBytes(pair.Signing, receiptCanonical(receipt))
		frame, err := e.sealRecord(candidate.region, receiptSlot, txn.Tenant, receipt)
		if err != nil {
			return ResolveResult{}, err
		}
		if err := e.writeSlot(nil, PhaseResolveAck, candidate.region, receiptSlot, frame); err != nil {
			return ResolveResult{}, fmt.Errorf("%w: resolve receipt: %v", ErrIO, err)
		}
		candidate.receipt = receipt
		candidate.frames = append(candidate.frames, decodedFrame{
			region: candidate.region,
			slot:   receiptSlot,
			raw:    frame,
			rec:    receipt,
		})
		e.finishOpenBatch(txn)
		info := candidate.publicReceipt()
		return ResolveResult{
			State:    ResolveRetrieved,
			Snapshot: cloneSnapshot(candidate.snapshot),
			Receipt:  &info,
		}, nil
	}
	return ResolveResult{State: ResolveRolledBack}, nil
}

func (e *Engine) finishOpenBatch(txn SignedTxn) {
	if e.openBatch != nil && sameSignedTxn(e.openBatch.txn, txn) {
		e.openBatch.done = true
		e.openBatch = nil
	}
}

func sameSignedTxn(a, b SignedTxn) bool {
	return a.ID == b.ID &&
		a.Tenant == b.Tenant &&
		a.LeaseEpoch == b.LeaseEpoch &&
		a.LeaseID == b.LeaseID &&
		a.BaseGeneration == b.BaseGeneration &&
		a.Nonce == b.Nonce &&
		a.Signature == b.Signature
}

func cloneSnapshot(snapshot *Snapshot) *Snapshot {
	if snapshot == nil {
		return nil
	}
	out := *snapshot
	out.Blocks = make([]Block, len(snapshot.Blocks))
	for i, block := range snapshot.Blocks {
		out.Blocks[i] = cloneBlock(block)
	}
	return &out
}
