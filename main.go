package main

import (
	"fmt"
	"io"
	"os"
)

func describe(e LogEntry) string {
	switch e.Op {
	case "create":
		t := e.Type
		if t == "" {
			t = "file"
		}
		return fmt.Sprintf("create %s (%s)", e.Path, t)
	case "rename":
		return fmt.Sprintf("rename %s -> %s", e.Src, e.Dst)
	case "unlink":
		return fmt.Sprintf("unlink %s", e.Path)
	default:
		return fmt.Sprintf("%s ?", e.Op)
	}
}

func printReport(w io.Writer, rep *Report) {
	for _, step := range rep.Steps {
		status := "OK  "
		if step.Err != nil {
			status = "FAIL"
		}
		fmt.Fprintf(w, "[seq=%d] %-28s %s\n", step.Entry.Seq, describe(step.Entry), status)
		for _, ev := range step.Events {
			fmt.Fprintf(w, "    %s\n", ev)
		}
		if step.Err != nil {
			fmt.Fprintf(w, "    error: %v (transaction rolled back, state unchanged)\n", step.Err)
		}
		for _, line := range SortedMapping(step.Mapping) {
			fmt.Fprintf(w, "    %s\n", line)
		}
	}
	if len(rep.Recreations) > 0 {
		fmt.Fprintln(w, "recreations (delete-then-recreate at same path):")
		for _, r := range rep.Recreations {
			fmt.Fprintf(w, "    %s\n", r)
		}
	}
	if len(rep.Issues) > 0 {
		fmt.Fprintln(w, "log issues:")
		for _, i := range rep.Issues {
			fmt.Fprintf(w, "    %s\n", i)
		}
	}
	if rep.Failed > 0 {
		fmt.Fprintf(w, "failed transactions: %d (state left untouched by each)\n", rep.Failed)
	}
	if rep.Diverged {
		fmt.Fprintln(w, "FATAL: primary tree and independent model diverged")
	}
	if len(rep.Issues) == 0 && rep.Failed == 0 && !rep.Diverged {
		fmt.Fprintln(w, "replay clean: no duplicates, no gaps, identities stable")
	}
}

func main() {
	var input io.Reader = os.Stdin
	if len(os.Args) > 1 {
		f, err := os.Open(os.Args[1])
		if err != nil {
			fmt.Fprintln(os.Stderr, "open:", err)
			os.Exit(2)
		}
		defer f.Close()
		input = f
	}
	entries, err := ParseLog(input)
	if err != nil {
		fmt.Fprintln(os.Stderr, "parse:", err)
		os.Exit(2)
	}
	rep := Replay(entries)
	printReport(os.Stdout, rep)
	if len(rep.Issues) > 0 || rep.Failed > 0 || rep.Diverged {
		os.Exit(1)
	}
}
