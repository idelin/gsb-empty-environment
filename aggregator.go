package aggregator

import (
	"fmt"
	"sync"
)

// Sink 是结果下游。Publish 语义为幂等投递：
// 同一 Result.ID 可能被重复投递（恢复重发），接收方必须去重。
type Sink interface {
	Publish(res Result) error
}

// Aggregator 累计输入事件，按阈值结算出结果并发布。
//
// 不变式：
//   - 结果 ID 由持久化的 EmitSeq 派生，同一逻辑结果永远同一 ID；
//   - 结果先进入 Pending 并落检查点，再对外发布（发布与确认分离）；
//   - 确认（Ack）从 Pending 移除并再次落检查点；
//   - 恢复时重放 Pending 中的原 ID，由幂等接收方去重。
type Aggregator struct {
	mu        sync.Mutex
	store     Store
	sink      Sink
	emitEvery int64
	cp        Checkpoint
	restored  bool
}

// Open 打开（或恢复）一个聚合器。
// 若存储中已有检查点，则恢复计算状态；待确认结果留在 Pending 中，
// 由调用方调用 PublishPending 重发。
func Open(store Store, sink Sink, emitEvery int64) (*Aggregator, error) {
	if emitEvery <= 0 {
		emitEvery = 1
	}
	a := &Aggregator{store: store, sink: sink, emitEvery: emitEvery}
	cp, err := store.Load()
	switch err {
	case nil:
		a.cp = cp
		a.restored = true
	case ErrNoCheckpoint:
		a.cp = newCheckpoint()
	default:
		return nil, err
	}
	return a, nil
}

// Add 应用一条输入事件。每 emitEvery 条事件自动结算一次。
func (a *Aggregator) Add(key string, delta int64) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	next := a.cp.clone()
	next.Sums[key] += delta
	next.InputSeq++

	if next.InputSeq%a.emitEvery == 0 {
		var err error
		next, err = a.emitLocked(next)
		if err != nil {
			return err
		}
	}
	if err := a.store.Save(next); err != nil {
		return err
	}
	a.cp = next
	return nil
}

// Flush 强制结算所有 key 的未发布增量。
func (a *Aggregator) Flush() error {
	a.mu.Lock()
	defer a.mu.Unlock()

	next, err := a.emitLocked(a.cp.clone())
	if err != nil {
		return err
	}
	if err := a.store.Save(next); err != nil {
		return err
	}
	a.cp = next
	return nil
}

// emitLocked 把每个 key 的增量（累计值 - 已结算基线）结算为结果，
// 追加到 Pending。结果 ID 来自持久化的 EmitSeq，先落盘再发布。
func (a *Aggregator) emitLocked(next Checkpoint) (Checkpoint, error) {
	for key, sum := range next.Sums {
		delta := sum - next.Emitted[key]
		if delta == 0 {
			continue
		}
		next.EmitSeq++
		next.Pending = append(next.Pending, Result{
			ID:    fmt.Sprintf("res-%d", next.EmitSeq),
			Key:   key,
			Value: delta,
		})
		next.Emitted[key] = sum
	}
	return next, nil
}

// PublishPending 把 Pending 中所有未确认结果投递给下游。
// 恢复后调用即可原样重发，ID 不变。发布失败时中断并返回错误，
// 剩余结果留待下次重试。
func (a *Aggregator) PublishPending() error {
	a.mu.Lock()
	pending := append([]Result(nil), a.cp.Pending...)
	a.mu.Unlock()

	for _, res := range pending {
		if err := a.sink.Publish(res); err != nil {
			return fmt.Errorf("publish %s: %w", res.ID, err)
		}
	}
	return nil
}

// Ack 确认一条结果：从 Pending 移除并落检查点。
// 对未知或已确认的 ID 重复 Ack 是安全的（幂等）。
func (a *Aggregator) Ack(id string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	next := a.cp.clone()
	kept := next.Pending[:0]
	found := false
	for _, res := range next.Pending {
		if res.ID == id {
			found = true
			continue
		}
		kept = append(kept, res)
	}
	if !found {
		return nil
	}
	next.Pending = kept
	next.Confirmed++
	if err := a.store.Save(next); err != nil {
		return err
	}
	a.cp = next
	return nil
}

// Snapshot 返回当前检查点副本，供测试与演示核对。
func (a *Aggregator) Snapshot() Checkpoint {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cp.clone()
}
