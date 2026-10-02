// Command demo runs the acceptance scenario end to end: concurrent
// cross-partition transactions, out-of-order packet injection, auditable
// conflict evidence, and snapshot-based crash recovery.
package main

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"

	"kvsm"
)

func buildLog() *kvsm.Log {
	l := kvsm.NewLog()
	l.Append(kvsm.Event{Kind: kvsm.KindPrepare, TxnID: "T1", Parts: []int{0, 1},
		Writes: map[int]map[string]string{0: {"a": "v1", "b": "v1"}, 1: {"x": "v1"}}})
	l.Append(kvsm.Event{Kind: kvsm.KindPrepare, TxnID: "T2", Parts: []int{0, 1},
		Writes: map[int]map[string]string{0: {"a": "v2"}, 1: {"x": "v2", "y": "v2"}}})
	l.Append(kvsm.Event{Kind: kvsm.KindCommit, TxnID: "T2", Parts: []int{0, 1}})
	l.Append(kvsm.Event{Kind: kvsm.KindPrepare, TxnID: "T3", Parts: []int{1},
		Writes: map[int]map[string]string{1: {"z": "v3"}}})
	l.Append(kvsm.Event{Kind: kvsm.KindCommit, TxnID: "T1", Parts: []int{0, 1}})
	l.Append(kvsm.Event{Kind: kvsm.KindCommit, TxnID: "T3", Parts: []int{1}})
	return l
}

func printState(title string, state map[string]string) {
	keys := make([]string, 0, len(state))
	for k := range state {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	fmt.Println(title)
	for _, k := range keys {
		fmt.Printf("    %-4s = %s\n", k, state[k])
	}
}

func main() {
	log := buildLog()
	events := log.Events()

	fmt.Println("== deterministic log ==")
	for _, ev := range events {
		fmt.Printf("  seq=%d %-7s txn=%s parts=%v\n", ev.Seq, ev.Kind, ev.TxnID, ev.Parts)
	}

	// Reference: in-order replay.
	ref := kvsm.NewEngine(2, 0, 0)
	must("in-order replay", ref.DeliverAll(events))
	printState("== in-order final state ==", ref.State())

	// Out-of-order packet injection: shuffle delivery with several seeds.
	fmt.Println("== out-of-order delivery (injected reordering) ==")
	for seed := int64(1); seed <= 5; seed++ {
		shuffled := make([]kvsm.Event, len(events))
		for i, j := range rand.New(rand.NewSource(seed)).Perm(len(events)) {
			shuffled[i] = events[j]
		}
		e := kvsm.NewEngine(2, 0, 0)
		must(fmt.Sprintf("shuffled replay seed=%d", seed), e.DeliverAll(shuffled))
		fmt.Printf("  seed=%d identical=%v\n", seed, reflect.DeepEqual(e.State(), ref.State()))
	}

	// Auditable tie-break evidence for the T1/T2 conflict.
	fmt.Println("== conflict evidence (auditable) ==")
	for _, ev := range ref.Evidence() {
		fmt.Printf("  [%s] rule=%s winner=%s loser=%s part=%d key=%q\n      %s\n",
			ev.Action, ev.Rule, ev.Winner, ev.Loser, ev.Part, ev.Key, ev.Detail)
	}

	// Snapshot at a slice point, "crash", restore, replay the suffix.
	fmt.Println("== snapshot + crash recovery ==")
	const cut = 3
	head := kvsm.NewEngine(2, 0, 0)
	must("prefix replay", head.DeliverAll(events[:cut]))
	snap := head.Snapshot()
	fmt.Printf("  snapshot at LastSeq=%d, pending=%d txn(s)\n", snap.LastSeq, len(snap.Pending))
	must("truncate log", log.Truncate(snap.LastSeq))
	fmt.Printf("  log truncated: %d event(s) retained for replay\n", log.Len())

	recovered := kvsm.Restore(snap, 0, 0)
	must("suffix replay", recovered.DeliverAll(log.Events()))
	printState("== recovered final state ==", recovered.State())
	fmt.Printf("  recovered == full replay: %v\n", reflect.DeepEqual(recovered.State(), ref.State()))
}

func must(what string, err error) {
	if err != nil {
		panic(fmt.Sprintf("%s: %v", what, err))
	}
}
