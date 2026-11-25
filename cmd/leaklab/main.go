// Command leaklab 演示并校验推测执行泄漏分析包：
//
//	leaklab demo [--scenario leaky|barrier|public] [--budget N] [--out witness.json]
//	leaklab verify --in witness.json
//
// demo 运行内置场景并打印分析结论；发现泄漏时可写出可重放见证 JSON。
// verify 独立重放见证文件并逐条核对逐步记录。
package main

import (
	"flag"
	"fmt"
	"os"

	"prescreen.local/ps-65e4b96950"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "demo":
		err = runDemo(os.Args[2:])
	case "verify":
		err = runVerify(os.Args[2:])
	case "-h", "--help", "help":
		usage()
	default:
		err = fmt.Errorf("unknown subcommand %q", os.Args[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "leaklab:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `usage:
  leaklab demo [--scenario leaky|barrier|public] [--budget N] [--out witness.json]
  leaklab verify --in witness.json
`)
}

func runDemo(args []string) error {
	fs := flag.NewFlagSet("demo", flag.ContinueOnError)
	scenario := fs.String("scenario", "leaky", "built-in scenario: leaky, barrier or public")
	budget := fs.Int("budget", 0, "execution budget (0 = unlimited)")
	out := fs.String("out", "", "write witness JSON to this path when a leak is found")
	if err := fs.Parse(args); err != nil {
		return err
	}

	var prog *leaklab.Program
	spec := leaklab.LeakySpecMem()
	switch *scenario {
	case "leaky":
		prog = leaklab.LeakyProgram(false)
	case "barrier":
		prog = leaklab.LeakyProgram(true)
	case "public":
		prog = leaklab.PublicLeakProgram()
	default:
		return fmt.Errorf("unknown scenario %q", *scenario)
	}

	res, err := leaklab.Analyze(leaklab.Config{Program: prog, Spec: spec, ExecBudget: *budget})
	if err != nil {
		return err
	}
	fmt.Printf("scenario=%s status=%s executions=%d candidates=%d exhausted=%t\n",
		*scenario, res.Status, res.Executions, res.Candidates, res.Exhausted)
	if res.Reason != "" {
		fmt.Println("reason:", res.Reason)
	}
	if res.Witness != nil {
		printWitness(res.Witness)
		if *out != "" {
			data, err := leaklab.EncodeWitness(res.Witness)
			if err != nil {
				return err
			}
			if err := os.WriteFile(*out, data, 0o644); err != nil {
				return err
			}
			fmt.Println("witness written to", *out)
		}
	}
	return nil
}

func runVerify(args []string) error {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	in := fs.String("in", "", "witness JSON file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *in == "" {
		return fmt.Errorf("--in is required")
	}
	data, err := os.ReadFile(*in)
	if err != nil {
		return err
	}
	w, err := leaklab.DecodeWitness(data)
	if err != nil {
		return err
	}
	if err := w.Replay(); err != nil {
		return err
	}
	printWitness(w)
	fmt.Println("replay: OK — witness reproduces the speculative-only leak")
	return nil
}

func printWitness(w *leaklab.Witness) {
	fmt.Printf("witness site=pc%d public=%v secrets=(%d,%d)\n",
		w.SitePC, w.A.Input.Public, w.A.Input.Secret, w.B.Input.Secret)
	fmt.Printf("  arch trace A=%v B=%v (equal=%t)\n",
		w.A.Result.ArchTrace, w.B.Result.ArchTrace,
		equalSets(w.A.Result.ArchTrace, w.B.Result.ArchTrace))
	fmt.Printf("  spec trace A=%v\n", w.A.Result.SpecTrace)
	fmt.Printf("  spec trace B=%v\n", w.B.Result.SpecTrace)
}

func equalSets(a, b []leaklab.ProbeEvent) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
