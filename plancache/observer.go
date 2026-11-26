package plancache

import "sync"

// Observer receives observable lifecycle events. Operator callbacks are the
// security-critical signal: any rejected, stale or immediately invalidated
// plan must produce zero OperatorExecuted events.
type Observer interface {
	PlanCompiled(planID string, keyHash string)
	CacheHit(planID string, keyHash string)
	CacheMiss(keyHash string)
	RevalidationFailed(planID string, reason string)
	Recompiled(oldPlanID string, newPlanID string, keyHash string)
	ImmediateFailure(planID string, eventType string)
	HandleInvalidated(handleID string)
	OperatorExecuted(planID string, operatorName string, index int)
}

// TraceObserver is a thread-safe in-memory Observer used by hosts and tests
// to assert exact execution behavior.
type TraceObserver struct {
	mu     sync.Mutex
	events []string
	ops    []string
}

func (o *TraceObserver) record(event string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.events = append(o.events, event)
}

func (o *TraceObserver) PlanCompiled(planID string, keyHash string) {
	o.record("compiled:" + planID)
}

func (o *TraceObserver) CacheHit(planID string, keyHash string) {
	o.record("hit:" + planID)
}

func (o *TraceObserver) CacheMiss(keyHash string) {
	o.record("miss:" + keyHash)
}

func (o *TraceObserver) RevalidationFailed(planID string, reason string) {
	o.record("revalidation-failed:" + planID + ":" + reason)
}

func (o *TraceObserver) Recompiled(oldPlanID string, newPlanID string, keyHash string) {
	o.record("recompiled:" + oldPlanID + "->" + newPlanID)
}

func (o *TraceObserver) ImmediateFailure(planID string, eventType string) {
	o.record("immediate-failure:" + planID + ":" + eventType)
}

func (o *TraceObserver) HandleInvalidated(handleID string) {
	o.record("handle-invalidated:" + handleID)
}

func (o *TraceObserver) OperatorExecuted(planID string, operatorName string, index int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.events = append(o.events, "operator:"+planID+":"+operatorName)
	o.ops = append(o.ops, planID+"/"+operatorName)
}

// Events returns a copy of all lifecycle events in occurrence order.
func (o *TraceObserver) Events() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	out := make([]string, len(o.events))
	copy(out, o.events)
	return out
}

// ExecutedOperators returns a copy of every operator callback that actually
// ran, in occurrence order.
func (o *TraceObserver) ExecutedOperators() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	out := make([]string, len(o.ops))
	copy(out, o.ops)
	return out
}

// OperatorCount returns how many operator callbacks have run.
func (o *TraceObserver) OperatorCount() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.ops)
}

// Reset clears all recorded events.
func (o *TraceObserver) Reset() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.events = nil
	o.ops = nil
}
