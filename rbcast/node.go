package rbcast

import "fmt"

// sourceWindow is the bounded dedup history a node keeps for one source.
//
// It combines:
//   - a continuous watermark: every seq <= watermark has been delivered, so
//     old retransmissions are recognised as duplicates with a single compare;
//   - a sparse gap table: seqs above the watermark that arrived out of order
//     and have already been delivered.
//
// The gap table is capped at maxGaps entries; messages too far ahead of the
// watermark are refused and must be retransmitted later.
type sourceWindow struct {
	watermark uint64
	gaps      map[uint64]struct{}
	maxGaps   int
}

func newSourceWindow(maxGaps int) *sourceWindow {
	return &sourceWindow{gaps: make(map[uint64]struct{}), maxGaps: maxGaps}
}

// Node is a broadcast subscriber with bounded dedup history.
type Node struct {
	ID         string
	maxGaps    int
	windows    map[string]*sourceWindow
	Delivered  []Message // ordered log of distinct delivered messages
	Duplicates int       // count of recognised duplicate deliveries
}

func NewNode(id string, maxGaps int) *Node {
	return &Node{ID: id, maxGaps: maxGaps, windows: make(map[string]*sourceWindow)}
}

func (n *Node) window(source string) *sourceWindow {
	w, ok := n.windows[source]
	if !ok {
		w = newSourceWindow(n.maxGaps)
		n.windows[source] = w
	}
	return w
}

// Receive ingests a message. It returns true exactly once per distinct
// message; duplicates (seq <= watermark, or present in the gap table) are
// recognised and dropped. Messages beyond the bounded gap table are refused
// (return false) so the source will retransmit them during catch-up.
func (n *Node) Receive(m Message) bool {
	w := n.window(m.Source)
	if m.Seq <= w.watermark {
		n.Duplicates++
		return false
	}
	if _, ok := w.gaps[m.Seq]; ok {
		n.Duplicates++
		return false
	}
	if m.Seq != w.watermark+1 && len(w.gaps) >= w.maxGaps {
		// Bounded history is full and this does not extend the
		// contiguous prefix: refuse, wait for retransmission.
		return false
	}
	if m.Seq == w.watermark+1 {
		w.watermark = m.Seq
		// Fold any contiguous gap entries into the watermark.
		for {
			if _, ok := w.gaps[w.watermark+1]; !ok {
				break
			}
			delete(w.gaps, w.watermark+1)
			w.watermark++
		}
	} else {
		w.gaps[m.Seq] = struct{}{}
	}
	n.Delivered = append(n.Delivered, m)
	return true
}

// Watermark returns the contiguous delivery watermark for a source.
func (n *Node) Watermark(source string) uint64 {
	return n.window(source).watermark
}

// GapSize returns the number of sparse entries above the watermark.
func (n *Node) GapSize(source string) int {
	return len(n.window(source).gaps)
}

func (n *Node) String() string {
	return fmt.Sprintf("node %s: delivered=%d dups=%d", n.ID, len(n.Delivered), n.Duplicates)
}
