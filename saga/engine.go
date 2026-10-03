package saga

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// errCrash is used by tests to simulate an abrupt power loss.
var errCrash = errors.New("saga: simulated crash")

// Engine executes saga definitions and persists every state transition
// to per-transaction journals in a directory. Re-opening an Engine on
// the same directory recovers all unfinished transactions exactly
// where they left off, including re-arming pending timeout monitors.
type Engine struct {
	dir     string
	onEvent func(Event)

	// crashHook, when non-nil, is evaluated after each journaled
	// event; returning true simulates an abrupt power loss. Test-only.
	crashHook func(Event) bool

	mu     sync.Mutex
	txs    map[string]*Tx
	defs   map[string]*Definition
	closed bool
	wg     sync.WaitGroup
}

// Open creates or recovers an engine rooted at dir. onEvent, if
// non-nil, is invoked after every persisted event (audit hook).
func Open(dir string, onEvent func(Event)) (*Engine, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	e := &Engine{
		dir:     dir,
		onEvent: onEvent,
		txs:     make(map[string]*Tx),
		defs:    make(map[string]*Definition),
	}
	matches, err := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	if err != nil {
		return nil, err
	}
	sort.Strings(matches)
	for _, m := range matches {
		id := strings.TrimSuffix(filepath.Base(m), ".jsonl")
		j, events, err := openJournal(m)
		if err != nil {
			return nil, fmt.Errorf("saga: recover %s: %w", id, err)
		}
		tx := newTx(e, id, j)
		for i := range events {
			tx.apply(&events[i])
		}
		e.txs[id] = tx
	}
	return e, nil
}

// Register makes a definition available and resumes any recovered,
// non-terminal transactions of that definition.
func (e *Engine) Register(def *Definition) {
	e.mu.Lock()
	e.defs[def.Name] = def
	var resume []*Tx
	for _, tx := range e.txs {
		if tx.defName == def.Name && tx.def == nil {
			tx.def = def
			if !tx.getStatus().Terminal() {
				resume = append(resume, tx)
			}
		}
	}
	e.mu.Unlock()
	for _, tx := range resume {
		e.run(tx)
	}
}

// Start begins a new transaction and returns its id.
func (e *Engine) Start(defName string, data map[string]interface{}) (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return "", errors.New("saga: engine closed")
	}
	def, ok := e.defs[defName]
	if !ok {
		return "", fmt.Errorf("saga: definition %q not registered", defName)
	}
	if data == nil {
		data = make(map[string]interface{})
	}
	id := newTxID()
	j, _, err := openJournal(filepath.Join(e.dir, id+".jsonl"))
	if err != nil {
		return "", err
	}
	tx := newTx(e, id, j)
	tx.def = def
	tx.defName = defName
	tx.data = data
	e.txs[id] = tx
	tx.append(Event{Type: EvTxStarted, Def: defName, Data: data})
	e.run(tx)
	return id, nil
}

// Deliver routes an external asynchronous callback to a waiting
// transaction.
func (e *Engine) Deliver(id string, payload interface{}) error {
	e.mu.Lock()
	tx, ok := e.txs[id]
	e.mu.Unlock()
	if !ok {
		return fmt.Errorf("saga: unknown transaction %q", id)
	}
	awaiting := tx.isAwaiting()
	if !awaiting {
		return errors.New("saga: transaction is not awaiting a callback")
	}
	select {
	case tx.awaitCh <- payload:
		return nil
	case <-tx.done:
		return errors.New("saga: transaction already finished")
	case <-tx.stopCh:
		return errors.New("saga: engine stopped")
	case <-time.After(5 * time.Second):
		return errors.New("saga: deliver timed out")
	}
}

// Wait blocks until the transaction reaches a terminal status.
func (e *Engine) Wait(id string) (Status, error) {
	e.mu.Lock()
	tx, ok := e.txs[id]
	e.mu.Unlock()
	if !ok {
		return "", fmt.Errorf("saga: unknown transaction %q", id)
	}
	<-tx.done
	return tx.getStatus(), nil
}

// Status returns the current status of a transaction.
func (e *Engine) Status(id string) (Status, error) {
	e.mu.Lock()
	tx, ok := e.txs[id]
	e.mu.Unlock()
	if !ok {
		return "", fmt.Errorf("saga: unknown transaction %q", id)
	}
	return tx.getStatus(), nil
}

// Audit reads back the full journaled event trail of a transaction.
func (e *Engine) Audit(id string) ([]Event, error) {
	j, events, err := openJournal(filepath.Join(e.dir, id+".jsonl"))
	if err != nil {
		return nil, err
	}
	j.close()
	return events, nil
}

// Close stops all runner goroutines and closes journals. Unfinished
// transactions resume on the next Open. It models a shutdown (or, from
// the journal's point of view, a power loss).
func (e *Engine) Close() {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return
	}
	e.closed = true
	txs := make([]*Tx, 0, len(e.txs))
	for _, tx := range e.txs {
		close(tx.stopCh)
		txs = append(txs, tx)
	}
	e.mu.Unlock()
	e.wg.Wait()
	for _, tx := range txs {
		tx.journal.close()
	}
}

func (e *Engine) run(tx *Tx) {
	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		defer func() {
			if r := recover(); r != nil && r != errCrash {
				panic(r)
			}
		}()
		tx.loop()
	}()
}

func newTxID() string {
	var b [8]byte
	rand.Read(b[:])
	return fmt.Sprintf("tx-%d-%s", time.Now().UnixNano(), hex.EncodeToString(b[:]))
}

// Tx is the in-memory state machine of one transaction. Its state is a
// pure function of the journaled events (see apply).
type Tx struct {
	id      string
	eng     *Engine
	journal *journal
	def     *Definition
	defName string

	// Runner-owned state, rebuilt from the journal by apply.
	next         int
	outputs      map[int]interface{}
	data         map[string]interface{}
	started      map[int]bool
	completed    map[int]bool
	compDone     map[int]bool
	awaiting     bool
	deadline     time.Time
	compensating bool
	compNext     int
	failReason   string

	smu      sync.RWMutex
	status   Status
	awaitCh  chan interface{}
	stopCh   chan struct{}
	done     chan struct{}
	doneOnce sync.Once
}

func newTx(e *Engine, id string, j *journal) *Tx {
	return &Tx{
		id:        id,
		eng:       e,
		journal:   j,
		outputs:   make(map[int]interface{}),
		data:      make(map[string]interface{}),
		started:   make(map[int]bool),
		completed: make(map[int]bool),
		compDone:  make(map[int]bool),
		status:    StatusRunning,
		compNext:  -1,
		awaitCh:   make(chan interface{}, 1),
		stopCh:    make(chan struct{}),
		done:      make(chan struct{}),
	}
}

func (t *Tx) getStatus() Status {
	t.smu.RLock()
	defer t.smu.RUnlock()
	return t.status
}

func (t *Tx) setStatus(s Status) {
	t.smu.Lock()
	t.status = s
	t.smu.Unlock()
}

func (t *Tx) isAwaiting() bool {
	t.smu.RLock()
	defer t.smu.RUnlock()
	return t.awaiting
}

func (t *Tx) setAwaiting(b bool) {
	t.smu.Lock()
	t.awaiting = b
	t.smu.Unlock()
}

// apply folds one journaled event into the state machine. It is used
// both during recovery replay and live execution, guaranteeing the two
// paths can never diverge.
func (t *Tx) apply(ev *Event) {
	switch ev.Type {
	case EvTxStarted:
		t.defName = ev.Def
		if ev.Data != nil {
			t.data = ev.Data
		}
		t.setStatus(StatusRunning)
	case EvStepStarted:
		t.started[ev.Idx] = true
	case EvStepCompleted, EvCallbackReceived:
		t.completed[ev.Idx] = true
		t.outputs[ev.Idx] = ev.Output
		t.next = ev.Idx + 1
		t.setAwaiting(false)
		if ev.Data != nil {
			t.data = ev.Data
		}
		t.setStatus(StatusRunning)
	case EvAwaitingCallback:
		t.setAwaiting(true)
		t.deadline = ev.Deadline
		t.setStatus(StatusWaiting)
	case EvStepFailed, EvStepTimedOut:
		t.setAwaiting(false)
		t.compensating = true
		t.compNext = ev.Idx - 1
		t.failReason = ev.Err
		t.setStatus(StatusCompensating)
	case EvCompensateStarted:
		t.compNext = ev.Idx
	case EvCompensateCompleted:
		t.compDone[ev.Idx] = true
		t.compNext = ev.Idx - 1
		if ev.Data != nil {
			t.data = ev.Data
		}
	case EvTxCompleted:
		t.setStatus(StatusCompleted)
	case EvTxCompensated:
		t.setStatus(StatusCompensated)
	case EvTxFailed:
		t.setStatus(StatusFailed)
	}
}

// append journals an event (fsync), folds it into the state machine
// and notifies the audit hook.
func (t *Tx) append(ev Event) {
	ev.TxID = t.id
	ev.Time = time.Now()
	if err := t.journal.append(&ev); err != nil {
		return // journal already closed: engine is shutting down
	}
	t.apply(&ev)
	if t.eng.onEvent != nil {
		t.eng.onEvent(ev)
	}
	if t.eng.crashHook != nil && t.eng.crashHook(ev) {
		panic(errCrash)
	}
}

// loop is the transaction runner goroutine.
func (t *Tx) loop() {
	for {
		if t.getStatus().Terminal() {
			t.finish()
			return
		}
		select {
		case <-t.stopCh:
			return
		default:
		}
		switch {
		case t.compensating:
			if t.compensateStep() {
				continue
			}
			if t.getStatus().Terminal() {
				continue // compensation itself failed -> FAILED
			}
			t.append(Event{Type: EvTxCompensated})
		case t.isAwaiting():
			if !t.waitCallback() {
				return // engine stopped while parked
			}
		case t.next < len(t.def.Steps):
			if !t.runStep(t.next) {
				return // engine stopped mid-step
			}
		default:
			t.append(Event{Type: EvTxCompleted})
		}
	}
}

func (t *Tx) finish() {
	t.doneOnce.Do(func() { close(t.done) })
}

// runStep executes one forward step. Returns false if the engine was
// stopped while the external call was in flight.
func (t *Tx) runStep(idx int) bool {
	step := t.def.Steps[idx]
	key := t.id + ":" + step.Name
	if !t.started[idx] {
		t.append(Event{Type: EvStepStarted, Idx: idx, Step: step.Name, Key: key})
	}
	ctx := &StepContext{TxID: t.id, Step: step.Name, IdempotencyKey: key, Data: t.data}
	out, err := step.Action(ctx)
	select {
	case <-t.stopCh:
		return false
	default:
	}
	switch {
	case err == ErrAwait:
		var deadline time.Time
		if step.Timeout > 0 {
			deadline = time.Now().Add(step.Timeout)
		}
		t.append(Event{Type: EvAwaitingCallback, Idx: idx, Step: step.Name, Deadline: deadline})
	case err != nil:
		t.append(Event{Type: EvStepFailed, Idx: idx, Step: step.Name, Err: err.Error()})
	default:
		t.append(Event{Type: EvStepCompleted, Idx: idx, Step: step.Name, Output: out, Data: t.data})
	}
	return true
}

// waitCallback parks until the external callback arrives, the
// persisted deadline fires, or the engine stops. Returns false only
// when the engine stopped.
func (t *Tx) waitCallback() bool {
	var timeout <-chan time.Time
	var timer *time.Timer
	if !t.deadline.IsZero() {
		d := time.Until(t.deadline)
		if d < 0 {
			d = 0 // deadline already passed while we were down: fire now
		}
		timer = time.NewTimer(d)
		timeout = timer.C
		defer timer.Stop()
	}
	idx := t.next
	name := t.def.Steps[idx].Name
	select {
	case payload := <-t.awaitCh:
		t.append(Event{Type: EvCallbackReceived, Idx: idx, Step: name, Output: payload, Data: t.data})
		return true
	case <-timeout:
		t.append(Event{Type: EvStepTimedOut, Idx: idx, Step: name, Err: ErrTimeout.Error()})
		return true
	case <-t.stopCh:
		return false
	}
}

// compensateStep runs one reverse step. Returns true while work
// remains, false when compensation is finished (or terminally failed).
func (t *Tx) compensateStep() bool {
	for t.compNext >= 0 {
		idx := t.compNext
		step := t.def.Steps[idx]
		if t.compDone[idx] || !t.completed[idx] || step.Compensate == nil {
			t.compNext--
			continue
		}
		key := t.id + ":" + step.Name
		t.append(Event{Type: EvCompensateStarted, Idx: idx, Step: step.Name, Key: key})
		ctx := &StepContext{TxID: t.id, Step: step.Name, IdempotencyKey: key, Data: t.data}
		err := step.Compensate(ctx)
		select {
		case <-t.stopCh:
			return true // paused; recovery retries this step with the same key
		default:
		}
		if err != nil {
			t.append(Event{Type: EvTxFailed, Idx: idx, Step: step.Name, Err: err.Error()})
			return false
		}
		t.append(Event{Type: EvCompensateCompleted, Idx: idx, Step: step.Name, Data: t.data})
		return true
	}
	return false
}
