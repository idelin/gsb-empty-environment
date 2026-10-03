package paxos

import (
	"bytes"
	"testing"
)

func tickAll(nodes []*Node, n int) {
	for t := 0; t < n; t++ {
		for _, nd := range nodes {
			nd.Tick()
		}
	}
}

// tickUntilLeader ticks the cluster until some node is a lease-valid leader.
func tickUntilLeader(nodes []*Node, maxTicks int) *Node {
	for i := 0; i < maxTicks; i++ {
		tickAll(nodes, 1)
		for _, n := range nodes {
			if n.LeaseValid() {
				return n
			}
		}
	}
	return nil
}

func leaderAmong(nodes []*Node, ids ...int) *Node {
	for _, id := range ids {
		if nodes[id].LeaseValid() {
			return nodes[id]
		}
	}
	return nil
}

func mustPropose(t *testing.T, leader *Node, cmd string) int {
	t.Helper()
	idx, err := leader.Propose([]byte(cmd))
	if err != nil {
		t.Fatalf("Propose(%q) failed: %v", cmd, err)
	}
	return idx
}

// assertNoHoles verifies the no-hole commit invariant on one node: the
// committed prefix contains no placeholder holes and the applied state
// machine output matches the committed log commands exactly, in order.
func assertNoHoles(t *testing.T, st Status) {
	t.Helper()
	if len(st.Applied) != st.CommitIndex {
		t.Fatalf("node %d: applied len %d != commitIndex %d", st.ID, len(st.Applied), st.CommitIndex)
	}
	for i := 0; i < st.CommitIndex; i++ {
		if i >= len(st.Log) {
			t.Fatalf("node %d: commitIndex %d beyond log len %d", st.ID, st.CommitIndex, len(st.Log))
		}
		if st.Log[i].isHole() {
			t.Fatalf("node %d: hole at committed index %d", st.ID, i)
		}
		if !bytes.Equal(st.Applied[i], st.Log[i].Cmd) {
			t.Fatalf("node %d: applied[%d]=%q != log cmd %q", st.ID, i, st.Applied[i], st.Log[i].Cmd)
		}
	}
}

// assertCommittedPrefixesAgree verifies that committed prefixes of all nodes
// are pairwise identical (no two nodes ever commit different commands at the
// same index).
func assertCommittedPrefixesAgree(t *testing.T, nodes []*Node) {
	t.Helper()
	sts := make([]Status, len(nodes))
	for i, n := range nodes {
		sts[i] = n.Snapshot()
		assertNoHoles(t, sts[i])
	}
	for i := 0; i < len(sts); i++ {
		for j := i + 1; j < len(sts); j++ {
			k := sts[i].CommitIndex
			if sts[j].CommitIndex < k {
				k = sts[j].CommitIndex
			}
			for idx := 0; idx < k; idx++ {
				if !bytes.Equal(sts[i].Applied[idx], sts[j].Applied[idx]) {
					t.Fatalf("committed divergence at index %d: node %d has %q, node %d has %q",
						idx, sts[i].ID, sts[i].Applied[idx], sts[j].ID, sts[j].Applied[idx])
				}
			}
		}
	}
}

// assertConverged verifies that after settling, every node applied exactly
// the want commands in order.
func assertConverged(t *testing.T, nodes []*Node, want []string) {
	t.Helper()
	assertCommittedPrefixesAgree(t, nodes)
	for _, n := range nodes {
		st := n.Snapshot()
		if len(st.Applied) != len(want) {
			t.Fatalf("node %d: applied %d entries, want %d (%v)", st.ID, len(st.Applied), len(want), want)
		}
		for i, w := range want {
			if string(st.Applied[i]) != w {
				t.Fatalf("node %d: applied[%d]=%q, want %q", st.ID, i, st.Applied[i], w)
			}
		}
	}
}

func appliedStrings(st Status) []string {
	out := make([]string, len(st.Applied))
	for i, a := range st.Applied {
		out[i] = string(a)
	}
	return out
}
