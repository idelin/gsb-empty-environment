package saga

import (
	"bufio"
	"encoding/json"
	"os"
)

// journal is an append-only, fsync'd JSON-lines file holding the full
// event history (audit trail) of one transaction.
type journal struct {
	f   *os.File
	seq int64
}

// openJournal opens (or creates) the journal at path and replays any
// existing events, returning them in order.
func openJournal(path string) (*journal, []Event, error) {
	var events []Event
	if rf, err := os.Open(path); err == nil {
		sc := bufio.NewScanner(rf)
		sc.Buffer(make([]byte, 1024*1024), 1024*1024)
		for sc.Scan() {
			line := sc.Bytes()
			if len(line) == 0 {
				continue
			}
			var ev Event
			if err := json.Unmarshal(line, &ev); err != nil {
				rf.Close()
				return nil, nil, err
			}
			events = append(events, ev)
		}
		rf.Close()
		if err := sc.Err(); err != nil {
			return nil, nil, err
		}
	} else if !os.IsNotExist(err) {
		return nil, nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, nil, err
	}
	j := &journal{f: f}
	for _, ev := range events {
		if ev.Seq > j.seq {
			j.seq = ev.Seq
		}
	}
	return j, events, nil
}

// append writes one event and fsyncs before returning, so a power loss
// after append can never lose the recorded transition.
func (j *journal) append(ev *Event) error {
	j.seq++
	ev.Seq = j.seq
	b, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	if _, err := j.f.Write(b); err != nil {
		return err
	}
	return j.f.Sync()
}

func (j *journal) close() { j.f.Close() }
