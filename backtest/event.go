package backtest

import "time"

// EventType classifies events flowing through the engine.
type EventType int

const (
	// EventMarketData is a regular observation (quote, trade, bar, ...).
	EventMarketData EventType = iota
	// EventCorrection is a late-arriving correction that supersedes the
	// payload of a previously published event (CorrectsID).
	EventCorrection
)

func (t EventType) String() string {
	switch t {
	case EventMarketData:
		return "MarketData"
	case EventCorrection:
		return "Correction"
	}
	return "Unknown"
}

// Event is the unit of information in the backtest.
//
// EventTime is when the fact happened in the real world; ArrivalTime is
// when the strategy is allowed to learn about it. The engine guarantees a
// strategy never observes an event before its ArrivalTime.
type Event struct {
	ID          string
	Seq         int64 // input sequence number, assigned from input order
	Type        EventType
	EventTime   time.Time
	ArrivalTime time.Time
	CorrectsID  string // set only for EventCorrection
	Payload     interface{}
}

// orderingKey implements the engine's dispatch order:
// primary ArrivalTime, tie-broken by input sequence number.
func (e Event) orderingKey() (time.Time, int64) {
	return e.ArrivalTime, e.Seq
}
