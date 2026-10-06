// Package lifecycle 提供任务组的生命周期管理：
// 接收(Receiving) -> 排空(Draining) -> 释放(Releasing) -> 停止(Stopped)。
//
// 排空阶段等待所有已接收任务及其登记的子任务完成；
// 一旦进入释放阶段，禁止登记任何新工作；
// 重复关闭共享同一份结果。
package lifecycle

import (
	"errors"
	"sync"
	"time"
)

var (
	// ErrClosed 表示组已进入释放或停止阶段，不再接受新工作。
	ErrClosed = errors.New("lifecycle: group is closing, no new work accepted")
	// ErrTimeout 表示在超时时间内关闭未完成，后台仍会继续完成。
	ErrTimeout = errors.New("lifecycle: close timed out")
)

// State 表示生命周期阶段。
type State int32

const (
	// Receiving 接收阶段：可自由登记任务。
	Receiving State = iota
	// Draining 排空阶段：不再接受外部新任务语义不变，但已接收任务
	// 及其登记的子任务仍会被等待。
	Draining
	// Releasing 释放阶段：执行释放钩子，禁止登记任何新工作。
	Releasing
	// Stopped 停止阶段：全部工作收尾完毕。
	Stopped
)

func (s State) String() string {
	switch s {
	case Receiving:
		return "Receiving"
	case Draining:
		return "Draining"
	case Releasing:
		return "Releasing"
	case Stopped:
		return "Stopped"
	default:
		return "Unknown"
	}
}

// Scope 是任务执行上下文，运行中的任务可通过它登记子任务。
type Scope struct {
	g *Group
}

// Go 在所属组内登记一个子任务。排空阶段仍允许登记，
// 进入释放阶段后返回 ErrClosed。
func (s *Scope) Go(fn func(*Scope) error) error {
	return s.g.Go(fn)
}

// Group 管理一组任务的生命周期。
type Group struct {
	mu       sync.Mutex
	state    State
	active   int
	firstErr error
	releases []func()

	startOnce sync.Once
	done      chan struct{}
	result    error // 在 done 关闭前写入
}

// New 创建一个处于接收阶段的组。
func New() *Group {
	return &Group{done: make(chan struct{})}
}

// State 返回当前生命周期阶段。
func (g *Group) State() State {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.state
}

// Go 登记一个任务。接收与排空阶段返回 nil；
// 进入释放阶段后返回 ErrClosed。
func (g *Group) Go(fn func(*Scope) error) error {
	g.mu.Lock()
	if g.state >= Releasing {
		g.mu.Unlock()
		return ErrClosed
	}
	g.active++
	g.mu.Unlock()
	go g.run(fn)
	return nil
}

func (g *Group) run(fn func(*Scope) error) {
	err := fn(&Scope{g: g})
	g.mu.Lock()
	if err != nil && g.firstErr == nil {
		g.firstErr = err
	}
	g.active--
	g.mu.Unlock()
	g.maybeRelease()
}

// OnRelease 登记释放钩子，在释放阶段按登记顺序执行。
// 若已越过接收/排空阶段，则立即执行。
func (g *Group) OnRelease(fn func()) {
	g.mu.Lock()
	if g.state < Releasing {
		g.releases = append(g.releases, fn)
		g.mu.Unlock()
		return
	}
	g.mu.Unlock()
	fn()
}

// Close 发起关闭并等待到达停止阶段，返回首个任务错误（若有）。
// 可并发、可重复调用，所有调用共享同一份结果。
func (g *Group) Close() error {
	g.beginClose()
	<-g.done
	return g.result
}

// CloseTimeout 与 Close 类似，但最多等待 d。
// 超时返回 ErrTimeout，关闭流程仍在后台继续推进直至停止。
func (g *Group) CloseTimeout(d time.Duration) error {
	g.beginClose()
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-g.done:
		return g.result
	case <-timer.C:
		return ErrTimeout
	}
}

func (g *Group) beginClose() {
	g.startOnce.Do(func() {
		g.mu.Lock()
		if g.state == Receiving {
			g.state = Draining
		}
		g.mu.Unlock()
		g.maybeRelease()
	})
}

// maybeRelease 在排空完成（无活跃任务）时推进到释放阶段，
// 执行释放钩子后进入停止阶段。状态迁移在锁内完成，故只执行一次。
func (g *Group) maybeRelease() {
	g.mu.Lock()
	if g.state != Draining || g.active != 0 {
		g.mu.Unlock()
		return
	}
	g.state = Releasing
	releases := append([]func(){}, g.releases...)
	g.mu.Unlock()

	for _, fn := range releases {
		fn()
	}

	g.mu.Lock()
	g.state = Stopped
	g.result = g.firstErr
	g.mu.Unlock()
	close(g.done)
}
