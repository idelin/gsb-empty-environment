package dma

import "errors"

// DeviceCallback 是设备模型实现的提交入口。参数刻意使用不导出字段的
// *FrozenChain，外部包无法构造，回调只可能收到执行器交出的宿主私有数组。
type DeviceCallback interface {
	Submit(chain *FrozenChain) error
}

// frozenCarrier 由 *FrozenChain 以不导出方法满足，确保除宿主私有数组
// 外没有别的类型能冒充电回调参数的来源。
type frozenCarrier interface {
	isFrozenChain()
}

var _ frozenCarrier = (*FrozenChain)(nil)

// DeviceCallbackFunc 让普通函数满足 DeviceCallback。
type DeviceCallbackFunc func(chain *FrozenChain) error

// Submit 实现 DeviceCallback。
func (fn DeviceCallbackFunc) Submit(chain *FrozenChain) error { return fn(chain) }

// Receipt 是设备完成一个描述符后回传的完成回执，逐项绑定提交代次。
type Receipt struct {
	Generation uint64
	Slot       int
}

// inFlightItem 记录一项在途描述符钉住的页。
type inFlightItem struct {
	generation uint64
	pages      []uint64
}

// Executor 管理描述符提交、在途页钉住与完成回收。它可作为字段嵌入
// 具体设备宿主；零值不可用，请用 NewExecutor 构造。
type Executor struct {
	callback DeviceCallback
	pins     *PinSet

	generation uint64
	// 以完整回执（代次+槽位）为键，旧代次与同槽位新代次互不混淆。
	active map[Receipt]*inFlightItem
}

// NewExecutor 创建执行器。callback 是只接受私有冻结链的设备回调，
// pins 是宿主共享的页钉住计数表。
func NewExecutor(callback DeviceCallback, pins *PinSet) *Executor {
	return &Executor{
		callback: callback,
		pins:     pins,
		active:   make(map[Receipt]*inFlightItem),
	}
}

// Generation 返回当前提交代次（单调递增，复位也不倒退）。
func (e *Executor) Generation() uint64 { return e.generation }

// InFlight 返回当前未完成项数量。
func (e *Executor) InFlight() int { return len(e.active) }

// Submit 为冻结链分配新代次，逐项钉住传输页，然后一次性把私有数组
// 交给设备回调。回调失败时在途页保持钉住，仍可通过 Complete 回收，
// 绝不因回调失败而提前释放。
func (e *Executor) Submit(chain *FrozenChain) (uint64, error) {
	if chain == nil || len(chain.entries) == 0 {
		return 0, ErrBadFlags
	}

	e.generation++
	gen := e.generation
	chain.generation = gen

	for i := range chain.entries {
		entry := chain.entries[i]
		pages := e.pins.PinRange(entry.desc.Addr, entry.desc.Length)
		rcpt := Receipt{Generation: gen, Slot: entry.slot}
		e.active[rcpt] = &inFlightItem{generation: gen, pages: pages}
	}

	if err := e.callback.Submit(chain); err != nil {
		// 显式保留 active：钉住页不释放，等待逐项完成或 Reset。
		return gen, errors.Join(ErrCallbackFailed, err)
	}
	return gen, nil
}

// Complete 处理一个完成回执。只有代次与槽位都匹配当前在途集合时才
// 释放对应页；旧代次、未知槽位或重复完成一律拒绝且不产生回收。
func (e *Executor) Complete(rcpt Receipt) error {
	item, ok := e.active[rcpt]
	if !ok {
		return ErrStaleCompletion
	}
	for _, page := range item.pages {
		e.pins.Release(page)
	}
	delete(e.active, rcpt)
	return nil
}

// Reset 释放所有仍在途的钉住页并清空在途集合。代次继续前进，
// 复位前发出的旧回执此后必然被 Complete 拒绝。
func (e *Executor) Reset() {
	for rcpt, item := range e.active {
		for _, page := range item.pages {
			e.pins.Release(page)
		}
		delete(e.active, rcpt)
	}
	e.generation++
}

// PinSet 是按 4 KiB 页基址维护的钉住引用计数表，保证同一物理页
// 被多个在途描述符共享时，必须等所有描述符完成后才真正解除钉住。
type PinSet struct {
	count map[uint64]int
}

// NewPinSet 创建空钉住表。
func NewPinSet() *PinSet {
	return &PinSet{count: make(map[uint64]int)}
}

// Count 返回某页当前的钉住引用数（测试与诊断用）。
func (p *PinSet) Count(pageBase uint64) int { return p.count[pageBase] }

// PinRange 钉住覆盖 [addr, addr+length) 的全部页，返回该区间覆盖的页基址。
func (p *PinSet) PinRange(addr uint64, length uint32) []uint64 {
	first := addr / PageSize
	last := (addr + uint64(length) - 1) / PageSize
	pages := make([]uint64, 0, last-first+1)
	for base := first; base <= last; base++ {
		pageBase := base * PageSize
		// 每个钉住者都记录自己涉及的页，完成时按自身列表逐页释放；
		// 共享页靠引用计数在最后一个完成者处才真正解除钉住。
		pages = append(pages, pageBase)
		p.count[pageBase]++
	}
	return pages
}

// Release 把某页引用计数减一；计数归零时移除钉住记录。
func (p *PinSet) Release(pageBase uint64) {
	if p.count[pageBase] <= 1 {
		delete(p.count, pageBase)
		return
	}
	p.count[pageBase]--
}
