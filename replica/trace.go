package replica

import (
	"fmt"
	"sync"
)

// Step kinds in a recorded event trace.
const (
	StepAppend = "append"
	StepCrash  = "crash" // power loss: discard replica, recover from disk
)

// RecordedStep is one event in a trace: an append request with its
// observed outcome, or a crash/restart marker.
type RecordedStep struct {
	Kind  string         `json:"kind"`
	Req   *AppendRequest `json:"req,omitempty"`
	Reply *AppendReply   `json:"reply,omitempty"`
	Err   string         `json:"err,omitempty"`
}

// Recorder captures the events a replica observes, in order.
type Recorder struct {
	mu    sync.Mutex
	Steps []RecordedStep `json:"steps"`
}

func (rec *Recorder) Add(kind string, req *AppendRequest, rep *AppendReply, errStr string) {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	rec.Steps = append(rec.Steps, RecordedStep{Kind: kind, Req: req, Reply: rep, Err: errStr})
}

// RecordCrash appends a crash marker to the trace.
func (rec *Recorder) RecordCrash() { rec.Add(StepCrash, nil, nil, "") }

// Replay re-executes a trace against a fresh replica rooted at dir and
// returns the outcomes actually observed. Crash steps close the current
// replica and recover a new one from the durable store in the same dir.
func Replay(dir string, steps []RecordedStep) ([]RecordedStep, error) {
	var out []RecordedStep
	var r *Replica
	open := func() error {
		st, err := OpenStore(dir, nil)
		if err != nil {
			return err
		}
		r, err = OpenReplica(st)
		return err
	}
	if err := open(); err != nil {
		return nil, err
	}
	for _, s := range steps {
		switch s.Kind {
		case StepCrash:
			r = nil // in-memory state is lost
			if err := open(); err != nil {
				return nil, err
			}
			out = append(out, RecordedStep{Kind: StepCrash})
		case StepAppend:
			rep, err := r.HandleAppend(*s.Req)
			errStr := ""
			if err != nil {
				errStr = err.Error()
			}
			repCopy := rep
			out = append(out, RecordedStep{Kind: StepAppend, Req: s.Req, Reply: &repCopy, Err: errStr})
		default:
			return nil, fmt.Errorf("unknown step kind %q", s.Kind)
		}
	}
	return out, nil
}

// DiffSteps compares two traces and returns human-readable mismatches.
func DiffSteps(want, got []RecordedStep) []string {
	var diffs []string
	if len(want) != len(got) {
		diffs = append(diffs, fmt.Sprintf("step count: want %d, got %d", len(want), len(got)))
	}
	for i := 0; i < len(want) && i < len(got); i++ {
		w, g := want[i], got[i]
		if w.Kind != g.Kind {
			diffs = append(diffs, fmt.Sprintf("step %d: kind want %q got %q", i, w.Kind, g.Kind))
			continue
		}
		if w.Kind != StepAppend {
			continue
		}
		if w.Err != g.Err {
			diffs = append(diffs, fmt.Sprintf("step %d: err want %q got %q", i, w.Err, g.Err))
		}
		if (w.Reply == nil) != (g.Reply == nil) {
			diffs = append(diffs, fmt.Sprintf("step %d: reply presence differs", i))
			continue
		}
		if w.Reply != nil && *w.Reply != *g.Reply {
			diffs = append(diffs, fmt.Sprintf("step %d: reply want %+v got %+v", i, *w.Reply, *g.Reply))
		}
	}
	return diffs
}
