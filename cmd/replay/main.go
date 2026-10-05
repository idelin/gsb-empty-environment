// Command replay runs a deterministic anti-entropy scenario and prints
// per-round byte and cursor statistics, then checks the final tables
// against the independent full-merge model.
//
// Usage:
//
//	go run ./cmd/replay                 # built-in demo scenario
//	go run ./cmd/replay -scenario s.json
//	go run ./cmd/replay -rounds 120
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"math/rand"
	"os"

	"antientropy/member"
	"antientropy/sim"
	"antientropy/syncx"
)

type scenario struct {
	Nodes           int   `json:"nodes"`
	NumShards       int   `json:"numShards"`
	MaxRespBytes    int   `json:"maxRespBytes"`
	ForceFullEvery  int   `json:"forceFullEvery"`
	Rounds          int   `json:"rounds"`
	Seed            int64 `json:"seed"`
	RecordsPerNode  int   `json:"recordsPerNode"`
	UpdatesPerRound int   `json:"updatesPerRound"`
	UpdateUntil     int   `json:"updateUntil"`
	ConfigChange    *struct {
		Round     int `json:"round"`
		NumShards int `json:"numShards"`
	} `json:"configChange"`
	Faults struct {
		CorruptPage   bool `json:"corruptPage"`
		ForgeDigest   bool `json:"forgeDigest"`
		DuplicatePage bool `json:"duplicatePage"`
		CancelPull    bool `json:"cancelPull"`
		UntilRound    int  `json:"untilRound"`
	} `json:"faults"`
}

func defaultScenario() scenario {
	var sc scenario
	sc.Nodes = 6
	sc.NumShards = 8
	sc.MaxRespBytes = 128
	sc.ForceFullEvery = 4
	sc.Rounds = 80
	sc.Seed = 1
	sc.RecordsPerNode = 6
	sc.UpdatesPerRound = 2
	sc.UpdateUntil = 6
	sc.ConfigChange = &struct {
		Round     int `json:"round"`
		NumShards int `json:"numShards"`
	}{Round: 30, NumShards: 16}
	sc.Faults.CorruptPage = true
	sc.Faults.ForgeDigest = true
	sc.Faults.DuplicatePage = true
	sc.Faults.CancelPull = true
	sc.Faults.UntilRound = 12
	return sc
}

func printReport(rep sim.RoundReport) {
	fmt.Printf("round %3d: req=%6dB resp=%6dB pulled=%3d applied=%3d restarts=%d forged=%d errors=%d\n",
		rep.Round, rep.RequestBytes, rep.ResponseBytes, rep.RecordsPulled,
		rep.RecordsApplied, rep.Restarts, rep.Forged, len(rep.Errors))
	for _, st := range rep.Stats {
		for _, m := range st.CursorProgress {
			done := ""
			if m.Done {
				done = " (done)"
			}
			fmt.Printf("    cursor %-18s shard %2d: %q -> %q%s\n", st.Peer, m.Shard, m.From, m.To, done)
		}
	}
	for _, e := range rep.Errors {
		fmt.Printf("    error: %s\n", e)
	}
}

func run(sc scenario) int {
	rng := rand.New(rand.NewSource(sc.Seed))
	names := make([]string, sc.Nodes)
	for i := range names {
		names[i] = fmt.Sprintf("node-%d", i)
	}
	c := sim.NewCluster(names, sc.NumShards, sc.MaxRespBytes, sc.ForceFullEvery)

	inc := map[string]uint64{}
	mkUpdate := func() member.Record {
		id := fmt.Sprintf("svc-%02d", rng.Intn(sc.Nodes*3))
		inc[id]++
		return member.Record{NodeID: id, Incarnation: inc[id], State: member.State(rng.Intn(3))}
	}
	for _, n := range names {
		for i := 0; i < sc.RecordsPerNode; i++ {
			c.Introduce(n, mkUpdate())
		}
	}

	until := sc.Faults.UntilRound
	if sc.Faults.CorruptPage {
		c.Faults.CorruptPage = func(round int, from, to string) bool {
			return round <= until && rng.Intn(3) == 0
		}
	}
	if sc.Faults.DuplicatePage {
		c.Faults.DuplicatePage = func(round int, from, to string) bool {
			return round <= until && rng.Intn(3) == 0
		}
	}
	if sc.Faults.CancelPull {
		c.Faults.CancelPull = func(round int, from, to string) bool {
			return round <= until && rng.Intn(4) == 0
		}
	}
	if sc.Faults.ForgeDigest {
		c.Faults.ForgeDigest = func(round int, from, to string, d *syncx.Digest) {
			if round > until || rng.Intn(2) != 0 {
				return
			}
			local := c.Nodes[from].Srv.DigestsFor(d.NumShards)
			if d.Shard < len(local) {
				*d = local[d.Shard] // hide divergence
			}
		}
	}

	fmt.Printf("replay: %d nodes, %d shards, maxResp=%dB, forceFullEvery=%d, seed=%d\n",
		sc.Nodes, sc.NumShards, sc.MaxRespBytes, sc.ForceFullEvery, sc.Seed)
	convergedAt := -1
	totalReq, totalResp := 0, 0
	for r := 1; r <= sc.Rounds; r++ {
		if sc.ConfigChange != nil && sc.ConfigChange.Round == r {
			c.SetNumShards(sc.ConfigChange.NumShards)
			fmt.Printf("round %3d: config change -> numShards=%d (snapshots invalidated)\n", r, sc.ConfigChange.NumShards)
		}
		if r <= sc.UpdateUntil {
			for i := 0; i < sc.UpdatesPerRound; i++ {
				c.Introduce(names[rng.Intn(len(names))], mkUpdate())
			}
		}
		rep := c.RunRound(context.Background())
		totalReq += rep.RequestBytes
		totalResp += rep.ResponseBytes
		printReport(rep)
		quiesced := r > sc.UpdateUntil && r > until &&
			(sc.ConfigChange == nil || r > sc.ConfigChange.Round)
		if quiesced && c.Converged() {
			convergedAt = r
			fmt.Println("-- quiesced & converged; two steady-state rounds follow --")
			for i := 0; i < 2; i++ {
				rep := c.RunRound(context.Background())
				totalReq += rep.RequestBytes
				totalResp += rep.ResponseBytes
				printReport(rep)
			}
			break
		}
	}

	fmt.Println("---- summary ----")
	if err := c.CheckModel(); err != nil {
		fmt.Println("MODEL CHECK FAILED:", err)
		return 1
	}
	fmt.Printf("model check OK: all %d nodes equal the independent full-merge of %d updates\n",
		sc.Nodes, len(c.History))
	fmt.Printf("total bytes: req=%d resp=%d; byte-limit violations=%d\n",
		totalReq, totalResp, c.LimitViolations)
	if convergedAt < 0 {
		fmt.Printf("did NOT terminate within %d rounds\n", sc.Rounds)
		return 1
	}
	fmt.Printf("terminated at round %d under fair polling with finite stable data\n", convergedAt)
	return 0
}

func main() {
	scenarioPath := flag.String("scenario", "", "JSON scenario file (empty = built-in demo)")
	rounds := flag.Int("rounds", 0, "override the round count")
	flag.Parse()

	sc := defaultScenario()
	if *scenarioPath != "" {
		data, err := os.ReadFile(*scenarioPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, "read scenario:", err)
			os.Exit(2)
		}
		if err := json.Unmarshal(data, &sc); err != nil {
			fmt.Fprintln(os.Stderr, "parse scenario:", err)
			os.Exit(2)
		}
	}
	if *rounds > 0 {
		sc.Rounds = *rounds
	}
	os.Exit(run(sc))
}
