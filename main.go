package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
)

// op is a single pending row mutation recorded by an open transaction.
type op struct {
	kind  string // "set" | "del"
	row   string
	field string
	value string
}

// savepoint only remembers an offset into the transaction's op log, so taking
// a savepoint never copies (or even reads) the whole table.
type savepoint struct {
	name   string
	opMark int // length of the op log at the time the savepoint was taken
}

// txnState buffers one transaction's mutations until it commits or aborts.
type txnState struct {
	ops        []op
	savepoints []savepoint
}

// publicTable is the committed row image, keyed by row key and then field.
type publicTable map[string]map[string]string

// replay consumes interleaved transaction events and returns the final
// committed table. Each event is [txnID, action, args...].
func replay(events [][]string) (publicTable, error) {
	txns := map[string]*txnState{}
	table := publicTable{}

	for line, event := range events {
		if len(event) < 2 {
			return nil, fmt.Errorf("event %d: need at least txn id and action", line)
		}
		txnID, action := event[0], event[1]

		switch action {
		case "set":
			if len(event) != 5 {
				return nil, fmt.Errorf("event %d: set expects row, field, value", line)
			}
			t := getTxn(txns, txnID)
			t.ops = append(t.ops, op{
				kind:  "set",
				row:   event[2],
				field: event[3],
				value: event[4],
			})

		case "del":
			if len(event) != 3 {
				return nil, fmt.Errorf("event %d: del expects row key", line)
			}
			t := getTxn(txns, txnID)
			t.ops = append(t.ops, op{kind: "del", row: event[2]})

		case "save":
			if len(event) != 3 {
				return nil, fmt.Errorf("event %d: save expects savepoint name", line)
			}
			t := getTxn(txns, txnID)
			// Pushing another entry with the same name shadows earlier ones:
			// undo always resolves the nearest match from the top.
			t.savepoints = append(t.savepoints, savepoint{
				name:   event[2],
				opMark: len(t.ops),
			})

		case "undo":
			if len(event) != 3 {
				return nil, fmt.Errorf("event %d: undo expects savepoint name", line)
			}
			t := getTxn(txns, txnID)
			idx := -1
			for i := len(t.savepoints) - 1; i >= 0; i-- {
				if t.savepoints[i].name == event[2] {
					idx = i
					break
				}
			}
			if idx < 0 {
				return nil, fmt.Errorf("event %d: transaction %q has no savepoint %q", line, txnID, event[2])
			}
			matched := t.savepoints[idx]
			// Roll the op log back to the marked position, keep the matched
			// savepoint and discard every savepoint taken after it.
			t.ops = t.ops[:matched.opMark]
			t.savepoints = t.savepoints[:idx+1]

		case "commit":
			t, ok := txns[txnID]
			if !ok {
				return nil, fmt.Errorf("event %d: commit for unknown transaction %q", line, txnID)
			}
			// Apply surviving mutations in original order against the table as
			// it exists at commit time. set touches one field only, so a
			// concurrent commit can never be overwritten by a stale whole-row
			// snapshot; del removes the row entirely.
			for _, mutation := range t.ops {
				switch mutation.kind {
				case "set":
					row := table[mutation.row]
					if row == nil {
						row = map[string]string{}
						table[mutation.row] = row
					}
					row[mutation.field] = mutation.value
				case "del":
					delete(table, mutation.row)
				}
			}
			delete(txns, txnID)

		case "abort":
			// Everything the transaction buffered is discarded.
			delete(txns, txnID)

		default:
			return nil, fmt.Errorf("event %d: unknown action %q", line, action)
		}
	}

	return table, nil
}

func getTxn(txns map[string]*txnState, txnID string) *txnState {
	t, ok := txns[txnID]
	if !ok {
		t = &txnState{}
		txns[txnID] = t
	}
	return t
}

func run(stdin io.Reader, stdout io.Writer) error {
	var events [][]string
	if err := json.NewDecoder(stdin).Decode(&events); err != nil {
		return fmt.Errorf("decode stdin: %w", err)
	}
	table, err := replay(events)
	if err != nil {
		return err
	}
	out, err := json.MarshalIndent(table, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(stdout, string(out))
	return err
}

func main() {
	if err := run(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "cdc-replay:", err)
		os.Exit(1)
	}
}
