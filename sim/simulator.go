package sim

import (
	"fmt"
	"sort"

	"mvreg/mvreg"
)

type eventKind int

const (
	writeEvent eventKind = iota
	resolveEvent
	sendEvent
	failEvent
	recoverEvent
	tickEvent
)

// Event is one controlled input or network/fault action in the simulation.
type Event[T any] struct {
	Time   int64
	Kind   Kind
	Target string
	To     string
	Value  T
}

// Kind identifies an event. Its zero value is Write.
type Kind int

const (
	Write   Kind = Kind(writeEvent)
	Resolve Kind = Kind(resolveEvent)
	Send    Kind = Kind(sendEvent)
	Fail    Kind = Kind(failEvent)
	Recover Kind = Kind(recoverEvent)
	Tick    Kind = Kind(tickEvent)
)

type pendingMessage[T any] struct {
	from      string
	to        string
	deliverAt int64
	snapshot  mvreg.Snapshot[T]
}

// LogEntry is an observable, deterministic simulator result.
type LogEntry struct {
	Time    int64
	Replica string
	Message string
}

// Simulator runs replicas and a lossless-but-controllable network in one process.
type Simulator[T any] struct {
	time      int64
	replicas  map[string]*replicaState[T]
	pending   []pendingMessage[T]
	log       []LogEntry
	sendDelay int64
}

type replicaState[T any] struct {
	register *mvreg.Register[T]
	up       bool
}

func New[T any](replicaIDs []string, sendDelay int64) *Simulator[T] {
	if sendDelay < 0 {
		panic("sendDelay must be non-negative")
	}
	s := &Simulator[T]{
		time:      0,
		replicas:  make(map[string]*replicaState[T], len(replicaIDs)),
		sendDelay: sendDelay,
	}
	for _, id := range replicaIDs {
		s.replicas[id] = &replicaState[T]{
			register: mvreg.New[T](id),
			up:       true,
		}
	}
	return s
}

// Run applies events in (time, insertion order) and delivers due messages at each time.
func (s *Simulator[T]) Run(events []Event[T]) []LogEntry {
	s.log = nil
	events = append([]Event[T](nil), events...)
	sort.SliceStable(events, func(i, j int) bool {
		return events[i].Time < events[j].Time
	})

	nextIndex := 0
	for nextIndex < len(events) || len(s.pending) > 0 {
		var nextEventTime int64
		hasEventTime := nextIndex < len(events)
		if hasEventTime {
			nextEventTime = events[nextIndex].Time
		}
		nextDeliveryTime, hasDeliveryTime := s.nextDeliveryTime(s.time)

		switch {
		case hasEventTime && hasDeliveryTime:
			s.time = minInt64(nextEventTime, nextDeliveryTime)
		case hasEventTime:
			s.time = nextEventTime
		default:
			s.time = nextDeliveryTime
		}

		s.deliverDue()
		for nextIndex < len(events) && events[nextIndex].Time == s.time {
			s.apply(events[nextIndex])
			nextIndex++
		}
		s.deliverDue()
	}
	return append([]LogEntry(nil), s.log...)
}

func (s *Simulator[T]) apply(event Event[T]) {
	state, exists := s.replicas[event.Target]
	if !exists {
		panic(fmt.Sprintf("unknown replica %q", event.Target))
	}

	switch event.Kind {
	case Write:
		if !state.up {
			s.record(event.Target, "write rejected: replica down")
			return
		}
		item := state.register.Write(event.Value)
		s.record(event.Target, fmt.Sprintf("write %v at %v", item.Value, vectorString(item.Vector)))
	case Resolve:
		if !state.up {
			s.record(event.Target, "resolve rejected: replica down")
			return
		}
		item := state.register.Resolve(event.Value)
		s.record(event.Target, fmt.Sprintf("resolve %v at %v", item.Value, vectorString(item.Vector)))
	case Send:
		if !state.up {
			s.record(event.Target, "send rejected: replica down")
			return
		}
		if _, exists := s.replicas[event.To]; !exists {
			panic(fmt.Sprintf("unknown destination %q", event.To))
		}
		s.pending = append(s.pending, pendingMessage[T]{
			from:      event.Target,
			to:        event.To,
			deliverAt: s.time + s.sendDelay,
			snapshot:  state.register.Snapshot(),
		})
		s.record(event.Target, fmt.Sprintf("send to %s scheduled at t=%d", event.To, s.time+s.sendDelay))
	case Fail:
		state.up = false
		s.record(event.Target, "failed")
	case Recover:
		state.up = true
		s.record(event.Target, "recovered")
	case Tick:
		s.record(event.Target, "tick")
	default:
		panic(fmt.Sprintf("unknown event kind %d", event.Kind))
	}
}

func (s *Simulator[T]) deliverDue() {
	for {
		index := s.firstDeliverableIndex()
		if index < 0 {
			return
		}
		message := s.pending[index]
		s.pending = append(s.pending[:index], s.pending[index+1:]...)
		state := s.replicas[message.to]
		state.register.MergeSnapshot(message.snapshot)
		values := valuesString(state.register.Read())
		s.record(message.to, fmt.Sprintf("received state from %s; values=%s", message.from, values))
	}
}

func (s *Simulator[T]) firstDeliverableIndex() int {
	first := -1
	for i, message := range s.pending {
		if message.deliverAt <= s.time && s.replicas[message.to].up && (first < 0 || i < first) {
			first = i
		}
	}
	return first
}

func (s *Simulator[T]) nextDeliveryTime(now int64) (int64, bool) {
	next := int64(0)
	found := false
	for _, message := range s.pending {
		state := s.replicas[message.to]
		if !state.up && message.deliverAt <= now {
			continue
		}
		if !found || message.deliverAt < next {
			next = message.deliverAt
			found = true
		}
	}
	return next, found
}

func (s *Simulator[T]) record(replica, message string) {
	s.log = append(s.log, LogEntry{
		Time:    s.time,
		Replica: replica,
		Message: message,
	})
}

func (s *Simulator[T]) Read(replicaID string) []mvreg.Item[T] {
	state, exists := s.replicas[replicaID]
	if !exists {
		panic(fmt.Sprintf("unknown replica %q", replicaID))
	}
	return state.register.Read()
}

func minInt64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func vectorString(v mvreg.VersionVector) string {
	ids := make([]string, 0, len(v))
	for id := range v {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := "{"
	for i, id := range ids {
		if i > 0 {
			out += ","
		}
		out += fmt.Sprintf("%s:%d", id, v[id])
	}
	return out + "}"
}

func valuesString[T any](items []mvreg.Item[T]) string {
	if len(items) == 0 {
		return "[]"
	}
	out := "["
	for i, item := range items {
		if i > 0 {
			out += ","
		}
		out += fmt.Sprintf("%v", item.Value)
	}
	return out + "]"
}
