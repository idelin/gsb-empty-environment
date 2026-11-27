package mvcc

import (
	"errors"
	"sync"
)

// ErrVisibilityConflict 表示试图在非 ALL_VISIBLE 页上置 ALL_FROZEN。
var ErrVisibilityConflict = errors.New("mvcc: ALL_FROZEN requires ALL_VISIBLE")

// VisibilityMap 是按块号维护的可见性映射（VM），并发安全。
type VisibilityMap struct {
	mu      sync.Mutex
	visible map[uint32]bool
	frozen  map[uint32]bool
}

// NewVisibilityMap 创建空的可见性映射。
func NewVisibilityMap() *VisibilityMap {
	return &VisibilityMap{
		visible: make(map[uint32]bool),
		frozen:  make(map[uint32]bool),
	}
}

// Set 更新某页的两个 VM 位；ALL_FROZEN 必须以 ALL_VISIBLE 为前提。
func (vm *VisibilityMap) Set(blockNo uint32, allVisible, allFrozen bool) error {
	if allFrozen && !allVisible {
		return ErrVisibilityConflict
	}
	vm.mu.Lock()
	defer vm.mu.Unlock()
	vm.visible[blockNo] = allVisible
	vm.frozen[blockNo] = allFrozen
	return nil
}

// IsAllVisible 报告该页是否置有 ALL_VISIBLE。
func (vm *VisibilityMap) IsAllVisible(blockNo uint32) bool {
	vm.mu.Lock()
	defer vm.mu.Unlock()
	return vm.visible[blockNo]
}

// IsAllFrozen 报告该页是否置有 ALL_FROZEN。
func (vm *VisibilityMap) IsAllFrozen(blockNo uint32) bool {
	vm.mu.Lock()
	defer vm.mu.Unlock()
	return vm.frozen[blockNo]
}

// Clear 清除某页的全部 VM 位。
func (vm *VisibilityMap) Clear(blockNo uint32) {
	vm.mu.Lock()
	defer vm.mu.Unlock()
	delete(vm.visible, blockNo)
	delete(vm.frozen, blockNo)
}

// ApplyVacuum 把一次页清理结果写入 VM。
func (vm *VisibilityMap) ApplyVacuum(blockNo uint32, result VacuumResult) error {
	return vm.Set(blockNo, result.AllVisible, result.AllFrozen)
}
