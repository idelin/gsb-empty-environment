// Command logtmpl clusters log lines into templates from the command line.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"logtmpl"
)

func main() {
	var (
		budget        = flag.Int("budget", 0, "max live templates (0 = unlimited)")
		threshold     = flag.Int("threshold", 4, "max compatibility distance for merges")
		ratio         = flag.Float64("ratio", 0.5, "max wildcard ratio (over-generalization limit)")
		slotValues    = flag.Int("slot-values", 8, "distinct wildcard values before a split")
		deterministic = flag.Bool("deterministic", true, "canonical-order processing (order-independent results)")
		offline       = flag.Bool("offline", false, "also run the offline reference re-clustering and compare")
		replay        = flag.String("replay", "", "replay a telemetry fixture (JSONL) instead of clustering")
		fixtureOut    = flag.String("fixture-out", "", "write emitted telemetry events to this JSONL file")
	)
	flag.Parse()

	if *replay != "" {
		if err := runReplay(*replay); err != nil {
			fatal(err)
		}
		return
	}

	lines, err := readLines(flag.Args())
	if err != nil {
		fatal(err)
	}

	cfg := logtmpl.Config{
		MergeThreshold:   *threshold,
		MaxTemplates:     *budget,
		MaxWildcardRatio: *ratio,
		MaxSlotValues:    *slotValues,
		Deterministic:    *deterministic,
	}
	tel := &logtmpl.Telemetry{}
	c := logtmpl.New(cfg, tel)
	for _, l := range lines {
		c.Add(l)
	}
	c.Finalize()

	templates := c.Templates()
	fmt.Printf("== templates (%d live, %d journal entries) ==\n", len(templates), len(c.Journal()))
	for _, t := range templates {
		fmt.Printf("[%d] count=%d  %s\n", t.ID, t.Count, t.Signature())
		for _, e := range t.Examples {
			fmt.Printf("    ex: %s\n", e)
		}
	}
	if un := c.Unclassified(); len(un) > 0 {
		fmt.Printf("== unclassified (%d, retained, not dropped) ==\n", len(un))
		for _, u := range un {
			fmt.Printf("    %s  [%s]\n", u.Line, u.Reason)
		}
	}

	if *offline {
		ref := logtmpl.Recluster(lines, cfg)
		fmt.Printf("== offline reference (%d templates) ==\n", len(ref))
		for _, t := range ref {
			fmt.Printf("    count=%d  %s\n", t.Count, t.Signature())
		}
		onP, ok1 := logtmpl.PartitionOf(templates, lines)
		offP, ok2 := logtmpl.PartitionOf(ref, lines)
		switch {
		case !ok1 || !ok2:
			fmt.Println("== partition comparison: AMBIGUOUS (cannot compare) ==")
		case partitionsEqual(onP, offP):
			fmt.Println("== partition comparison: MATCH (online == offline reference) ==")
		default:
			fmt.Println("== partition comparison: DIVERGED ==")
		}
	}

	if *fixtureOut != "" {
		f, err := os.Create(*fixtureOut)
		if err != nil {
			fatal(err)
		}
		defer f.Close()
		if err := logtmpl.WriteFixture(f, tel.Events()); err != nil {
			fatal(err)
		}
		fmt.Printf("== telemetry: %d events written to %s ==\n", len(tel.Events()), *fixtureOut)
	} else {
		fmt.Printf("== telemetry: %d events synthesized locally ==\n", len(tel.Events()))
	}
}

func runReplay(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	events, err := logtmpl.ReadFixture(f)
	if err != nil {
		return err
	}
	sigs := logtmpl.ReplayTemplates(events)
	fmt.Printf("== replayed %d events from %s: %d live templates ==\n", len(events), path, len(sigs))
	for _, s := range sigs {
		fmt.Printf("    %s\n", s)
	}
	return nil
}

func partitionsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if bv, ok := b[k]; !ok || bv != v {
			return false
		}
	}
	return true
}

func readLines(paths []string) ([]string, error) {
	var readers []io.Reader
	var closers []io.Closer
	if len(paths) == 0 {
		readers = append(readers, os.Stdin)
	}
	for _, p := range paths {
		f, err := os.Open(p)
		if err != nil {
			return nil, err
		}
		readers = append(readers, f)
		closers = append(closers, f)
	}
	defer func() {
		for _, c := range closers {
			c.Close()
		}
	}()
	var lines []string
	for _, r := range readers {
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 0, 1<<20), 1<<20)
		for sc.Scan() {
			if l := strings.TrimRight(sc.Text(), "\r"); l != "" {
				lines = append(lines, l)
			}
		}
		if err := sc.Err(); err != nil {
			return nil, err
		}
	}
	return lines, nil
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
