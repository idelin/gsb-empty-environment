package logrec

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
)

// Op is one step of a replayable fixture script. Fixtures drive a
// Multiplexer deterministically so runs can be compared byte-for-byte
// and telemetry can be diffed against expectations.
type Op struct {
	Do     string `json:"do"`               // line|chunk|close|flush|rules|snapshot
	Source string `json:"source,omitempty"` // stream source id
	Epoch  uint64 `json:"epoch,omitempty"`
	Seq    uint64 `json:"seq,omitempty"`   // for "line"
	Text   string `json:"text,omitempty"`  // for "line"
	Data   string `json:"data,omitempty"`  // for "chunk"
	Rules  string `json:"rules,omitempty"` // start-pattern regexp, for "rules"
}

// Result captures everything a replay produced.
type Result struct {
	Emissions []Emission `json:"emissions"`
	Events    []Event    `json:"events"`
	Snapshots [][]byte   `json:"-"`
	Errors    []string   `json:"errors,omitempty"`
}

// Replay executes ops against m and records every emission and
// telemetry event. "snapshot" ops store a checkpoint in the Result and
// restore it into a fresh multiplexer that continues the script,
// proving checkpoint/restore preserves state mid-stream.
func Replay(m *Multiplexer, ops []Op) Result {
	res := Result{}
	mark := len(m.Telemetry().Events())
	for i, op := range ops {
		var ems []Emission
		var err error
		switch op.Do {
		case "line":
			ems, err = m.FeedLine(op.Source, op.Epoch, op.Seq, op.Text)
		case "chunk":
			var n int
			ems, n, err = m.FeedChunk(op.Source, op.Epoch, []byte(op.Data))
			if err == ErrBackpressure && n < len(op.Data) {
				// Relieve pressure deterministically and retry once.
				m.FlushPending(op.Source, op.Epoch)
				var ems2 []Emission
				ems2, _, err = m.FeedChunk(op.Source, op.Epoch, []byte(op.Data)[n:])
				ems = append(ems, ems2...)
			}
		case "close":
			ems = m.Close(op.Source, op.Epoch)
		case "flush":
			ems = m.FlushPending(op.Source, op.Epoch)
		case "rules":
			m.SetRules(op.Source, op.Epoch, Rules{StartPattern: MustPattern(op.Rules)})
		case "snapshot":
			var data []byte
			data, err = m.Snapshot()
			if err == nil {
				res.Snapshots = append(res.Snapshots, data)
				m, err = Restore(data)
			}
		default:
			err = fmt.Errorf("unknown op %q", op.Do)
		}
		if err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("op %d (%s): %v", i, op.Do, err))
		}
		res.Emissions = append(res.Emissions, ems...)
	}
	res.Events = m.Telemetry().Events()[mark:]
	return res
}

// LoadFixture reads a JSON-lines fixture: one Op per line. Blank lines
// and lines starting with '#' are ignored.
func LoadFixture(r io.Reader) ([]Op, error) {
	var ops []Op
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		trimmed := false
		for _, b := range line {
			if b != ' ' && b != '\t' {
				trimmed = true
				break
			}
		}
		if !trimmed || len(line) == 0 || line[0] == '#' {
			continue
		}
		var op Op
		if err := json.Unmarshal(line, &op); err != nil {
			return nil, fmt.Errorf("logrec: fixture line: %w", err)
		}
		ops = append(ops, op)
	}
	return ops, sc.Err()
}
