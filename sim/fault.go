package sim

import "errors"

const (
	FaultBeforeOrderCommit = "before_order_commit"
	FaultAfterOrderAck     = "after_order_ack"
	FaultAfterOutboxSend   = "after_outbox_send"
	FaultBeforeEventCommit = "before_event_commit"
)

var (
	ErrCrash        = errors.New("simulated process crash")
	ErrResponseLost = errors.New("response was lost after durable commit")
)

type Fault struct {
	Point     string
	MessageID string
}

type FaultInjector struct {
	faults []Fault
	hits   map[int]bool
}

func NewFaultInjector(faults ...Fault) *FaultInjector {
	return &FaultInjector{faults: faults, hits: make(map[int]bool)}
}

func (f *FaultInjector) Hit(point, messageID string) error {
	for i, fault := range f.faults {
		if f.hits[i] || fault.Point != point || fault.MessageID != messageID {
			continue
		}
		f.hits[i] = true
		if point == FaultAfterOrderAck {
			return ErrResponseLost
		}
		return ErrCrash
	}
	return nil
}

func (f *FaultInjector) HitCount(point, messageID string) int {
	count := 0
	for i, fault := range f.faults {
		if fault.Point == point && fault.MessageID == messageID && f.hits[i] {
			count++
		}
	}
	return count
}
