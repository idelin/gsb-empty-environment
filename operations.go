package netsec

import "fmt"

// BeginSensitiveOperation 请求在资源上开始一次敏感操作。
//
// 资源受限（含传递依赖失效）时拒绝新增操作，返回 ErrResourceRestricted。
// opID 为空时自动生成；返回的 Operation 记录完整生命周期。
func (idx *DependencyIndex) BeginSensitiveOperation(resourceID, opID string) (*Operation, error) {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	entry := idx.resources[resourceID]
	if entry == nil {
		return nil, wrapID(ErrResourceNotFound, resourceID)
	}
	if entry.restricted {
		return nil, fmt.Errorf("%w: %s (causes=%v)", ErrResourceRestricted, resourceID, entry.causes)
	}
	if opID == "" {
		idx.opSeq++
		opID = fmt.Sprintf("op-%d", idx.opSeq)
	}
	if _, exists := idx.operations[opID]; exists {
		return nil, fmt.Errorf("netsec: operation id already used: %s", opID)
	}
	op := &Operation{
		ID:         opID,
		ResourceID: resourceID,
		StartedAt:  idx.clock.Now(),
		state:      OpRunning,
	}
	idx.operations[opID] = op
	entry.runningOps[opID] = op
	return cloneOperation(op), nil
}

// CompleteOperation 将在途操作标记为显式完成。已终止操作返回
// ErrOperationTerminated；被 abort 中止的操作不能再完成。
func (idx *DependencyIndex) CompleteOperation(opID string) error {
	return idx.terminateOperation(opID, OpCompleted)
}

// FailOperation 将在途操作标记为显式失败，终止语义与 Complete 相同。
func (idx *DependencyIndex) FailOperation(opID string) error {
	return idx.terminateOperation(opID, OpFailed)
}

// Operation 返回操作快照；不存在返回 nil。
func (idx *DependencyIndex) Operation(opID string) *Operation {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	if op := idx.operations[opID]; op != nil {
		return cloneOperation(op)
	}
	return nil
}

// terminateOperation 是 completed/failed 的统一显式终止入口。
func (idx *DependencyIndex) terminateOperation(opID string, state OperationState) error {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	op := idx.operations[opID]
	if op == nil {
		return wrapID(ErrOperationNotFound, opID)
	}
	if op.Terminated() {
		return fmt.Errorf("%w: %s (%s)", ErrOperationTerminated, opID, op.state)
	}
	op.state = state
	op.terminatedAt = idx.clock.Now()
	if entry := idx.resources[op.ResourceID]; entry != nil {
		delete(entry.runningOps, opID)
	}
	return nil
}
