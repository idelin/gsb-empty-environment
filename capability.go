package netsec

import (
	"errors"
	"fmt"
	"sync"
)

// 能力复核失败时返回的哨兵错误,调用方可用 errors.Is 精确区分拒绝原因。
var (
	// ErrCapabilityNotRegistered 表示该 (来源, 帧类型) 从未登记过此绑定。
	ErrCapabilityNotRegistered = errors.New("binding capability is not registered for this effective origin and frame type")
	// ErrOpaqueOrigin 表示调用帧当前处于 data/opaque 等不透明来源,
	// 不透明来源默认无权,能力表也拒绝为其登记能力。
	ErrOpaqueOrigin = errors.New("opaque origins are denied by default")
	// ErrStaleGeneration 表示回调绑定于旧导航代次,帧已经发生导航。
	ErrStaleGeneration = errors.New("callback is bound to a stale navigation generation")
	// ErrFrameMismatch 表示回调被拿到其它帧上调用。
	ErrFrameMismatch = errors.New("callback was invoked from a different frame")
)

// NativeFunc 是宿主注入页面脚本环境的原生函数。
// 只有通过能力表复核的调用才会触达 NativeFunc;越权调用在闸门处即被拒绝。
type NativeFunc func(args []any) (any, error)

// capabilityKey 是能力登记的复合键:绑定名 + 有效来源 + 帧类型。
type capabilityKey struct {
	binding   string
	origin    Origin
	frameType FrameType
}

// CapabilityTable 是可导入的绑定能力表。
//
// 能力按 (有效来源, 帧类型) 登记:同一名绑定在不同来源或不同帧类型下
// 可以映射到不同的原生函数,也可以只对其中一部分组合开放。
// 零值 CapabilityTable 不可直接使用,请用 NewCapabilityTable 构造。
type CapabilityTable struct {
	mu           sync.RWMutex
	capabilities map[capabilityKey]NativeFunc
}

// NewCapabilityTable 创建空能力表。
func NewCapabilityTable() *CapabilityTable {
	return &CapabilityTable{
		capabilities: make(map[capabilityKey]NativeFunc),
	}
}

// Grant 按有效来源与帧类型登记一个绑定能力。
//
// 拒绝为不透明来源登记能力,从登记侧保证 data: 等文档永远拿不到授权;
// fn 为 nil 时返回错误而不是存入空入口。
func (t *CapabilityTable) Grant(binding string, origin Origin, frameType FrameType, fn NativeFunc) error {
	if binding == "" {
		return fmt.Errorf("capability grant: binding name must not be empty")
	}
	if fn == nil {
		return fmt.Errorf("capability grant %q: native function must not be nil", binding)
	}
	if origin.IsOpaque() {
		return fmt.Errorf("capability grant %q: %w", binding, ErrOpaqueOrigin)
	}
	key := capabilityKey{binding: binding, origin: origin, frameType: frameType}
	t.mu.Lock()
	t.capabilities[key] = fn
	t.mu.Unlock()
	return nil
}

// Revoke 撤销此前登记的能力;未登记的组合视为空操作。
func (t *CapabilityTable) Revoke(binding string, origin Origin, frameType FrameType) {
	key := capabilityKey{binding: binding, origin: origin, frameType: frameType}
	t.mu.Lock()
	delete(t.capabilities, key)
	t.mu.Unlock()
}

// Check 在不执行原生函数的前提下报告当前帧状态是否允许调用指定绑定。
// 返回的错误为 nil 表示放行,否则为哨兵包装错误,可用 errors.Is 判定。
func (t *CapabilityTable) Check(binding string, frame *Frame) error {
	if frame == nil {
		return fmt.Errorf("capability check %q: frame must not be nil", binding)
	}
	snap := frame.Snapshot()
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.checkLocked(binding, snap)
}

// Invoke 代表宿主原生绑定层执行一次调用。
//
// 复核与派发都以帧当前快照为准:来源、帧类型不匹配或处于不透明来源时,
// 在闸门处直接返回错误,保证越权调用绝不会触达 NativeFunc。
func (t *CapabilityTable) Invoke(binding string, frame *Frame, args []any) (any, error) {
	if frame == nil {
		return nil, fmt.Errorf("capability invoke %q: frame must not be nil", binding)
	}
	snap := frame.Snapshot()

	t.mu.RLock()
	fn, err := t.lookupLocked(binding, snap)
	t.mu.RUnlock()
	if err != nil {
		return nil, err
	}
	return fn(args)
}

// Bind 把一个绑定固化到调用时刻的 (帧, 导航代次) 上,返回代次受限的回调。
//
// Bind 本身也做一次能力复核:无权上下文连回调对象都拿不到。
func (t *CapabilityTable) Bind(binding string, frame *Frame) (*Callback, error) {
	if frame == nil {
		return nil, fmt.Errorf("capability bind %q: frame must not be nil", binding)
	}
	snap := frame.Snapshot()
	t.mu.RLock()
	err := t.checkLocked(binding, snap)
	t.mu.RUnlock()
	if err != nil {
		return nil, err
	}
	return &Callback{
		table:      t,
		binding:    binding,
		frameID:    snap.FrameID,
		generation: snap.Generation,
	}, nil
}

// lookupLocked 在持锁状态下取出原生函数并完成来源/帧类型复核。
func (t *CapabilityTable) lookupLocked(binding string, snap FrameSnapshot) (NativeFunc, error) {
	if err := t.checkLocked(binding, snap); err != nil {
		return nil, err
	}
	key := capabilityKey{binding: binding, origin: snap.Origin, frameType: snap.Type}
	return t.capabilities[key], nil
}

// checkLocked 在持锁状态下复核单次调用的静态授权。
// 代次复核由 Callback 在静态授权之外额外完成。
func (t *CapabilityTable) checkLocked(binding string, snap FrameSnapshot) error {
	if snap.Origin.IsOpaque() {
		return fmt.Errorf("capability check %q on frame %q: %w", binding, snap.FrameID, ErrOpaqueOrigin)
	}
	key := capabilityKey{binding: binding, origin: snap.Origin, frameType: snap.Type}
	if _, ok := t.capabilities[key]; !ok {
		return fmt.Errorf("capability check %q on frame %q (%s): %w",
			binding, snap.FrameID, snap.Origin, ErrCapabilityNotRegistered)
	}
	return nil
}
