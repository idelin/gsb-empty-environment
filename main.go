package main

import (
	"flag"
	"fmt"
)

func printResult(res Result) {
	fmt.Printf("verdict: %s (%d search nodes, %d memoized states)\n", res.Verdict, res.Nodes, res.States)
	fmt.Println("evidence:", res.Reason)
	if res.Verdict == Linearizable {
		fmt.Println("witness:", FormatWitness(res.Witness))
	}
}

func runSimScenario(name string, rep SimReport, budget int) {
	fmt.Printf("=== scenario %q (simulator) ===\n", name)
	fmt.Printf("liveness evidence: %d/%d invocations completed by virtual time %d\n",
		rep.Completed, rep.Invoked, rep.Now)
	fmt.Println("history:")
	fmt.Println(FormatHistory(rep.History))
	chk := Checker{Init: 0, Budget: budget}
	res := chk.Check(rep.History)
	fmt.Printf("safety evidence: ")
	printResult(res)
	if res.Verdict == Linearizable {
		if err := VerifyLinearization(rep.History, res.Witness, chk.Init); err != nil {
			fmt.Println("witness self-check FAILED:", err)
		} else {
			fmt.Println("witness self-check: OK")
		}
	}
	fmt.Println()
}

func main() {
	scenario := flag.String("scenario", "all", "good|crash|bad|pending|unknown|all")
	budget := flag.Int("budget", 1000000, "search budget (nodes) before the verdict is UNKNOWN")
	flag.Parse()

	run := func(name string, fn func()) {
		if *scenario == "all" || *scenario == name {
			fn()
		}
	}

	run("good", func() { rep, _ := goodScenario(); runSimScenario("good", rep, *budget) })
	run("crash", func() { rep, _ := crashScenario(); runSimScenario("crash", rep, *budget) })
	run("bad", func() { rep, _ := badScenario(); runSimScenario("bad", rep, *budget) })

	run("pending", func() {
		fmt.Println("=== scenario \"pending\" (hand-built history) ===")
		hist := pendingHistory()
		fmt.Println("history:")
		fmt.Println(FormatHistory(hist))
		printResult(Checker{Init: 0, Budget: *budget}.Check(hist))
		fmt.Println()
	})

	run("unknown", func() {
		fmt.Println("=== scenario \"unknown\" (hand-built history, tight budget) ===")
		hist := hardHistory()
		fmt.Println("history:")
		fmt.Println(FormatHistory(hist))
		fmt.Println("-- budget 200 --")
		printResult(Checker{Init: 0, Budget: 200}.Check(hist))
		fmt.Println("-- full budget --")
		printResult(Checker{Init: 0, Budget: *budget}.Check(hist))
		fmt.Println()
	})
}
