package main

import "fmt"

func report(name string, res *SimResult, cfg CheckConfig) {
	fmt.Printf("=== %s ===\n", name)
	fmt.Printf("simulator: %s\n", res)
	out, err := Check(res.History, cfg)
	if err != nil {
		fmt.Println("check error:", err)
		return
	}
	fmt.Printf("verdict: %s (states explored: %d)\n", out.Verdict, out.Explored)
	switch out.Verdict {
	case Linearizable:
		fmt.Println("safety evidence (witness, one legal sequential order):")
		for _, op := range out.Witness {
			fmt.Println("  " + op.String())
		}
	case NotLinearizable:
		fmt.Println("safety evidence (counterexample: no legal order respects real-time + register semantics):")
		fmt.Print(res.History.String())
	}
	fmt.Println()
}

func main() {
	// 1. Correct server, no faults: expect LINEARIZABLE with a witness.
	report("correct server, no faults", Simulate(SimConfig{
		Seed: 1, NumClients: 3, OpsPerClient: 3, MaxDelay: 5,
	}), CheckConfig{InitValue: 0})

	// 2. Crashes and message loss: incomplete calls, still linearizable.
	report("crashes + message loss (incomplete calls)", Simulate(SimConfig{
		Seed: 7, NumClients: 3, OpsPerClient: 4, MaxDelay: 5,
		CrashProb: 0.2, LossProb: 0.2,
	}), CheckConfig{InitValue: 0})

	// 3. Buggy server (stale reads): expect NOT LINEARIZABLE counterexample.
	report("buggy server (stale reads)", Simulate(SimConfig{
		Seed: 3, NumClients: 3, OpsPerClient: 4, MaxDelay: 5, StaleRead: true,
	}), CheckConfig{InitValue: 0})

	// 4. Heavy contention with a tiny budget: expect UNKNOWN.
	report("heavy contention, tiny budget", Simulate(SimConfig{
		Seed: 11, NumClients: 8, OpsPerClient: 3, MaxDelay: 50,
	}), CheckConfig{InitValue: 0, Budget: 64})
}
