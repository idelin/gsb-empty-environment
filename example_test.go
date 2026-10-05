package membership

import "fmt"

// A two-node cluster where node 1 crashes, is condemned, then restarts and
// rejoins with its persisted incarnation bumped.
func ExampleSim_Report() {
	cfg := baseConfig(1)
	cfg.NumNodes = 2
	cfg.SuspectTimeout = 15
	s := NewSim(cfg, []Fault{
		{Tick: 15, Kind: FaultCrash, Node: 1},
		{Tick: 90, Kind: FaultRestart, Node: 1},
	})
	s.Run(200)
	fmt.Print(s.Report())
	// Output:
	// node 0 (running=true incarnation=0)
	//   member 0: alive inc=0
	//   member 1: alive inc=1
	//   [t=31] member 1: alive -> suspect (inc=0) direct and indirect probes to node 1 all expired
	//   [t=46] member 1: suspect -> dead (inc=0) suspect timeout for node 1 expired without a higher-incarnation refutation
	//   [t=92] member 1: dead -> alive (inc=1) gossip from node 1
	// node 1 (running=true incarnation=1)
	//   member 0: alive inc=0
	//   member 1: alive inc=1
	//   [t=90] member 1: dead -> alive (inc=1) restart: rejoining with persisted incarnation+1
}
