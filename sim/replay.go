package sim

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

// Record is one applied op plus the rolling checksum observed after it.
type Record struct {
	Step uint64
	Op   Op
	Sum  uint64
}

// Recorder captures the op stream of a simulation run, in memory and
// (optionally) to a writer, so failures can be replayed and reproduced.
type Recorder struct {
	Records []Record
	w       *bufio.Writer
}

// NewRecorder returns a Recorder; w may be nil for in-memory only.
func NewRecorder(w io.Writer) *Recorder {
	r := &Recorder{}
	if w != nil {
		r.w = bufio.NewWriter(w)
	}
	return r
}

// Hook returns the Engine.Run hook that records every step.
func (r *Recorder) Hook() func(step uint64, op Op, sum uint64) {
	return func(step uint64, op Op, sum uint64) {
		rec := Record{Step: step, Op: op, Sum: sum}
		r.Records = append(r.Records, rec)
		if r.w != nil {
			fmt.Fprintf(r.w, "%d %d %d %d %d %d %d %d\n",
				rec.Step, rec.Op.Kind, rec.Op.Target, rec.Op.Other,
				rec.Op.Start, rec.Op.End, rec.Op.Count, rec.Sum)
		}
	}
}

// Flush flushes the underlying writer, if any.
func (r *Recorder) Flush() {
	if r.w != nil {
		r.w.Flush()
	}
}

// ParseLog reads a recorded op log.
func ParseLog(rd io.Reader) ([]Record, error) {
	var out []Record
	s := bufio.NewScanner(rd)
	s.Buffer(make([]byte, 1024*1024), 1024*1024)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		var rec Record
		var kind uint8
		_, err := fmt.Sscanf(line, "%d %d %d %d %d %d %d %d",
			&rec.Step, &kind, &rec.Op.Target, &rec.Op.Other,
			&rec.Op.Start, &rec.Op.End, &rec.Op.Count, &rec.Sum)
		if err != nil {
			return nil, fmt.Errorf("sim: bad log line %q: %v", line, err)
		}
		rec.Op.Kind = OpKind(kind)
		out = append(out, rec)
	}
	return out, s.Err()
}

// Fault describes the first divergence found during replay.
type Fault struct {
	Step     uint64
	Op       Op
	Expected uint64
	Actual   uint64
}

func (f *Fault) Error() string {
	return fmt.Sprintf("sim: divergence at step %d (%s target=%d other=%d [%d,%d) count=%d): "+
		"checksum expected %d, got %d",
		f.Step, f.Op.Kind, f.Op.Target, f.Op.Other, f.Op.Start, f.Op.End, f.Op.Count,
		f.Expected, f.Actual)
}

// Replay re-executes recs on a fresh engine and verifies every recorded
// checksum. It returns nil on success or the first Fault.
func Replay(seed int64, numBitmaps int, recs []Record) *Fault {
	e := NewEngine(seed, numBitmaps)
	for _, rec := range recs {
		e.apply(rec.Op)
		if e.sum != rec.Sum {
			return &Fault{Step: rec.Step, Op: rec.Op, Expected: rec.Sum, Actual: e.sum}
		}
	}
	return nil
}

// Reproduce builds a minimal, self-contained reproduction for a fault: the
// shortest op prefix that still triggers the divergence, plus instructions.
func Reproduce(seed int64, numBitmaps int, recs []Record, f *Fault) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# deterministic bug reproduction\n")
	fmt.Fprintf(&b, "# seed=%d bitmaps=%d failing_step=%d\n", seed, numBitmaps, f.Step)
	fmt.Fprintf(&b, "# expected checksum %d, got %d\n", f.Expected, f.Actual)
	fmt.Fprintf(&b, "# replay: sim.Replay(%d, %d, records[:%d])\n", seed, numBitmaps, f.Step)
	for _, rec := range recs {
		if rec.Step > f.Step {
			break
		}
		fmt.Fprintf(&b, "%d %d %d %d %d %d %d %d\n",
			rec.Step, rec.Op.Kind, rec.Op.Target, rec.Op.Other,
			rec.Op.Start, rec.Op.End, rec.Op.Count, rec.Sum)
	}
	return b.String()
}
