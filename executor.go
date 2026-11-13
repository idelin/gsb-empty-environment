package netsec

import (
	"errors"
	"fmt"
	"sync"
)

var (
	// ErrStaleGeneration 表示完成回执指向不存在或已结束的提交代次，旧代次不得回收任何页。
	ErrStaleGeneration = errors.New("netsec: completion for stale or unknown generation")
	// ErrDuplicateCompletion 表示同一项的重复完成回执，不得二次回收。
	ErrDuplicateCompletion = errors.New("netsec: duplicate completion for descriptor")
)

// SubmitFunc 是设备回调：只接受宿主私有的冻结链，一次提交。
// 实现不得在执行时回读客户机可写内存（反面示例见 RereadAdapter）。
type SubmitFunc func(chain *FrozenChain) error

// submission 跟踪一代提交的在途状态。
type submission struct {
	done      []bool
	remaining int
}

// Executor 串起冻结与提交，并按代次管理在途页与完成回执。
type Executor struct {
	freezer  *Freezer
	submit   SubmitFunc
	mu       sync.Mutex
	nextGen  uint64
	inflight map[uint64]*submission
}

func NewExecutor(freezer *Freezer, submit SubmitFunc) *Executor {
	return &Executor{freezer: freezer, submit: submit, inflight: make(map[uint64]*submission)}
}

// Submit 冻结描述符环并一次性提交给设备回调。
// 回调失败时仅丢弃本次提交，绝不释放此前仍在途的页。
// 返回本次提交的代次。
func (e *Executor) Submit(ringBase uint64, head uint16) (uint64, error) {
	chain, err := e.freezer.Freeze(ringBase, head)
	if err != nil {
		return 0, err
	}
	e.mu.Lock()
	e.nextGen++
	gen := e.nextGen
	chain.gen = gen
	e.mu.Unlock()
	if err := e.submit(chain); err != nil {
		return 0, fmt.Errorf("netsec: device submit: %w", err)
	}
	e.mu.Lock()
	e.inflight[gen] = &submission{done: make([]bool, chain.Len()), remaining: chain.Len()}
	e.mu.Unlock()
	return gen, nil
}

// Complete 处理一条完成回执：代次与项索引必须匹配某个在途提交，
// 重复或旧代次的回执一律拒绝，不得回收。
func (e *Executor) Complete(gen uint64, index int) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	sub, ok := e.inflight[gen]
	if !ok {
		return ErrStaleGeneration
	}
	if index < 0 || index >= len(sub.done) {
		return ErrIndexOutOfRange
	}
	if sub.done[index] {
		return ErrDuplicateCompletion
	}
	sub.done[index] = true
	sub.remaining--
	if sub.remaining == 0 {
		delete(e.inflight, gen)
	}
	return nil
}

// Reset 复位设备：作废旧代次，返回复位时仍未完成的描述符数。
// 复位后任何旧代次的完成回执都会被 ErrStaleGeneration 拒绝。
func (e *Executor) Reset() (outstanding int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, sub := range e.inflight {
		outstanding += sub.remaining
	}
	e.inflight = make(map[uint64]*submission)
	return outstanding
}

// InFlight 返回当前在途（未完成）的描述符总数。
func (e *Executor) InFlight() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	n := 0
	for _, sub := range e.inflight {
		n += sub.remaining
	}
	return n
}
