// cutcheck computes the recoverable backup cut for a multi-partition log
// and prints evidence for every cross-partition transaction that forced the
// cut to retreat. With no arguments it runs a built-in demo scenario;
// otherwise it reads a JSON scenario file.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"os"

	"backupcut/checker"
)

type scenario struct {
	Partitions []string         `json:"partitions"`
	Txns       []txnSpec        `json:"txns"`
	Entries    []entrySpec      `json:"entries"`
	Target     map[string]int64 `json:"target"`
}

type txnSpec struct {
	ID           string   `json:"id"`
	Participants []string `json:"participants"`
}

type entrySpec struct {
	Partition string `json:"partition"`
	Kind      string `json:"kind"` // "data" | "commit" | "abort"
	Txn       string `json:"txn"`
	Key       string `json:"key,omitempty"`
	Value     string `json:"value,omitempty"`
	Version   int64  `json:"version"`
}

func buildLog(s scenario) (*checker.Log, error) {
	l := checker.NewLog()
	for _, p := range s.Partitions {
		l.AddPartition(p)
	}
	for _, t := range s.Txns {
		l.AddTxn(t.ID, t.Participants...)
	}
	for _, e := range s.Entries {
		var kind checker.EntryKind
		switch e.Kind {
		case "data":
			kind = checker.EntryData
		case "commit":
			kind = checker.EntryCommit
		case "abort":
			kind = checker.EntryAbort
		default:
			return nil, fmt.Errorf("unknown entry kind %q", e.Kind)
		}
		if _, ok := l.Partitions[e.Partition]; !ok {
			return nil, fmt.Errorf("unknown partition %q", e.Partition)
		}
		l.AddEntry(e.Partition, checker.Entry{
			Kind: kind, TxnID: e.Txn, Key: e.Key, Value: e.Value,
			Version: checker.Version(e.Version),
		})
	}
	l.Sort()
	if err := l.Validate(); err != nil {
		return nil, err
	}
	return l, nil
}

func demoScenario() scenario {
	return scenario{
		Partitions: []string{"us", "eu", "asia"},
		Txns: []txnSpec{
			{ID: "t1", Participants: []string{"us", "eu"}},
			{ID: "t2", Participants: []string{"us", "asia"}},
			{ID: "t3", Participants: []string{"eu", "asia"}},
			{ID: "t4", Participants: []string{"us"}},
		},
		Entries: []entrySpec{
			// t1: committed on us at v5, late commit marker on eu at v12.
			{Partition: "us", Kind: "data", Txn: "t1", Key: "a", Value: "1", Version: 3},
			{Partition: "us", Kind: "commit", Txn: "t1", Version: 5},
			{Partition: "eu", Kind: "data", Txn: "t1", Key: "a", Value: "1", Version: 4},
			{Partition: "eu", Kind: "commit", Txn: "t1", Version: 12},
			// t2: committed on us at v7, data received on asia at v6 but
			// the transaction is still pending there.
			{Partition: "us", Kind: "data", Txn: "t2", Key: "b", Value: "2", Version: 6},
			{Partition: "us", Kind: "commit", Txn: "t2", Version: 7},
			{Partition: "asia", Kind: "data", Txn: "t2", Key: "b", Value: "2", Version: 6},
			// t3: committed on both sides, fully recoverable.
			{Partition: "eu", Kind: "data", Txn: "t3", Key: "c", Value: "3", Version: 8},
			{Partition: "eu", Kind: "commit", Txn: "t3", Version: 9},
			{Partition: "asia", Kind: "data", Txn: "t3", Key: "c", Value: "3", Version: 8},
			{Partition: "asia", Kind: "commit", Txn: "t3", Version: 10},
			// t4: single-partition, no cross-partition constraint.
			{Partition: "us", Kind: "data", Txn: "t4", Key: "d", Value: "4", Version: 8},
			{Partition: "us", Kind: "commit", Txn: "t4", Version: 9},
		},
		Target: map[string]int64{"us": 10, "eu": 10, "asia": 10},
	}
}

func main() {
	scenarioPath := flag.String("scenario", "", "path to a JSON scenario file (empty = built-in demo)")
	flag.Parse()

	var s scenario
	if *scenarioPath == "" {
		s = demoScenario()
	} else {
		data, err := ioutil.ReadFile(*scenarioPath)
		if err != nil {
			fatal(err)
		}
		if err := json.Unmarshal(data, &s); err != nil {
			fatal(err)
		}
	}

	l, err := buildLog(s)
	if err != nil {
		fatal(err)
	}

	target := checker.Cut{}
	for _, name := range l.Order {
		target[name] = l.Partitions[name].LatestVersion()
	}
	for name, v := range s.Target {
		target[name] = checker.Version(v)
	}

	fmt.Printf("target cut (latest per partition): %s\n", target)
	if vs := l.ValidateCut(target); len(vs) > 0 {
		fmt.Println("target cut is NOT recoverable; partial transactions:")
		for _, v := range vs {
			printViolation(v)
		}
	}

	cut, lowerings := l.FindRecoverableCut(target)
	fmt.Printf("\nrecoverable cut: %s\n", cut)
	if len(lowerings) == 0 {
		fmt.Println("no transactions had to be excluded")
	}
	for _, lw := range lowerings {
		fmt.Printf("\nexcluded txn %s\n", lw.TxnID)
		fmt.Println("  missing participants (evidence):")
		for _, m := range lw.Missing {
			fmt.Printf("    - %s: %s\n", m.Partition, m.Reason)
		}
		fmt.Println("  participants lowered:")
		for _, lo := range lw.Lowered {
			fmt.Printf("    - %s: %s\n", lo.Partition, lo.Reason)
		}
	}

	if vs := l.ValidateCut(cut); len(vs) > 0 {
		fmt.Println("\nBUG: computed cut is still inconsistent:")
		for _, v := range vs {
			printViolation(v)
		}
		os.Exit(1)
	}
	fmt.Println("\ncut verified consistent")
}

func printViolation(v checker.Violation) {
	fmt.Printf("  txn %s partially included:\n", v.TxnID)
	for _, p := range v.Participants {
		mark := "excluded"
		if p.Included {
			mark = "INCLUDED"
		}
		fmt.Printf("    - %s [%s]: %s\n", p.Partition, mark, p.Reason)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
