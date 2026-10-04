package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
)

func parseSize(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty size")
	}
	mult := int64(1)
	upper := strings.ToUpper(s)
	switch {
	case strings.HasSuffix(upper, "KB"):
		mult, s = 1<<10, s[:len(s)-2]
	case strings.HasSuffix(upper, "MB"):
		mult, s = 1<<20, s[:len(s)-2]
	case strings.HasSuffix(upper, "GB"):
		mult, s = 1<<30, s[:len(s)-2]
	case strings.HasSuffix(upper, "K"):
		mult, s = 1<<10, s[:len(s)-1]
	case strings.HasSuffix(upper, "M"):
		mult, s = 1<<20, s[:len(s)-1]
	case strings.HasSuffix(upper, "G"):
		mult, s = 1<<30, s[:len(s)-1]
	case strings.HasSuffix(upper, "B"):
		s = s[:len(s)-1]
	}
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid size %q", s)
	}
	if n <= 0 {
		return 0, fmt.Errorf("size must be positive")
	}
	return n * mult, nil
}

func usage() {
	fmt.Fprintf(os.Stderr, `Usage:
  merger [flags] -out OUT FILE...          merge sources into OUT (resumable)
  merger -verify -out OUT FILE...          verify OUT against a full sort of sources

Input format: one record per line, the whole line is the key; files must end
with a newline (a trailing partial line is treated as truncated and discarded).
Output format: "key<TAB>source<TAB>sequence" per line, sorted by that triple.

Flags:
`)
	flag.PrintDefaults()
}

func main() {
	var (
		memStr     = flag.String("mem", "16MB", "memory limit for buffered record bytes (suffixes K/M/G/KB/MB/GB)")
		out        = flag.String("out", "", "output file path (required)")
		workdir    = flag.String("workdir", "", "work directory for runs and checkpoint (default <out>.work)")
		verify     = flag.Bool("verify", false, "verify output against sources with a full in-memory sort")
		crashAfter = flag.Int64("crash-after", 0, "testing: simulate a crash after N records processed in this run")
		crashAck   = flag.Bool("crash-before-checkpoint", false, "testing: simulate a lost ack (crash after output fsync, before checkpoint)")
		verbose    = flag.Bool("v", false, "verbose logging")
	)
	flag.Usage = usage
	flag.Parse()
	sources := flag.Args()
	if *out == "" || len(sources) == 0 {
		usage()
		os.Exit(2)
	}

	if *verify {
		if err := Verify(*out, sources); err != nil {
			fmt.Fprintln(os.Stderr, "verify FAILED:", err)
			os.Exit(1)
		}
		fmt.Println("verify OK")
		return
	}

	mem, err := parseSize(*memStr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(2)
	}
	wd := *workdir
	if wd == "" {
		wd = *out + ".work"
	}
	cfg := Config{
		MemLimit:              mem,
		OutPath:               *out,
		WorkDir:               wd,
		Sources:               sources,
		CrashAfter:            *crashAfter,
		CrashBeforeCheckpoint: *crashAck,
		Verbose:               *verbose,
	}
	err = NewMerger(cfg).Run()
	if errors.Is(err, errSimulatedCrash) {
		fmt.Fprintln(os.Stderr, "simulated crash: durable state saved; re-run the same command to resume")
		os.Exit(3)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	fmt.Println("merge complete")
}
