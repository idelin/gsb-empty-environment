// Package matrixclock implements matrix clocks for distributed garbage
// collection. Every node maintains not only its own vector clock, but a
// full matrix recording its knowledge of every other node's vector clock.
// The per-column minimum of the matrix yields the timestamp that every
// node in the cluster is known to have acknowledged, which is the safe
// watermark for physically truncating replicated message logs.
package matrixclock

// Vector is a vector clock: node ID -> logical timestamp.
type Vector map[string]uint64

// MatrixClock is the timing component of a node's knowledge:
// M[i][j] is the owner's knowledge of "node i's knowledge of node j's
// clock". The owner's own row is its vector clock.
type MatrixClock map[string]Vector

func (m MatrixClock) ensure(node string) Vector {
	row, ok := m[node]
	if !ok {
		row = make(Vector)
		m[node] = row
	}
	return row
}

// Increment advances the owner's own component (local or send event).
func (m MatrixClock) Increment(owner string) {
	row := m.ensure(owner)
	row[owner]++
}

// Merge folds a matrix snapshot received from `from` into this clock:
// the sender's vector row updates the owner's own vector clock, every
// row is merged element-wise (max), and the owner's own component is
// incremented for the receive event.
func (m MatrixClock) Merge(owner, from string, other MatrixClock) {
	if senderRow, ok := other[from]; ok {
		own := m.ensure(owner)
		for k, v := range senderRow {
			if k != owner && v > own[k] {
				own[k] = v
			}
		}
	}
	for node, row := range other {
		dst := m.ensure(node)
		for k, v := range row {
			if v > dst[k] {
				dst[k] = v
			}
		}
	}
	m.Increment(owner)
}

// Snapshot returns a deep copy of the matrix, safe to attach to a message.
func (m MatrixClock) Snapshot() MatrixClock {
	cp := make(MatrixClock, len(m))
	for node, row := range m {
		r := make(Vector, len(row))
		for k, v := range row {
			r[k] = v
		}
		cp[node] = r
	}
	return cp
}

// Acknowledged returns, for every known node j, the highest timestamp
// that ALL nodes are known to have received and processed:
// ack[j] = min over every row i of M[i][j]. A missing entry counts as 0.
func (m MatrixClock) Acknowledged() Vector {
	nodes := make(map[string]bool)
	for _, row := range m {
		for j := range row {
			nodes[j] = true
		}
	}
	ack := make(Vector, len(nodes))
	for j := range nodes {
		first := true
		var min uint64
		for _, row := range m {
			v := row[j]
			if first || v < min {
				min = v
				first = false
			}
		}
		ack[j] = min
	}
	return ack
}
