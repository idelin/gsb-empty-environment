package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "run":
		err = cmdRun(os.Args[2:])
	case "replay":
		err = cmdReplay(os.Args[2:])
	case "-h", "--help", "help":
		usage()
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, strings.TrimLeft(`
dns-replay - offline replay of DNS cache / SSRF check / dial / pool reuse

Usage:
  dns-replay run    --scenario file.json [--report out.json]
  dns-replay replay --scenario file.json

run:    executes the full scenario, writes the JSON report (stdout unless
        --report is given) and a human-readable summary to stderr.
replay: executes a subscenario previously extracted into a violation and
        verifies it reproduces a violation independently.
`, "\n"))
}

type runFlags struct {
	scenario string
	report   string
}

func parseRunFlags(args []string) (runFlags, error) {
	var flags runFlags
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--scenario":
			i++
			if i >= len(args) {
				return flags, fmt.Errorf("--scenario requires a file path")
			}
			flags.scenario = args[i]
		case "--report":
			i++
			if i >= len(args) {
				return flags, fmt.Errorf("--report requires a file path")
			}
			flags.report = args[i]
		default:
			return flags, fmt.Errorf("unknown flag %q", args[i])
		}
	}
	if flags.scenario == "" {
		return flags, fmt.Errorf("missing --scenario")
	}
	return flags, nil
}

func loadScenario(path string) (Scenario, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Scenario{}, err
	}
	var sc Scenario
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&sc); err != nil {
		return Scenario{}, fmt.Errorf("decode %s: %w", path, err)
	}
	return sc, nil
}

func execute(sc Scenario) (*Report, error) {
	engine, err := newEngine(sc)
	if err != nil {
		return nil, err
	}
	report, _, err := engine.run()
	if err != nil {
		return nil, err
	}
	return report, nil
}

func cmdRun(args []string) error {
	flags, err := parseRunFlags(args)
	if err != nil {
		return err
	}
	sc, err := loadScenario(flags.scenario)
	if err != nil {
		return err
	}
	report, err := execute(sc)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	if flags.report != "" {
		if err := os.WriteFile(flags.report, append(data, '\n'), 0o644); err != nil {
			return err
		}
	} else {
		os.Stdout.Write(append(data, '\n'))
	}
	printSummary(os.Stderr, report)
	return nil
}

func cmdReplay(args []string) error {
	flags, err := parseRunFlags(args)
	if err != nil {
		return err
	}
	sc, err := loadScenario(flags.scenario)
	if err != nil {
		return err
	}
	report, err := execute(sc)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	os.Stdout.Write(append(data, '\n'))
	printSummary(os.Stderr, report)
	if len(report.Violations) == 0 {
		return fmt.Errorf("replay produced no violation; subscenario is not a valid witness")
	}
	fmt.Fprintf(os.Stderr, "replay OK: %d violation(s) reproduced independently\n", len(report.Violations))
	return nil
}

func printSummary(w *os.File, report *Report) {
	fmt.Fprintf(w, "\n=== replay summary: %d event(s), %d violation(s) ===\n", len(report.Events), len(report.Violations))
	for _, ev := range report.Events {
		fmt.Fprintf(w, "%3d t=%-4d %-18s", ev.Seq, ev.At, ev.Type)
		if ev.RequestID != "" {
			fmt.Fprintf(w, " req=%s", ev.RequestID)
		}
		if ev.Host != "" && ev.Host != ev.RequestID {
			fmt.Fprintf(w, " host=%s", ev.Host)
		}
		if len(ev.Detail) > 0 {
			keys := make([]string, 0, len(ev.Detail))
			for k := range ev.Detail {
				keys = append(keys, k)
			}
			sortStrings(keys)
			parts := make([]string, 0, len(keys))
			for _, k := range keys {
				parts = append(parts, k+"="+ev.Detail[k])
			}
			fmt.Fprintf(w, " %s", strings.Join(parts, " "))
		}
		fmt.Fprintln(w)
	}
	for _, v := range report.Violations {
		fmt.Fprintln(w, strings.Repeat("-", 60))
		fmt.Fprintf(w, "VIOLATION %s\n  request:        %s\n  kind:           %s\n", v.ID, v.RequestID, v.Kind)
		fmt.Fprintf(w, "  validated addrs: %v (at t=%d, check PASS)\n", v.ValidatedAddrs, v.ValidateAt)
		if v.Kind == ViolationDial {
			fmt.Fprintf(w, "  dial address:   %s (at t=%d)\n", v.DialAddress, v.ActionAt)
		} else {
			fmt.Fprintf(w, "  reused peer:    %s via conn #%d (at t=%d, no re-resolution)\n", v.ReusedPeer, v.ReusedConnID, v.ActionAt)
		}
		fmt.Fprintf(w, "  causal subsequence (global seqs): %v\n", v.CausalEventSeqs)
		fmt.Fprintf(w, "  standalone witness: %d request(s), hosts %v\n", len(v.SubScenario.Requests), mapKeys(v.SubScenario.Hosts))
	}
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}

func mapKeys(m map[string]Host) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sortStrings(out)
	return out
}
