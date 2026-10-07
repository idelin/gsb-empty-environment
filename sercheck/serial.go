package sercheck

import "errors"

// ErrEnumLimit is returned when the number of committed transactions
// exceeds the enumeration limit.
var ErrEnumLimit = errors.New("sercheck: too many committed transactions to enumerate")

// EnumerateSerial performs a finite enumeration of serial executions:
// it tries every permutation of the committed transactions (up to limit
// transactions) and simulates each one, applying every transaction's
// recorded operations in order against a single-version state.
//
// A permutation is a witness iff every recorded read observes exactly
// the version (and value) it recorded: the read's ReadFrom must equal
// the writer of the current version of the key. It returns the first
// witness found, or ok=false if no serial execution reproduces the
// observed reads.
//
// Enumeration is exponential; keep limit modest (e.g. 10).
func (h *History) EnumerateSerial(limit int) (order []TxnID, ok bool, err error) {
	txns := h.CommittedTxns()
	if limit > 0 && len(txns) > limit {
		return nil, false, ErrEnumLimit
	}

	opsByTxn := make(map[TxnID][]Op)
	for _, op := range h.ops {
		if h.committed[op.Txn] {
			opsByTxn[op.Txn] = append(opsByTxn[op.Txn], op)
		}
	}

	type state struct {
		value  map[Key]Value
		writer map[Key]TxnID
	}
	initial := state{value: make(map[Key]Value), writer: make(map[Key]TxnID)}
	for k, v := range h.initial {
		initial.value[k] = v
	}

	// apply returns the successor state, or ok=false if a recorded read
	// contradicts the serial execution so far.
	apply := func(s state, txn TxnID) (state, bool) {
		next := state{value: make(map[Key]Value, len(s.value)), writer: make(map[Key]TxnID, len(s.writer))}
		for k, v := range s.value {
			next.value[k] = v
		}
		for k, w := range s.writer {
			next.writer[k] = w
		}
		for _, op := range opsByTxn[txn] {
			if op.Kind == OpRead {
				if next.writer[op.Key] != op.ReadFrom || next.value[op.Key] != op.Value {
					return state{}, false
				}
			} else {
				next.value[op.Key] = op.Value
				next.writer[op.Key] = op.Txn
			}
		}
		return next, true
	}

	used := make([]bool, len(txns))
	prefix := make([]TxnID, 0, len(txns))
	var dfs func(s state) bool
	dfs = func(s state) bool {
		if len(prefix) == len(txns) {
			return true
		}
		for i, t := range txns {
			if used[i] {
				continue
			}
			next, ok := apply(s, t)
			if !ok {
				continue // prune: this txn cannot run next
			}
			used[i] = true
			prefix = append(prefix, t)
			if dfs(next) {
				return true
			}
			prefix = prefix[:len(prefix)-1]
			used[i] = false
		}
		return false
	}

	if dfs(initial) {
		return append([]TxnID(nil), prefix...), true, nil
	}
	return nil, false, nil
}
