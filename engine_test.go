package seqstore

import (
	"math/rand"
	"testing"
)

type acct struct {
	table string
	key   string
}

func mustApply(t *testing.T, e *Engine, table string, seq uint64) {
	t.Helper()
	if err := e.Apply(table, seq); err != nil {
		t.Fatalf("apply table=%s seq=%d: %v", table, seq, err)
	}
}

func sumAll(t *testing.T, snap *Snapshot, accts []acct) int64 {
	t.Helper()
	var sum int64
	for _, a := range accts {
		v, ok := snap.Get(a.table, a.key)
		if !ok {
			t.Fatalf("missing key %s.%s", a.table, a.key)
		}
		sum += v
	}
	return sum
}

// A lagging table must cause Query to report the sequence to wait for
// instead of returning a partial cross-table transaction.
func TestNeedSeqInsteadOfPartial(t *testing.T) {
	e := NewEngine()
	// seq 1: open accounts on two tables.
	e.Commit(Write{"a", "acct1", 100}, Write{"b", "acct2", 100})
	// seq 2: transfer 40 across tables.
	e.Commit(Write{"a", "acct1", 60}, Write{"b", "acct2", 140})

	mustApply(t, e, "a", 1)
	mustApply(t, e, "b", 1)
	// Only table "a" applies the transfer; "b" lags.
	mustApply(t, e, "a", 2)

	_, err := e.Query(2, "a", "b")
	nse, ok := err.(*NeedSeqError)
	if !ok {
		t.Fatalf("expected NeedSeqError, got %v", err)
	}
	if nse.Table != "b" || nse.Required != 2 || nse.Applied != 1 {
		t.Fatalf("wrong NeedSeqError: %+v", nse)
	}

	// After "b" catches up, the full transaction is visible and conserved.
	mustApply(t, e, "b", 2)
	snap, err := e.Query(2, "a", "b")
	if err != nil {
		t.Fatalf("query after catch-up: %v", err)
	}
	accts := []acct{{"a", "acct1"}, {"b", "acct2"}}
	if got := sumAll(t, snap, accts); got != 200 {
		t.Fatalf("sum = %d, want 200", got)
	}
}

// Out-of-order application within one table must not leak a future commit
// before its predecessors are applied.
func TestOutOfOrderApplyGatedByWatermark(t *testing.T) {
	e := NewEngine()
	e.Commit(Write{"t", "k", 1})
	e.Commit(Write{"t", "k", 2})
	e.Commit(Write{"t", "k", 3})

	mustApply(t, e, "t", 1)
	mustApply(t, e, "t", 3) // applied early, but seq 2 missing
	if wm := e.Watermark("t"); wm != 1 {
		t.Fatalf("watermark = %d, want 1", wm)
	}
	if _, err := e.Query(3, "t"); err == nil {
		t.Fatal("query at seq 3 should be rejected while seq 2 is unapplied")
	}
	snap, err := e.Query(1, "t")
	if err != nil {
		t.Fatalf("query at seq 1: %v", err)
	}
	if v, _ := snap.Get("t", "k"); v != 1 {
		t.Fatalf("k at seq 1 = %d, want 1", v)
	}

	mustApply(t, e, "t", 2)
	if wm := e.Watermark("t"); wm != 3 {
		t.Fatalf("watermark = %d, want 3", wm)
	}
	snap, err = e.Query(3, "t")
	if err != nil {
		t.Fatalf("query at seq 3: %v", err)
	}
	if v, _ := snap.Get("t", "k"); v != 3 {
		t.Fatalf("k at seq 3 = %d, want 3", v)
	}
}

// Historical queries must observe past states exactly, even after newer
// commits have been applied.
func TestHistoricalQueries(t *testing.T) {
	e := NewEngine()
	e.Commit(Write{"t", "x", 10})                     // seq 1
	e.Commit(Write{"t", "x", 20})                     // seq 2
	e.Commit(Write{"t", "y", 7})                      // seq 3
	e.Commit(Write{"t", "x", 30}, Write{"t", "y", 8}) // seq 4
	for s := uint64(1); s <= 4; s++ {
		mustApply(t, e, "t", s)
	}

	cases := []struct {
		seq       uint64
		key       string
		want      int64
		wantFound bool
	}{
		{1, "x", 10, true},
		{2, "x", 20, true},
		{3, "x", 20, true},
		{4, "x", 30, true},
		{2, "y", 0, false},
		{3, "y", 7, true},
		{4, "y", 8, true},
	}
	for _, c := range cases {
		snap, err := e.Query(c.seq, "t")
		if err != nil {
			t.Fatalf("query seq %d: %v", c.seq, err)
		}
		got, found := snap.Get("t", c.key)
		if found != c.wantFound || (found && got != c.want) {
			t.Fatalf("seq %d key %s = (%d, %v), want (%d, %v)",
				c.seq, c.key, got, found, c.want, c.wantFound)
		}
	}
}

// Randomized model: cross-table transfers committed under a unified
// sequence, replicated to tables out of order with arbitrary lag. At no
// point may a query observe a partial transfer; once tables catch up,
// every historical sequence must show the conserved total and the exact
// per-account state of the reference model.
func TestOutOfOrderReplicationConservation(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	e := NewEngine()

	tables := []string{"checking", "savings", "brokerage"}
	var accts []acct
	var initWrites []Write
	for _, tbl := range tables {
		for i := 0; i < 4; i++ {
			key := tbl + string(rune('a'+i))
			accts = append(accts, acct{tbl, key})
			initWrites = append(initWrites, Write{tbl, key, 100})
		}
	}
	const total = int64(12 * 100)

	// Reference model: full balances as of each commit sequence.
	model := make(map[string]map[string]int64)
	var history []map[string]map[string]int64
	snapshotModel := func() {
		cp := make(map[string]map[string]int64, len(model))
		for tbl, m := range model {
			c2 := make(map[string]int64, len(m))
			for k, v := range m {
				c2[k] = v
			}
			cp[tbl] = c2
		}
		history = append(history, cp)
	}
	applyModel := func(ws []Write) {
		for _, w := range ws {
			if model[w.Table] == nil {
				model[w.Table] = make(map[string]int64)
			}
			model[w.Table][w.Key] = w.Value
		}
		snapshotModel()
	}

	e.Commit(initWrites...)
	applyModel(initWrites)

	// Pending replication: (table, seq) pairs not yet applied.
	type pending struct {
		table string
		seq   uint64
	}
	var queue []pending
	for _, tbl := range tables {
		queue = append(queue, pending{tbl, 1})
	}

	const rounds = 300
	for r := 0; r < rounds; r++ {
		// Commit a transfer between two random accounts (often cross-table).
		from, to := accts[rng.Intn(len(accts))], accts[rng.Intn(len(accts))]
		if from == to {
			continue
		}
		amount := int64(rng.Intn(50) + 1)
		ws := []Write{
			{from.table, from.key, model[from.table][from.key] - amount},
			{to.table, to.key, model[to.table][to.key] + amount},
		}
		seq := e.Commit(ws...)
		applyModel(ws)
		for _, tbl := range tables {
			queue = append(queue, pending{tbl, seq})
		}

		// Replicate a random subset of pending entries, in random order,
		// withholding the rest to simulate lag.
		rng.Shuffle(len(queue), func(i, j int) { queue[i], queue[j] = queue[j], queue[i] })
		keep := rng.Intn(len(queue) + 1)
		for _, p := range queue[keep:] {
			mustApply(t, e, p.table, p.seq)
		}
		queue = queue[:keep]

		// A query at the latest sequence either fails with NeedSeqError
		// naming a lagging table, or succeeds with the conserved total.
		snap, err := e.Query(seq, tables...)
		if err != nil {
			nse, ok := err.(*NeedSeqError)
			if !ok {
				t.Fatalf("round %d: unexpected error %v", r, err)
			}
			if nse.Required != seq {
				t.Fatalf("round %d: required = %d, want %d", r, nse.Required, seq)
			}
			if nse.Applied != e.Watermark(nse.Table) {
				t.Fatalf("round %d: applied = %d, watermark = %d", r, nse.Applied, e.Watermark(nse.Table))
			}
			continue
		}
		if got := sumAll(t, snap, accts); got != total {
			t.Fatalf("round %d: partial transaction observed, sum = %d, want %d", r, got, total)
		}
	}

	// Drain the remaining replication backlog.
	for _, p := range queue {
		mustApply(t, e, p.table, p.seq)
	}

	// Every historical sequence must match the reference model exactly and
	// conserve the total.
	final := e.Seq()
	for seq := uint64(1); seq <= final; seq++ {
		snap, err := e.Query(seq, tables...)
		if err != nil {
			t.Fatalf("historical query seq %d: %v", seq, err)
		}
		var sum int64
		for _, a := range accts {
			got, ok := snap.Get(a.table, a.key)
			if !ok {
				t.Fatalf("seq %d: missing %s.%s", seq, a.table, a.key)
			}
			want := history[seq-1][a.table][a.key]
			if got != want {
				t.Fatalf("seq %d: %s.%s = %d, model says %d", seq, a.table, a.key, got, want)
			}
			sum += got
		}
		if sum != total {
			t.Fatalf("seq %d: sum = %d, want %d", seq, sum, total)
		}
	}
}
