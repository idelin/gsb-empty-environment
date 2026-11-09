package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
)

// op is one surviving row mutation recorded by an open transaction.
type op struct {
	kind  string // "set" or "del"
	key   string
	field string // only meaningful for kind == "set"
	value string // only meaningful for kind == "set"
}

// savepoint marks a position in a transaction's op log. It stores an index
// rather than a snapshot, so taking a savepoint never copies any row/table.
type savepoint struct {
	name string
	at   int // number of ops present when the savepoint was taken
}

// txn holds the private, uncommitted state of one transaction.
type txn struct {
	ops        []op
	savepoints []savepoint
}

// Replay consumes interleaved CDC events and returns the final public table.
// Each event is a JSON array whose first two elements are the transaction id
// and action; remaining elements are action arguments.
func Replay(r io.Reader) (map[string]map[string]string, error) {
	var rawEvents [][]json.RawMessage
	if err := json.NewDecoder(r).Decode(&rawEvents); err != nil {
		return nil, fmt.Errorf("decode events: %w", err)
	}

	txns := map[string]*txn{}
	public := map[string]map[string]string{}

	for i, raw := range rawEvents {
		if len(raw) < 2 {
			return nil, fmt.Errorf("event %d: need at least [txnId, action]", i)
		}
		tid, err := decodeString(raw[0])
		if err != nil {
			return nil, fmt.Errorf("event %d: txn id: %w", i, err)
		}
		action, err := decodeString(raw[1])
		if err != nil {
			return nil, fmt.Errorf("event %d: action: %w", i, err)
		}
		args := raw[2:]

		switch action {
		case "set":
			if len(args) != 3 {
				return nil, fmt.Errorf("event %d: set needs [key, field, value]", i)
			}
			key, err := decodeString(args[0])
			if err != nil {
				return nil, fmt.Errorf("event %d: set key: %w", i, err)
			}
			field, err := decodeString(args[1])
			if err != nil {
				return nil, fmt.Errorf("event %d: set field: %w", i, err)
			}
			value, err := decodeString(args[2])
			if err != nil {
				return nil, fmt.Errorf("event %d: set value: %w", i, err)
			}
			t := getTxn(txns, tid)
			t.ops = append(t.ops, op{kind: "set", key: key, field: field, value: value})

		case "del":
			if len(args) != 1 {
				return nil, fmt.Errorf("event %d: del needs [key]", i)
			}
			key, err := decodeString(args[0])
			if err != nil {
				return nil, fmt.Errorf("event %d: del key: %w", i, err)
			}
			t := getTxn(txns, tid)
			t.ops = append(t.ops, op{kind: "del", key: key})

		case "save":
			if len(args) != 1 {
				return nil, fmt.Errorf("event %d: save needs [name]", i)
			}
			name, err := decodeString(args[0])
			if err != nil {
				return nil, fmt.Errorf("event %d: save name: %w", i, err)
			}
			t := getTxn(txns, tid)
			// Same-named savepoints may shadow earlier ones; the latest wins.
			t.savepoints = append(t.savepoints, savepoint{name: name, at: len(t.ops)})

		case "undo":
			if len(args) != 1 {
				return nil, fmt.Errorf("event %d: undo needs [name]", i)
			}
			name, err := decodeString(args[0])
			if err != nil {
				return nil, fmt.Errorf("event %d: undo name: %w", i, err)
			}
			t, ok := txns[tid]
			if !ok {
				return nil, fmt.Errorf("event %d: undo %q: transaction %q not active", i, name, tid)
			}
			idx := -1
			for j := len(t.savepoints) - 1; j >= 0; j-- {
				if t.savepoints[j].name == name {
					idx = j
					break
				}
			}
			if idx == -1 {
				return nil, fmt.Errorf("event %d: undo %q: savepoint not found in transaction %q", i, name, tid)
			}
			target := t.savepoints[idx]
			// Drop every op after the savepoint, keep the savepoint itself and
			// discard the savepoints created after it.
			t.ops = t.ops[:target.at]
			t.savepoints = t.savepoints[:idx+1]

		case "commit":
			if len(args) != 0 {
				return nil, fmt.Errorf("event %d: commit takes no arguments", i)
			}
			t, ok := txns[tid]
			if !ok {
				return nil, fmt.Errorf("event %d: commit: transaction %q not active", i, tid)
			}
			// Apply surviving ops one by one against the public table as it
			// stands now; never write back a private whole-row snapshot, so
			// concurrent commits to other fields are preserved.
			for _, o := range t.ops {
				applyOp(public, o)
			}
			delete(txns, tid)

		case "abort":
			if len(args) != 0 {
				return nil, fmt.Errorf("event %d: abort takes no arguments", i)
			}
			if _, ok := txns[tid]; !ok {
				return nil, fmt.Errorf("event %d: abort: transaction %q not active", i, tid)
			}
			delete(txns, tid)

		default:
			return nil, fmt.Errorf("event %d: unknown action %q", i, action)
		}
	}

	return public, nil
}

func getTxn(txns map[string]*txn, tid string) *txn {
	t, ok := txns[tid]
	if !ok {
		t = &txn{}
		txns[tid] = t
	}
	return t
}

func applyOp(table map[string]map[string]string, o op) {
	switch o.kind {
	case "set":
		row := table[o.key]
		if row == nil {
			row = map[string]string{}
			table[o.key] = row
		}
		row[o.field] = o.value
	case "del":
		delete(table, o.key)
	}
}

// decodeString accepts JSON strings as well as JSON numbers/bools so that
// numeric transaction ids work without forcing quoted input.
func decodeString(raw json.RawMessage) (string, error) {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s, nil
	}
	return string(raw), nil
}

func main() {
	table, err := Replay(os.Stdin)
	if err != nil {
		fmt.Fprintln(os.Stderr, "cdc-replay:", err)
		os.Exit(1)
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(table); err != nil {
		fmt.Fprintln(os.Stderr, "cdc-replay:", err)
		os.Exit(1)
	}
	io.Copy(os.Stdout, &buf)
}
