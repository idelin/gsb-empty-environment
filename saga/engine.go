package saga

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"
)

// ErrUnknownInstance 表示实例不存在。
var ErrUnknownInstance = errors.New("saga: unknown instance")

// Engine 是长事务引擎：驱动状态机迁移、执行超时监控、保证每次迁移先落盘。
//
// 持久化先行原则：任何状态迁移都先写入 Store（状态快照 + 审计事件），
// 再推进下一步。崩溃后通过 Recover 从磁盘恢复：
//   - completed 步骤绝不重跑（不重）；
//   - running（崩溃时执行到一半）步骤重置为 pending 重新执行，
//     因此 Action 必须具备幂等性（不漏）；
//   - waiting 步骤保留原截止时间，超时则由补偿流程接管；
//   - compensating 实例从中断处继续补偿（已补偿步骤跳过）。
type Engine struct {
	store           *Store
	now             func() time.Time
	monitorInterval time.Duration

	mu        sync.Mutex
	sagas     map[string]*SagaDef
	instances map[string]*Instance
	locks     map[string]*sync.Mutex // 每实例一把锁，串行化该实例的所有迁移

	stopCh         chan struct{}
	stopped        chan struct{}
	monitorOnce    sync.Once
	stopOnce       sync.Once
	monitorStarted bool
}

// Option 是引擎的可选配置。
type Option func(*Engine)

// WithMonitorInterval 设置超时监控器的扫描周期。
func WithMonitorInterval(d time.Duration) Option {
	return func(e *Engine) { e.monitorInterval = d }
}

// NewEngine 创建引擎。
func NewEngine(store *Store, opts ...Option) *Engine {
	e := &Engine{
		store:           store,
		now:             time.Now,
		monitorInterval: 100 * time.Millisecond,
		sagas:           make(map[string]*SagaDef),
		instances:       make(map[string]*Instance),
		locks:           make(map[string]*sync.Mutex),
		stopCh:          make(chan struct{}),
		stopped:         make(chan struct{}),
	}
	for _, o := range opts {
		o(e)
	}
	return e
}

// Register 注册一个长事务定义。Recover 之前必须完成注册。
func (e *Engine) Register(def *SagaDef) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.sagas[def.Name] = def
}

// Start 创建并启动一个长事务实例，同步推进直到：全部完成、
// 失败进入补偿、或某步骤返回 ErrWait 进入等待。返回实例 ID。
func (e *Engine) Start(sagaName string) (string, error) {
	e.mu.Lock()
	def, ok := e.sagas[sagaName]
	e.mu.Unlock()
	if !ok {
		return "", fmt.Errorf("saga: unknown saga %q", sagaName)
	}
	now := e.now()
	in := &Instance{
		ID:        newID(),
		Saga:      sagaName,
		Status:    StatusRunning,
		Data:      make(map[string]string),
		CreatedAt: now,
		UpdatedAt: now,
	}
	for _, s := range def.Steps {
		in.Steps = append(in.Steps, StepState{Name: s.Name, Status: StatusPending})
	}
	// 先落盘再对外可见，保证任何时刻实例都有持久化状态。
	if err := e.persist(in, Event{Type: "instance_created", Detail: sagaName}); err != nil {
		return "", err
	}
	e.mu.Lock()
	e.instances[in.ID] = in
	e.mu.Unlock()
	if err := e.advance(in); err != nil {
		return in.ID, err
	}
	return in.ID, nil
}

// Complete 是外部系统的成功回调：将等待中的步骤标记为完成并继续推进事务。
func (e *Engine) Complete(id, stepName, result string) error {
	in, err := e.instance(id)
	if err != nil {
		return err
	}
	l := e.lockFor(id)
	l.Lock()
	idx := stepIndex(in, stepName)
	if idx < 0 {
		l.Unlock()
		return fmt.Errorf("saga: unknown step %q", stepName)
	}
	st := &in.Steps[idx]
	if st.Status != StatusWaiting {
		l.Unlock()
		return fmt.Errorf("saga: step %q is %s, not waiting", stepName, st.Status)
	}
	st.Status = StatusCompleted
	st.Result = result
	st.CompletedAt = e.now()
	perr := e.persist(in, Event{Type: "step_completed", Step: st.Name, Detail: result})
	l.Unlock()
	if perr != nil {
		return perr
	}
	return e.advance(in)
}

// Fail 是外部系统的拒绝回调：标记步骤失败并触发逆向补偿。
func (e *Engine) Fail(id, stepName, reason string) error {
	in, err := e.instance(id)
	if err != nil {
		return err
	}
	idx := stepIndex(in, stepName)
	if idx < 0 {
		return fmt.Errorf("saga: unknown step %q", stepName)
	}
	return e.abort(in, idx, "step_rejected", reason)
}

// Recover 从持久化存储恢复所有未终结的实例并继续驱动。
// 必须在 Register 之后、StartMonitor 之前调用。
func (e *Engine) Recover() error {
	insts, err := e.store.LoadInstances()
	if err != nil {
		return err
	}
	now := e.now()
	for _, in := range insts {
		e.mu.Lock()
		if _, exists := e.instances[in.ID]; !exists {
			e.instances[in.ID] = in
		}
		def := e.sagas[in.Saga]
		e.mu.Unlock()
		if in.Terminal() {
			continue
		}
		if def == nil {
			return fmt.Errorf("saga: cannot recover %s: saga %q not registered", in.ID, in.Saga)
		}
		l := e.lockFor(in.ID)
		l.Lock()
		// 崩溃时执行到一半的步骤重置为 pending，由幂等的 Action 重放。
		for i := range in.Steps {
			if in.Steps[i].Status == StatusRunning {
				in.Steps[i].Status = StatusPending
				if err := e.persist(in, Event{Type: "step_retry", Step: in.Steps[i].Name,
					Detail: "recovered from interrupted execution"}); err != nil {
					l.Unlock()
					return err
				}
			}
		}
		expired := -1
		for i := range in.Steps {
			st := &in.Steps[i]
			if st.Status == StatusWaiting && !st.Deadline.IsZero() && !now.Before(st.Deadline) {
				expired = i
				break
			}
		}
		status := in.Status
		switch {
		case status == StatusCompensating:
			// 补偿中断：从断点继续，已补偿的步骤会被跳过。
			err := e.compensateLocked(in, def, len(in.Steps)-1)
			l.Unlock()
			if err != nil {
				return err
			}
		case expired >= 0:
			// 停机期间等待已超时：立即进入补偿。
			l.Unlock()
			if err := e.abort(in, expired, "step_timeout",
				"external wait timed out during downtime"); err != nil {
				return err
			}
		default:
			l.Unlock()
			// waiting 实例继续等待；其余继续推进。
			if err := e.advance(in); err != nil {
				return err
			}
		}
	}
	return nil
}

// StartMonitor 启动超时监控器：周期性扫描等待中超时的步骤并触发补偿。
func (e *Engine) StartMonitor() {
	e.monitorOnce.Do(func() {
		e.monitorStarted = true
		go func() {
			defer close(e.stopped)
			t := time.NewTicker(e.monitorInterval)
			defer t.Stop()
			for {
				select {
				case <-e.stopCh:
					return
				case <-t.C:
					e.checkTimeouts()
				}
			}
		}()
	})
}

// Stop 停止超时监控器。
func (e *Engine) Stop() {
	e.stopOnce.Do(func() { close(e.stopCh) })
	if e.monitorStarted {
		<-e.stopped
	}
}

// Instance 返回实例状态的快照副本。
func (e *Engine) Instance(id string) (*Instance, error) {
	in, err := e.instance(id)
	if err != nil {
		return nil, err
	}
	l := e.lockFor(id)
	l.Lock()
	defer l.Unlock()
	cp := *in
	cp.Steps = append([]StepState(nil), in.Steps...)
	if in.Data != nil {
		cp.Data = make(map[string]string, len(in.Data))
		for k, v := range in.Data {
			cp.Data[k] = v
		}
	}
	return &cp, nil
}

// Wait 轮询等待实例到达终态，超时返回 false。
func (e *Engine) Wait(id string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		in, err := e.Instance(id)
		if err == nil && in.Terminal() {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}

// AuditTrail 返回实例的完整审计轨迹。
func (e *Engine) AuditTrail(id string) ([]Event, error) {
	return e.store.AuditTrail(id)
}

// advance 从第一个未完成步骤开始推进状态机，直到完成、失败或进入等待。
func (e *Engine) advance(in *Instance) error {
	l := e.lockFor(in.ID)
	l.Lock()
	defer l.Unlock()
	if in.Status != StatusRunning {
		return nil
	}
	e.mu.Lock()
	def := e.sagas[in.Saga]
	e.mu.Unlock()
	for i := range in.Steps {
		st := &in.Steps[i]
		switch st.Status {
		case StatusCompleted:
			continue // 已完成步骤绝不重跑
		case StatusWaiting:
			return nil // 挂起，等待外部回调或超时
		}
		step := def.Steps[i]
		st.Status = StatusRunning
		st.StartedAt = e.now()
		st.Attempts++
		if err := e.persist(in, Event{Type: "step_started", Step: st.Name}); err != nil {
			return err
		}
		err := step.Action(&Context{InstanceID: in.ID, Step: st.Name, Data: in.Data})
		switch {
		case err == nil:
			st.Status = StatusCompleted
			st.CompletedAt = e.now()
			if perr := e.persist(in, Event{Type: "step_completed", Step: st.Name}); perr != nil {
				return perr
			}
		case errors.Is(err, ErrWait):
			st.Status = StatusWaiting
			if step.Timeout > 0 {
				st.Deadline = e.now().Add(step.Timeout)
			}
			if perr := e.persist(in, Event{Type: "step_waiting", Step: st.Name,
				Detail: "deadline=" + st.Deadline.Format(time.RFC3339)}); perr != nil {
				return perr
			}
			return nil
		default:
			st.Status = StatusFailed
			st.Err = err.Error()
			if perr := e.persist(in, Event{Type: "step_failed", Step: st.Name,
				Detail: err.Error()}); perr != nil {
				return perr
			}
			return e.compensateLocked(in, def, i-1)
		}
	}
	in.Status = StatusCompleted
	return e.persist(in, Event{Type: "instance_completed"})
}

// abort 标记指定步骤失败并触发逆向补偿（外部拒绝与超时共用）。
func (e *Engine) abort(in *Instance, stepIdx int, eventType, reason string) error {
	l := e.lockFor(in.ID)
	l.Lock()
	defer l.Unlock()
	return e.abortLocked(in, stepIdx, eventType, reason)
}

func (e *Engine) abortLocked(in *Instance, stepIdx int, eventType, reason string) error {
	if in.Status != StatusRunning {
		return nil // 已在补偿或已终结，幂等返回
	}
	st := &in.Steps[stepIdx]
	if st.Status == StatusCompleted || st.Status == StatusFailed {
		return nil
	}
	st.Status = StatusFailed
	st.Err = reason
	if err := e.persist(in, Event{Type: eventType, Step: st.Name, Detail: reason}); err != nil {
		return err
	}
	e.mu.Lock()
	def := e.sagas[in.Saga]
	e.mu.Unlock()
	return e.compensateLocked(in, def, stepIdx-1)
}

// compensateLocked 从 from 索引开始逆序补偿所有已完成且未补偿的步骤。
// 调用者必须持有实例锁。补偿动作失败时实例停留在 compensating 状态，
// 等待 Recover 重试，保证补偿不丢失。
func (e *Engine) compensateLocked(in *Instance, def *SagaDef, from int) error {
	if in.Status == StatusCompensated {
		return nil
	}
	in.Status = StatusCompensating
	if err := e.persist(in, Event{Type: "compensation_started"}); err != nil {
		return err
	}
	for i := from; i >= 0; i-- {
		st := &in.Steps[i]
		if st.Status != StatusCompleted || st.Compensated {
			continue
		}
		if def.Steps[i].Compensate != nil {
			if err := def.Steps[i].Compensate(&Context{InstanceID: in.ID, Step: st.Name, Data: in.Data}); err != nil {
				return e.persist(in, Event{Type: "compensation_failed", Step: st.Name,
					Detail: err.Error()})
			}
		}
		st.Compensated = true
		if err := e.persist(in, Event{Type: "step_compensated", Step: st.Name}); err != nil {
			return err
		}
	}
	in.Status = StatusCompensated
	return e.persist(in, Event{Type: "instance_compensated"})
}

// checkTimeouts 扫描所有运行中的实例，将等待超时的步骤标记失败并触发补偿。
func (e *Engine) checkTimeouts() {
	e.mu.Lock()
	active := make([]*Instance, 0, len(e.instances))
	for _, in := range e.instances {
		active = append(active, in)
	}
	e.mu.Unlock()
	now := e.now()
	for _, in := range active {
		l := e.lockFor(in.ID)
		l.Lock()
		if in.Status == StatusRunning {
			for i := range in.Steps {
				st := &in.Steps[i]
				if st.Status == StatusWaiting && !st.Deadline.IsZero() && !now.Before(st.Deadline) {
					e.abortLocked(in, i, "step_timeout",
						fmt.Sprintf("external wait exceeded deadline %s", st.Deadline.Format(time.RFC3339)))
					break
				}
			}
		}
		l.Unlock()
	}
}

// persist 先写状态快照再追加审计事件，是唯一的落盘入口。
func (e *Engine) persist(in *Instance, ev Event) error {
	in.UpdatedAt = e.now()
	if err := e.store.SaveInstance(in); err != nil {
		return err
	}
	ev.Time = e.now()
	ev.InstanceID = in.ID
	return e.store.AppendAudit(ev)
}

func (e *Engine) instance(id string) (*Instance, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	in, ok := e.instances[id]
	if !ok {
		return nil, ErrUnknownInstance
	}
	return in, nil
}

func (e *Engine) lockFor(id string) *sync.Mutex {
	e.mu.Lock()
	defer e.mu.Unlock()
	l, ok := e.locks[id]
	if !ok {
		l = &sync.Mutex{}
		e.locks[id] = l
	}
	return l
}

func stepIndex(in *Instance, name string) int {
	for i := range in.Steps {
		if in.Steps[i].Name == name {
			return i
		}
	}
	return -1
}

func newID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}
