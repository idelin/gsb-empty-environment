// Package executor 提供带限时关闭能力的任务执行器。
//
// 关闭流程分两阶段：
//  1. 排空（drain）：停止接收新任务，等待在途任务在期限内完成；
//  2. 取消（cancel）：期限到达后通过 context 请求取消仍在运行的任务，
//     并返回未完成任务清单。
//
// 任务可能忽略取消信号。Go 无法强杀 goroutine，本模块也不做任何强杀：
// 关闭返回后，迟到完成的任务结果仍会被安全记录，可随时查询。
package executor

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// ErrShutdown 表示执行器已关闭，不再接收新任务。
var ErrShutdown = errors.New("executor: shutdown in progress")

// Task 是执行器运行的任务单元。任务应当响应 ctx 取消，但不强制。
type Task func(ctx context.Context) (any, error)

// Result 是一个已完成任务的结果，包括关闭后迟到完成的结果。
type Result struct {
	ID    string
	Value any
	Err   error
}

// Report 是关闭报告，可重复查询。
type Report struct {
	// ShutdownAt 是关闭流程开始的时间。
	ShutdownAt time.Time
	// Drained 为 true 表示所有任务在期限内完成，未触发取消。
	Drained bool
	// CanceledAt 是请求取消的时间；Drained 为 true 时为零值。
	CanceledAt time.Time
	// Unfinished 是查询时刻仍未完成的任务 ID。
	// 关闭后迟到完成的任务会从清单中移除，不会被重复列入。
	Unfinished []string
	// Completed 是查询时刻已完成的任务总数（含迟到完成的）。
	Completed int
}

// Executor 并发运行任务，支持限时关闭。
type Executor struct {
	ctx    context.Context
	cancel context.CancelFunc

	mu            sync.Mutex
	accepting     bool
	seq           int64
	pending       map[string]struct{} // 仍在运行的任务
	results       []Result            // 全部已完成结果，含迟到结果
	drainedNotify chan struct{}       // 非 nil 时，pending 清空后关闭它

	shutdownOnce sync.Once
	shutdownDone chan struct{} // 关闭流程结束后关闭
	report       Report
}

// New 创建一个执行器。
func New() *Executor {
	ctx, cancel := context.WithCancel(context.Background())
	return &Executor{
		ctx:          ctx,
		cancel:       cancel,
		accepting:    true,
		pending:      make(map[string]struct{}),
		shutdownDone: make(chan struct{}),
	}
}

// Submit 提交一个任务并返回其 ID。执行器已关闭时返回 ErrShutdown。
func (e *Executor) Submit(task Task) (string, error) {
	e.mu.Lock()
	if !e.accepting {
		e.mu.Unlock()
		return "", ErrShutdown
	}
	e.seq++
	id := fmt.Sprintf("task-%d", e.seq)
	e.pending[id] = struct{}{}
	e.mu.Unlock()

	go func() {
		value, err := task(e.ctx)
		e.finish(id, Result{ID: id, Value: value, Err: err})
	}()
	return id, nil
}

// finish 记录结果并从在途集合移除。结果与 pending 状态在同一把锁内
// 更新，保证任务不会处于“既不在途也无结果”的中间态；关闭后迟到的
// 结果走同一路径，因此始终安全。
func (e *Executor) finish(id string, res Result) {
	e.mu.Lock()
	e.results = append(e.results, res)
	delete(e.pending, id)
	if len(e.pending) == 0 && e.drainedNotify != nil {
		close(e.drainedNotify)
		e.drainedNotify = nil
	}
	e.mu.Unlock()
}

// Shutdown 停止接收新任务，等待在途任务完成，最长等待 timeout。
// 超时后请求取消仍在运行的任务并返回。可并发、可重复调用，
// 只有第一次调用执行关闭流程，其余调用等待并返回同一份报告。
func (e *Executor) Shutdown(timeout time.Duration) Report {
	e.shutdownOnce.Do(func() {
		defer close(e.shutdownDone)
		now := time.Now()

		e.mu.Lock()
		e.accepting = false
		if len(e.pending) == 0 {
			e.report = Report{ShutdownAt: now, Drained: true}
			e.mu.Unlock()
			e.cancel()
			return
		}
		notify := make(chan struct{})
		e.drainedNotify = notify
		e.mu.Unlock()

		timer := time.NewTimer(timeout)
		defer timer.Stop()
		select {
		case <-notify:
			e.mu.Lock()
			e.report = Report{ShutdownAt: now, Drained: true}
			e.mu.Unlock()
		case <-timer.C:
			e.cancel() // 请求取消；不等待、不强杀，任务可忽略
			e.mu.Lock()
			e.report = Report{
				ShutdownAt: now,
				CanceledAt: time.Now(),
				Unfinished: pendingIDs(e.pending),
			}
			e.mu.Unlock()
		}
		e.cancel()
	})
	<-e.shutdownDone
	report, _ := e.Report()
	return report
}

// Report 返回当前关闭报告。第二个返回值为 false 表示尚未关闭。
// 可重复查询：Unfinished 只含查询时刻仍未完成的任务，
// 关闭后迟到完成的任务会被排除，不会重复列入。
func (e *Executor) Report() (Report, bool) {
	select {
	case <-e.shutdownDone:
	default:
		return Report{}, false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	report := e.report
	unfinished := make([]string, 0, len(report.Unfinished))
	for _, id := range report.Unfinished {
		if _, ok := e.pending[id]; ok {
			unfinished = append(unfinished, id)
		}
	}
	report.Unfinished = unfinished
	report.Completed = len(e.results)
	return report, true
}

// Results 返回全部已完成任务结果的快照，含关闭后迟到完成的结果。
func (e *Executor) Results() []Result {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]Result, len(e.results))
	copy(out, e.results)
	return out
}

// Pending 返回查询时刻仍在运行的任务数。
func (e *Executor) Pending() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.pending)
}

func pendingIDs(pending map[string]struct{}) []string {
	ids := make([]string, 0, len(pending))
	for id := range pending {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
