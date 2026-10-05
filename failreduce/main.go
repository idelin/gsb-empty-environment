// Command failreduce reduces a failing configuration to a 1-minimal set
// while keeping item dependencies legal. It judges configurations by
// invoking a deterministic external oracle program.
//
// Oracle contract (exit codes): 0 = pass, 1 = target test failure
// (interesting), 2 = compile failure (not interesting). Any other exit code
// aborts the reduction.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

const (
	exitOK            = 0
	exitUsage         = 1
	exitPrecondition  = 2 // illegal or non-failing initial configuration
	exitOracleFailure = 3 // oracle could not be run or misbehaved
)

type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error {
	*s = append(*s, v)
	return nil
}

type options struct {
	oraclePath string
	oracleArgs stringList
	useStdin   bool
	timeout    time.Duration
	depsFile   string
	items      string
	itemsFile  string
	jsonOut    bool
	verify     bool
}

type result struct {
	Items      []string `json:"items"`
	ItemCount  int      `json:"itemCount"`
	OneMinimal bool     `json:"oneMinimal"`
	Stats      Stats    `json:"stats"`
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(argv []string, stdout, stderr io.Writer) int {
	var opts options
	fs := flag.NewFlagSet("failreduce", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&opts.oraclePath, "oracle", "", "path to the deterministic oracle program (required)")
	fs.Var(&opts.oracleArgs, "oracle-arg", "fixed argument passed to the oracle (repeatable)")
	fs.BoolVar(&opts.useStdin, "oracle-stdin", false, "pass items to the oracle on stdin instead of argv")
	fs.DurationVar(&opts.timeout, "timeout", 60*time.Second, "per-invocation oracle timeout")
	fs.StringVar(&opts.depsFile, "deps", "", "dependency file: lines of 'item: dep1 dep2 ...'")
	fs.StringVar(&opts.items, "items", "", "comma-separated initial configuration items")
	fs.StringVar(&opts.itemsFile, "items-file", "", "file with whitespace-separated initial items")
	fs.BoolVar(&opts.jsonOut, "json", false, "emit machine-readable JSON")
	fs.BoolVar(&opts.verify, "verify", false, "re-check 1-minimality of the result (served from cache)")
	if err := fs.Parse(argv); err != nil {
		return exitUsage
	}

	if opts.oraclePath == "" {
		fmt.Fprintln(stderr, "error: --oracle is required")
		return exitUsage
	}
	items, err := loadItems(opts)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return exitUsage
	}
	if len(items) == 0 {
		fmt.Fprintln(stderr, "error: no configuration items given (use --items or --items-file)")
		return exitUsage
	}
	deps, err := loadDeps(opts.depsFile)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return exitUsage
	}

	execOracle := &ExecOracle{
		Path:     opts.oraclePath,
		Args:     opts.oracleArgs,
		UseStdin: opts.useStdin,
		Timeout:  opts.timeout,
	}
	oracle := NewCachingOracle(execOracle)

	reduced, err := Reduce(items, deps, oracle)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		if errors.Is(err, ErrIllegalConfig) || errors.Is(err, ErrNotFailing) {
			return exitPrecondition
		}
		return exitOracleFailure
	}

	oneMinimal := true
	if opts.verify {
		ok, verr := verifyOneMinimal(reduced, deps, oracle)
		if verr != nil {
			fmt.Fprintf(stderr, "error: verification failed: %v\n", verr)
			return exitOracleFailure
		}
		oneMinimal = ok
		if !ok {
			fmt.Fprintln(stderr, "warning: result is NOT 1-minimal")
		}
	}

	res := result{
		Items:      reduced,
		ItemCount:  len(reduced),
		OneMinimal: oneMinimal,
		Stats:      oracle.Stats,
	}
	if opts.jsonOut {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		enc.Encode(res)
	} else {
		printText(stdout, res)
	}
	return exitOK
}

func printText(w io.Writer, res result) {
	fmt.Fprintf(w, "reduced configuration (%d items, 1-minimal):\n", res.ItemCount)
	for _, item := range res.Items {
		fmt.Fprintf(w, "  %s\n", item)
	}
	fmt.Fprintf(w, "statistics:\n")
	fmt.Fprintf(w, "  oracle invocations: %d\n", res.Stats.Invocations)
	fmt.Fprintf(w, "  cache hits:         %d\n", res.Stats.CacheHits)
	fmt.Fprintf(w, "  verdicts:           test-failures=%d passes=%d compile-failures=%d\n",
		res.Stats.TestFailures, res.Stats.Passes, res.Stats.CompileFailures)
}

func loadItems(opts options) ([]string, error) {
	var items []string
	if opts.items != "" {
		for _, item := range strings.Split(opts.items, ",") {
			if item = strings.TrimSpace(item); item != "" {
				items = append(items, item)
			}
		}
	}
	if opts.itemsFile != "" {
		data, err := os.ReadFile(opts.itemsFile)
		if err != nil {
			return nil, fmt.Errorf("reading items file: %w", err)
		}
		items = append(items, strings.Fields(string(data))...)
	}
	return items, nil
}

// loadDeps parses a dependency file. Each non-comment line declares one item
// and the items it requires, e.g.:
//
//	# item: requires...
//	app: libcore libnet
//	libcore: runtime
func loadDeps(path string) (*Deps, error) {
	deps := NewDeps()
	if path == "" {
		return deps, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading deps file: %w", err)
	}
	for lineno, line := range strings.Split(string(data), "\n") {
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		item := strings.TrimSuffix(fields[0], ":")
		requires := fields[1:]
		if len(requires) > 0 && requires[0] == ":" {
			requires = requires[1:]
		}
		if item == "" {
			return nil, fmt.Errorf("deps file line %d: missing item name", lineno+1)
		}
		deps.Add(item, requires...)
	}
	return deps, nil
}

// verifyOneMinimal re-checks the 1-minimality guarantee. Because the final
// reduction phase already judged every single-item deletion, this is served
// entirely from the cache and costs no extra oracle invocations.
func verifyOneMinimal(cfg []string, deps *Deps, oracle Oracle) (bool, error) {
	for _, item := range cfg {
		candidate := deps.RemovalClosure(cfg, []string{item})
		if len(candidate) == len(cfg) {
			continue
		}
		verdict, err := oracle.Test(candidate)
		if err != nil {
			return false, err
		}
		if verdict == TestFail {
			return false, nil
		}
	}
	return true, nil
}
