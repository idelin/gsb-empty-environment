package netsec

import "sync"

// EventType classifies an observable cache/execution event.
type EventType string

const (
	EventCacheHit      EventType = "cache_hit"
	EventCacheMiss     EventType = "cache_miss"
	EventPlanCompiled  EventType = "plan_compiled"
	EventPlanValidated EventType = "plan_validated"
	EventPlanRejected  EventType = "plan_rejected"
	EventOperatorStart EventType = "operator_start"
	EventOperatorEnd   EventType = "operator_end"
)

// Event is delivered synchronously to observers. Operator events prove whether
// any execution primitive ran; rejected plans emit no operator events.
type Event struct {
	Type          EventType
	PlanKey       PlanKey
	PrincipalID   PrincipalID
	PolicyEpoch   uint64
	OperatorIndex int
	Operator      Operator
	Err           error
}

// Observer receives observable execution events.
type Observer interface {
	Observe(Event)
}

// ObserverFunc adapts a function to Observer.
type ObserverFunc func(Event)

func (f ObserverFunc) Observe(event Event) { f(event) }

// Executor is the optional primitive executor invoked after all pre-execution
// reviews pass. It receives only structurally bound parameters, never raw SQL
// concatenation.
type Executor interface {
	Execute(Operator, BoundParameters) error
}

// ExecutorFunc adapts a function to Executor.
type ExecutorFunc func(Operator, BoundParameters) error

func (f ExecutorFunc) Execute(operator Operator, params BoundParameters) error {
	return f(operator, params)
}

type observerSet struct {
	mu    sync.RWMutex
	items []Observer
}

func (s *observerSet) add(observer Observer) {
	if observer == nil {
		return
	}
	s.mu.Lock()
	s.items = append(s.items, observer)
	s.mu.Unlock()
}

func (s *observerSet) emit(event Event) {
	s.mu.RLock()
	items := append([]Observer(nil), s.items...)
	s.mu.RUnlock()
	for _, observer := range items {
		observer.Observe(event)
	}
}
