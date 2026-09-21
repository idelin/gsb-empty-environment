// Package quota 实现 CPU 与 IO 两阶段额度的申请、切换与释放。
//
// 关键不变量：
//   - 阶段切换时先释放旧阶段额度，再等待新阶段额度，绝不“持有旧额度等待
//     新额度”，因此即使两个任务互相交换阶段也不会形成占有等待式死锁。
//   - 释放旧额度与等待新额度之间属于“半转换”状态：任务此时不持有任何
//     额度，等待可通过 ctx 取消。
//   - 任务结束（正常返回、返回错误或 panic）时通过 defer 归还持有的额度，
//     使所有阶段的额度回归初值。
package quota

import (
	"context"
	"errors"
	"sync"
)

// Phase 表示一个资源阶段。
type Phase string

const (
	CPU Phase = "cpu"
	IO  Phase = "io"
)

// ErrInvalidPhase 在引用未知阶段时返回。
var ErrInvalidPhase = errors.New("quota: invalid phase")

// ErrZeroCapacity 在某阶段容量为 0 且任务仍试图获取该阶段额度时返回，
// 通常由 ctx 取消驱动。容量为 0 的阶段无法容纳任何任务。
var ErrZeroCapacity = errors.New("quota: phase capacity is zero")

// Work 是测试注入的阶段工作：执行时机由测试通过 channel 闸门控制，
// 返回 error 表示任务失败，panic 同样视为异常结束。
type Work func(ctx context.Context) error

// semaphore 是基于带缓冲 channel 的计数信号量。
// 容量即缓冲大小，缓冲中的一个 token 代表一份可用额度。
type semaphore struct {
	tokens chan struct{}
}

func newSemaphore(capacity int) *semaphore {
	if capacity < 0 {
		capacity = 0
	}
	s := &semaphore{tokens: make(chan struct{}, capacity)}
	// 初始时所有额度均可用，预先填满缓冲。
	for i := 0; i < capacity; i++ {
		s.tokens <- struct{}{}
	}
	return s
}

// acquire 获取一份额度，ctx 取消时返回。
func (s *semaphore) acquire(ctx context.Context) error {
	select {
	case <-s.tokens:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// release 归还一份额度。
func (s *semaphore) release() {
	s.tokens <- struct{}{}
}

// Scheduler 管理各阶段的额度池。其方法可被多个 goroutine 并发调用。
type Scheduler struct {
	mu     sync.RWMutex
	phases map[Phase]*semaphore
}

// New 根据容量映射创建调度器。容量为 0 的阶段不接受任何任务。
func New(capacities map[Phase]int) *Scheduler {
	phases := make(map[Phase]*semaphore, len(capacities))
	for p, c := range capacities {
		phases[p] = newSemaphore(c)
	}
	return &Scheduler{phases: phases}
}

// Available 返回某阶段的可用额度数；未知阶段返回 0。
func (s *Scheduler) Available(p Phase) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if sem, ok := s.phases[p]; ok {
		return len(sem.tokens)
	}
	return 0
}

func (s *Scheduler) semFor(p Phase) (*semaphore, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sem, ok := s.phases[p]
	if !ok {
		return nil, ErrInvalidPhase
	}
	return sem, nil
}

// NewTask 创建一个初始不持有任何额度的任务句柄。
// Task 的方法应由任务所在的单个 goroutine 顺序调用。
func (s *Scheduler) NewTask() *Task {
	return &Task{sched: s}
}

// Task 表示一次额度使用过程，任一时刻至多持有一个阶段的额度。
type Task struct {
	sched *Scheduler

	mu        sync.Mutex
	heldSem   *semaphore
	heldPhase Phase
}

// Current 返回任务当前持有额度的阶段；不持有时第二个返回值为 false。
func (t *Task) Current() (Phase, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.heldPhase, t.heldSem != nil
}

// Acquire 获取目标阶段额度。若任务此前已持有其它阶段额度，则等价于
// Switch（先释放旧额度再获取新额度）。
func (t *Task) Acquire(ctx context.Context, target Phase) error {
	t.mu.Lock()
	oldSem := t.heldSem
	oldPhase := t.heldPhase
	t.mu.Unlock()

	if oldSem != nil {
		if oldPhase == target {
			return nil
		}
		return t.switchPhase(ctx, target)
	}

	sem, err := t.sched.semFor(target)
	if err != nil {
		return err
	}
	if err := sem.acquire(ctx); err != nil {
		return err
	}

	t.mu.Lock()
	t.heldSem = sem
	t.heldPhase = target
	t.mu.Unlock()
	return nil
}

// Switch 原子地从旧阶段切换到新阶段：
//  1. 立即释放旧阶段额度（其它任务立刻可用）；
//  2. 清空任务持有状态，进入“半转换”状态；
//  3. 等待新阶段额度，可被 ctx 取消；
//  4. 成功后记录新的持有状态。
//
// 等待期间任务不持有任何额度，因此取消等待不会导致额度泄漏，
// 也不会因为互相持有对方需要的额度而死锁。
func (t *Task) Switch(ctx context.Context, target Phase) error {
	t.mu.Lock()
	oldSem := t.heldSem
	oldPhase := t.heldPhase
	t.mu.Unlock()

	if oldSem == nil {
		return t.Acquire(ctx, target)
	}
	if oldPhase == target {
		return nil
	}
	return t.switchPhase(ctx, target)
}

func (t *Task) switchPhase(ctx context.Context, target Phase) error {
	// 入参上下文已取消时，保持原有持有状态不变，由调用方决定后续动作。
	if err := ctx.Err(); err != nil {
		return err
	}

	newSem, err := t.sched.semFor(target)
	if err != nil {
		return err
	}

	t.mu.Lock()
	oldSem := t.heldSem
	// 第一步：释放旧额度。channel 发送在接收方就绪前即完成额度移交，
	// 旧额度对其它等待者立即可见。
	oldSem.release()
	// 第二步：进入半转换状态——不持有任何额度。
	t.heldSem = nil
	t.heldPhase = ""
	t.mu.Unlock()

	// 第三步：等待新额度。容量为 0 时这里会一直阻塞，直到 ctx 取消。
	if err := newSem.acquire(ctx); err != nil {
		return err
	}

	// 第四步：获取成功，更新持有状态。
	t.mu.Lock()
	t.heldSem = newSem
	t.heldPhase = target
	t.mu.Unlock()
	return nil
}

// Release 释放当前持有的额度；不持有时为空操作。
func (t *Task) Release() {
	t.mu.Lock()
	sem := t.heldSem
	t.heldSem = nil
	t.heldPhase = ""
	t.mu.Unlock()

	if sem != nil {
		sem.release()
	}
}

// Run 按给定阶段顺序依次获取额度并执行对应工作。
// 阶段间切换遵循“先释放旧额度再等待新额度”的规则。
// 无论工作返回错误还是 panic，退出前都会通过 defer 归还当前额度；
// panic 在归还后继续向上抛出。
func (s *Scheduler) Run(ctx context.Context, steps []Phase, work map[Phase]Work) (err error) {
	task := s.NewTask()
	defer task.Release()

	for _, p := range steps {
		if err = task.Acquire(ctx, p); err != nil {
			return err
		}
		fn := work[p]
		if fn == nil {
			continue
		}
		if err = fn(ctx); err != nil {
			return err
		}
	}
	return nil
}
