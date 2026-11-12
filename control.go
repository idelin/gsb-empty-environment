package netsec

import (
	"errors"
	"fmt"
	"sync"
)

// Signal 是会话可下发的控制信号。
type Signal int

const (
	SignalInterrupt Signal = iota + 1
	SignalTerminate
	SignalKill
	SignalHangup
	SignalQuit
)

func (s Signal) valid() bool {
	return s >= SignalInterrupt && s <= SignalQuit
}

func (s Signal) String() string {
	switch s {
	case SignalInterrupt:
		return "interrupt"
	case SignalTerminate:
		return "terminate"
	case SignalKill:
		return "kill"
	case SignalHangup:
		return "hangup"
	case SignalQuit:
		return "quit"
	default:
		return fmt.Sprintf("signal(%d)", int(s))
	}
}

var (
	// ErrNoProcess 表示通道当前没有任何受控进程。
	ErrNoProcess = errors.New("netsec: no active process on channel")
	// ErrStaleGeneration 表示句柄所属的进程启动代已被新进程取代。
	ErrStaleGeneration = errors.New("netsec: handle belongs to a superseded process generation")
	// ErrProcessExited 表示该代进程已完成，迟到的控制消息被拒绝。
	ErrProcessExited = errors.New("netsec: process generation already completed")
	// ErrSignalNotAllowed 表示信号不在通道声明的能力集合内。
	ErrSignalNotAllowed = errors.New("netsec: signal not in declared capability set")
	// ErrWindowNotAllowed 表示通道未声明窗口调整能力。
	ErrWindowNotAllowed = errors.New("netsec: window resize not in declared capability set")
	// ErrInvalidSignal 表示信号值未知。
	ErrInvalidSignal = errors.New("netsec: unknown signal")
)

// Capability 是通道实例声明的控制能力集合。
type Capability struct {
	Signals      map[Signal]bool
	WindowResize bool
}

// Process 是会话宿主管理的远程进程抽象，测试中由可暂停替身实现。
type Process interface {
	PID() int
	Signal(sig Signal) error
	Resize(rows, cols uint16) error
}

// Router 绑定单个通道实例，按进程启动代路由控制消息。
// 完成（Complete）与控制（Send/Resize/Cancel）在代状态上构成单一顺序：
// 任一控制操作要么完整发生在 Complete 之前（已投递），要么在其之后被拒绝。
type Router struct {
	mu        sync.Mutex
	channelID uint64
	caps      Capability
	nextGen   uint64
	active    *generation
}

type generation struct {
	id   uint64
	proc Process
	done bool
	wg   sync.WaitGroup // 在途投递，Complete 等待其排空
}

// NewRouter 为通道实例创建控制路由器。
func NewRouter(channelID uint64, caps Capability) *Router {
	return &Router{channelID: channelID, caps: caps}
}

// ChannelID 返回路由器绑定的通道实例标识。
func (r *Router) ChannelID() uint64 { return r.channelID }

// Start 在通道上登记新启动的进程，递增启动代并使旧句柄失效。
func (r *Router) Start(proc Process) *Handle {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextGen++
	r.active = &generation{id: r.nextGen, proc: proc}
	return &Handle{router: r, gen: r.nextGen, pid: proc.PID()}
}

// Complete 标记指定代的进程已结束，并等待在途投递排空。
// 返回后，该代上的任何迟到控制消息都会被拒绝。
func (r *Router) Complete(gen uint64) error {
	r.mu.Lock()
	g := r.active
	if g == nil {
		r.mu.Unlock()
		return ErrNoProcess
	}
	if g.id != gen {
		r.mu.Unlock()
		return ErrStaleGeneration
	}
	g.done = true
	r.mu.Unlock()
	g.wg.Wait()
	return nil
}

// Handle 是绑定通道实例与进程启动代的控制句柄。
type Handle struct {
	router *Router
	gen    uint64
	pid    int
}

// Generation 返回句柄绑定的进程启动代。
func (h *Handle) Generation() uint64 { return h.gen }

// PID 返回句柄绑定的进程号；进程号可能被复用，路由判断只依赖启动代。
func (h *Handle) PID() int { return h.pid }

// acquire 校验句柄归属的代仍然存活，并把本次投递登记为在途。
func (h *Handle) acquire() (*generation, error) {
	r := h.router
	r.mu.Lock()
	defer r.mu.Unlock()
	g := r.active
	if g == nil {
		return nil, ErrNoProcess
	}
	if g.id != h.gen {
		return nil, ErrStaleGeneration
	}
	if g.done {
		return nil, ErrProcessExited
	}
	g.wg.Add(1)
	return g, nil
}

// Send 向当前代进程投递信号，信号必须在通道声明的能力集合内。
func (h *Handle) Send(sig Signal) error {
	if !sig.valid() {
		return ErrInvalidSignal
	}
	if !h.router.caps.Signals[sig] {
		return ErrSignalNotAllowed
	}
	g, err := h.acquire()
	if err != nil {
		return err
	}
	defer g.wg.Done()
	return g.proc.Signal(sig)
}

// Resize 向当前代进程下发窗口调整，通道需声明窗口调整能力。
func (h *Handle) Resize(rows, cols uint16) error {
	if !h.router.caps.WindowResize {
		return ErrWindowNotAllowed
	}
	g, err := h.acquire()
	if err != nil {
		return err
	}
	defer g.wg.Done()
	return g.proc.Resize(rows, cols)
}

// Cancel 取消当前代进程，语义等同于投递 SignalKill。
func (h *Handle) Cancel() error {
	return h.Send(SignalKill)
}
