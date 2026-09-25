// Command kvimport is a small demo CLI around the kvimport package.
//
//	kvimport -dir DIR -input FILE [-batch N] [-inject point[:nth]]
//	kvimport -dir DIR -cat
//
// With -inject the process panics (simulating a crash) when the given
// fault point fires for the nth time; rerun without -inject to resume.
package main

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"kvimport"
)

type crashInjector struct {
	point kvimport.FaultPoint
	nth   int
	count int
}

func (c *crashInjector) OnFault(p kvimport.FaultPoint) {
	if p == c.point {
		c.count++
		if c.count >= c.nth {
			panic("simulated crash at " + string(p))
		}
	}
}

func main() {
	dir := flag.String("dir", "", "working/output directory (all files live here)")
	input := flag.String("input", "", "input record file (key=value lines)")
	batch := flag.Int("batch", 3, "records per checkpoint batch")
	inject := flag.String("inject", "", "simulate crash at point[:nth]: before_checkpoint, after_checkpoint, before_publish, after_publish")
	cat := flag.Bool("cat", false, "print the published store and exit")
	flag.Parse()

	if *dir == "" {
		fmt.Fprintln(os.Stderr, "-dir is required")
		os.Exit(2)
	}

	if *cat {
		store, err := kvimport.LoadPublished(*dir)
		if err != nil {
			fmt.Fprintln(os.Stderr, "cat:", err)
			os.Exit(1)
		}
		printStore(store)
		return
	}

	if *input == "" {
		fmt.Fprintln(os.Stderr, "-input is required")
		os.Exit(2)
	}

	cfg := kvimport.Config{Dir: *dir, InputPath: *input, BatchSize: *batch}
	if *inject != "" {
		point, nth := parseInject(*inject)
		cfg.Injector = &crashInjector{point: point, nth: nth}
		fmt.Printf("fault injection armed: %s (occurrence %d)\n", point, nth)
	}

	res, err := kvimport.Run(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "import failed:", err)
		os.Exit(1)
	}
	fmt.Printf("result: resumed=%v already_done=%v batches=%d keys=%d rejects=%d\n",
		res.Resumed, res.AlreadyDone, res.Batches, res.KeysTotal, res.RejectsTotal)
}

func parseInject(s string) (kvimport.FaultPoint, int) {
	point, nth := s, 1
	if i := strings.LastIndex(s, ":"); i >= 0 {
		if n, err := strconv.Atoi(s[i+1:]); err == nil {
			point, nth = s[:i], n
		}
	}
	return kvimport.FaultPoint(point), nth
}

func printStore(store *kvimport.Store) {
	keys := store.Keys()
	sort.Strings(keys)
	for _, k := range keys {
		v, _ := store.Get(k)
		fmt.Printf("%s = %s (version %d)\n", k, v.Value, v.Version)
	}
}
