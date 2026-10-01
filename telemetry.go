package recvq

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
)

// Telemetry event types emitted locally by the Receiver.
const (
	EventEnqueue = "enqueue"
	EventProcess = "process"
	EventDrop    = "drop"
)

// Event is a locally synthesized telemetry record. Events are written as
// JSON lines so a run can be captured to a fixture file and replayed later.
type Event struct {
	Seq      int    `json:"seq"`
	Type     string `json:"type"` // enqueue | process | drop
	Record   string `json:"record"`
	Tenant   string `json:"tenant"`
	Priority string `json:"priority"`
	Reason   string `json:"reason,omitempty"`
}

// EventSink returns a sink that appends every event to a growing log.
func EventSink() (func(Event), *[]Event) {
	log := &[]Event{}
	return func(ev Event) { *log = append(*log, ev) }, log
}

// WriteEvents serializes events as JSON lines.
func WriteEvents(w io.Writer, events []Event) error {
	bw := bufio.NewWriter(w)
	for _, ev := range events {
		line, err := json.Marshal(ev)
		if err != nil {
			return err
		}
		if _, err := bw.Write(append(line, '\n')); err != nil {
			return err
		}
	}
	return bw.Flush()
}

// Replay folds a fixture of JSON-line events back into Stats and verifies
// conservation: every arrived record must end up processed, dropped, or
// still queued. It returns the reconstructed stats, the number of records
// left in queue at end of stream, and any conservation violation.
func Replay(r io.Reader) (Stats, int, error) {
	stats := newStats()
	enqueued := 0
	sc := bufio.NewScanner(r)
	line := 0
	for sc.Scan() {
		line++
		var ev Event
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			return stats, 0, fmt.Errorf("line %d: %v", line, err)
		}
		switch ev.Type {
		case EventEnqueue:
			stats.Arrived++
			stats.Enqueued++
			enqueued++
		case EventProcess:
			stats.Processed++
			enqueued--
		case EventDrop:
			stats.Dropped++
			stats.DropsByReason[ev.Reason]++
			switch ev.Reason {
			case ReasonQueueFull, ReasonClosed:
				// Rejected at arrival: never entered the queue.
				stats.Arrived++
			case ReasonEvicted, ReasonShutdown:
				// Was queued, now discarded.
				enqueued--
			default:
				return stats, 0, fmt.Errorf("line %d: unknown drop reason %q", line, ev.Reason)
			}
		default:
			return stats, 0, fmt.Errorf("line %d: unknown event type %q", line, ev.Type)
		}
		if enqueued < 0 {
			return stats, 0, fmt.Errorf("line %d: more records left the queue than entered it", line)
		}
	}
	if err := sc.Err(); err != nil {
		return stats, 0, err
	}
	if !stats.Conserved(enqueued) {
		return stats, enqueued, fmt.Errorf("conservation violated: %s, in_queue=%d", stats, enqueued)
	}
	return stats, enqueued, nil
}

// Synthesize deterministically builds a workload: n records spread over
// the given tenants. The first tenant is a "noisy" tenant emitting a
// sustained high-priority stream; all other records are normal priority
// with a seeded random sprinkle of high-priority ones.
func Synthesize(seed int64, n int, tenants []string) []Record {
	if len(tenants) == 0 {
		tenants = []string{"t1"}
	}
	rnd := rand.New(rand.NewSource(seed))
	recs := make([]Record, 0, n)
	for i := 0; i < n; i++ {
		tenant := tenants[i%len(tenants)]
		prio := Normal
		if tenant == tenants[0] || rnd.Intn(10) == 0 {
			prio = High
		}
		recs = append(recs, Record{
			ID:       fmt.Sprintf("r%05d", i),
			Tenant:   tenant,
			Priority: prio,
			Payload:  fmt.Sprintf("payload-%d", i),
		})
	}
	return recs
}
