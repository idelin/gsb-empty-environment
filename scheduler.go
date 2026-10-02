package memsched

import (
	"sync"
	"sync/atomic"
)

// Scheduler 与内存监控器协作：
//   - 准入控制：Submit 在内存触及高水位时阻塞，拦截新协程创建；
//   - 优先级：具备 GC 回收能力的协程进入优先队列，内存吃紧时被优先调度。
type Scheduler struct {
	monitor *Monitor
	workers int

	reclaimCh chan *Fiber // 优先队列：GC 回收能力协程
	normalCh  chan *Fiber // 普通队列

	quit chan struct{}
	wg   sync.WaitGroup

	nextID    atomic.Uint64
	completed atomic.Int64
}

// NewScheduler 创建调度器。workers 为并发执行的 worker 数，queue 为队列容量。
func NewScheduler(m *Monitor, workers, queue int) *Scheduler {
	if workers <= 0 {
		workers = 1
	}
	if queue <= 0 {
		queue = 1
	}
	return &Scheduler{
		monitor:   m,
		workers:   workers,
		reclaimCh: make(chan *Fiber, queue),
		normalCh:  make(chan *Fiber, queue),
		quit:      make(chan struct{}),
	}
}

// Start 启动 worker 协程池。
func (s *Scheduler) Start() {
	for i := 0; i < s.workers; i++ {
		s.wg.Add(1)
		go s.loop()
	}
}

// Submit 提交一个任务。reclaimCapable 为 true 表示该协程具备 GC 回收能力，
// 将被放入优先队列。内存反压期间本调用会阻塞，直到存量协程释放内存。
func (s *Scheduler) Submit(task Task, reclaimCapable bool) *Fiber {
	s.monitor.Admit() // 反压：拦截新协程准入
	f := &Fiber{
		ID:             s.nextID.Add(1),
		task:           task,
		reclaimCapable: reclaimCapable,
		done:           make(chan struct{}),
	}
	if reclaimCapable {
		s.reclaimCh <- f
	} else {
		s.normalCh <- f
	}
	return f
}

func (s *Scheduler) loop() {
	defer s.wg.Done()
	for {
		// 优先消费具备 GC 回收能力的协程。
		select {
		case f := <-s.reclaimCh:
			s.run(f)
			continue
		default:
		}
		select {
		case f := <-s.reclaimCh:
			s.run(f)
		case f := <-s.normalCh:
			s.run(f)
		case <-s.quit:
			return
		}
	}
}

func (s *Scheduler) run(f *Fiber) {
	ctx := &Context{Fiber: f, monitor: s.monitor}
	f.err = f.task(ctx)
	s.completed.Add(1)
	close(f.done)
}

// Stop 停止调度器（不再接收新任务，等待 worker 退出）。
func (s *Scheduler) Stop() {
	close(s.quit)
	s.wg.Wait()
}

// Completed 返回已完成的协程数。
func (s *Scheduler) Completed() int64 { return s.completed.Load() }

// Monitor 返回关联的内存监控器。
func (s *Scheduler) Monitor() *Monitor { return s.monitor }
