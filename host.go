package netsec

import (
	"errors"
	"fmt"
	"sync"
)

var (
	errNilTable      = errors.New("netsec: cannot append nil table")
	errNilDomain     = errors.New("netsec: host requires a non-nil domain")
	errUnknownThread = errors.New("netsec: thread is not registered with host")
)

// WideningError 表示追加的判定表会在有限域内放宽某些请求的最终动作，
// 违反“追加只能收窄”。安装在产生任何副作用前返回该错误。
type WideningError struct {
	Request Key
	Before  Decision
	After   Decision
}

func (e *WideningError) Error() string {
	return fmt.Sprintf("netsec: table would widen syscall %d from %s to %s",
		e.Request.Syscall, e.Before.Action, e.After.Action)
}

// InstallError 包装全线程同步安装过程中的挂载失败，并记录回滚结果。
type InstallError struct {
	// FailedThread 是第一个挂载失败的线程标识。
	FailedThread string
	// Cause 是挂载钩子返回的底层错误。
	Cause error
	// RolledBack 为 true 表示所有已挂载线程均已恢复为旧链，无部分链残留。
	RolledBack bool
}

func (e *InstallError) Error() string {
	if e.RolledBack {
		return fmt.Sprintf("netsec: install failed on thread %q (%v); all threads rolled back", e.FailedThread, e.Cause)
	}
	return fmt.Sprintf("netsec: install failed on thread %q (%v); rollback incomplete", e.FailedThread, e.Cause)
}

func (e *InstallError) Unwrap() error { return e.Cause }

// AttachHook 模拟真实 seccomp/平台过滤器的挂载动作：把 next 链安装到指定线程。
// 返回非 nil 错误表示该线程挂载失败，宿主将中止安装并回滚其余线程。
// 钩子必须是线程安全的；宿主保证同一时刻只有一次安装在调用钩子。
type AttachHook func(thread *Thread, next *Chain) error

// Host 是沙箱宿主，管理一组共享同一过滤器链的线程。
// 所有评估与安装都在宿主锁保护下进行，安装期间不会有线程读到中间状态的链。
type Host struct {
	mu      sync.RWMutex
	domain  *Domain
	chain   *Chain
	threads map[string]*Thread
	hook    AttachHook
}

// NewHost 创建宿主。domain 给出收窄校验的有限域；hook 为每个线程的挂载钩子，
// 为 nil 时使用恒成功的默认钩子。
func NewHost(domain *Domain, hook AttachHook) (*Host, error) {
	if domain == nil {
		return nil, errNilDomain
	}
	if hook == nil {
		hook = func(*Thread, *Chain) error { return nil }
	}
	return &Host{
		domain:  domain,
		chain:   NewChain(),
		threads: make(map[string]*Thread),
		hook:    hook,
	}, nil
}

// AddThread 注册一个共享当前过滤器链的新线程，标识重复时返回错误。
func (h *Host) AddThread(id string) (*Thread, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, exists := h.threads[id]; exists {
		return nil, fmt.Errorf("netsec: duplicate thread id %q", id)
	}
	thread := &Thread{id: id, host: h, chain: h.chain}
	h.threads[id] = thread
	return thread, nil
}

// RemoveThread 注销线程。
func (h *Host) RemoveThread(id string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, exists := h.threads[id]; !exists {
		return fmt.Errorf("%w: %q", errUnknownThread, id)
	}
	delete(h.threads, id)
	return nil
}

// Thread 返回已注册线程。
func (h *Host) Thread(id string) (*Thread, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	thread, ok := h.threads[id]
	return thread, ok
}

// Chain 返回宿主当前过滤器链的快照（链本身不可变，可安全读取）。
func (h *Host) Chain() *Chain {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.chain
}

// Install 在全部线程上同步追加一张判定表，步骤为：
//  1. 在有限域上穷举校验追加只会收窄（或持平）最终行为，否则直接返回
//     [WideningError]，不触碰任何线程；
//  2. 构造新的不可变链，逐线程调用挂载钩子；
//  3. 任一线程失败时，对已挂载线程按逆序回滚到旧链，全部线程保持原链，
//     返回带 RolledBack=true 的 [InstallError]。
//
// 整个过程持有写锁，评估请求在安装期间阻塞，因此任何线程都不可能观察到部分链。
func (h *Host) Install(table *Table) error {
	if table == nil {
		return errNilTable
	}
	h.mu.Lock()
	defer h.mu.Unlock()

	if key, before, after, ok := h.chain.IsNarrowing(h.domain, table); !ok {
		return &WideningError{Request: key, Before: before, After: after}
	}

	next, err := h.chain.Append(table)
	if err != nil {
		return err
	}

	ids := make([]string, 0, len(h.threads))
	for id := range h.threads {
		ids = append(ids, id)
	}
	// 固定挂载顺序，使失败与回滚行为可复现。
	sortStrings(ids)

	attached := make([]*Thread, 0, len(ids))
	var failedID string
	var cause error
	for _, id := range ids {
		thread := h.threads[id]
		if hookErr := h.hook(thread, next); hookErr != nil {
			failedID, cause = id, hookErr
			break
		}
		thread.chain = next
		attached = append(attached, thread)
	}
	if cause == nil {
		h.chain = next
		return nil
	}

	rollbackErr := h.rollbackLocked(attached)
	return &InstallError{
		FailedThread: failedID,
		Cause:        cause,
		RolledBack:   rollbackErr == nil,
	}
}

// rollbackLocked 把已挂载新链的线程按逆序恢复为旧链。回滚钩子复用同一挂载钩子，
// 回滚本身失败会返回错误；正常情况下宿主状态与全部线程都保持安装前的链。
func (h *Host) rollbackLocked(attached []*Thread) error {
	previous := h.chain
	var firstErr error
	for i := len(attached) - 1; i >= 0; i-- {
		thread := attached[i]
		if err := h.hook(thread, previous); err != nil && firstErr == nil {
			firstErr = err
		}
		thread.chain = previous
	}
	return firstErr
}

// evaluate 在当前线程的共享链上解析一次系统调用请求。
func (h *Host) evaluate(thread *Thread, request Key) Decision {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if thread.chain == nil {
		return baselineDecision
	}
	decision, _ := thread.chain.Resolve(request)
	return decision
}

// Thread 表示宿主中的一个执行线程，持有与其他线程共享的过滤器链引用。
type Thread struct {
	id    string
	host  *Host
	chain *Chain
}

// ID 返回线程标识。
func (t *Thread) ID() string { return t.id }

// Evaluate 在线程当前共享链上解析系统调用请求，返回最终决策。
func (t *Thread) Evaluate(request Key) Decision {
	return t.host.evaluate(t, request)
}

// Chain 返回线程当前挂载的过滤器链。
func (t *Thread) Chain() *Chain {
	t.host.mu.RLock()
	defer t.host.mu.RUnlock()
	return t.chain
}

func sortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j-1] > values[j]; j-- {
			values[j-1], values[j] = values[j], values[j-1]
		}
	}
}
