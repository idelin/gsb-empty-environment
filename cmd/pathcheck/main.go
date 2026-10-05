// Command pathcheck checks whether a comma-separated set of allow patterns
// admits a path that no deny pattern rejects.
//
//	pathcheck -allow 'a/*,b' -deny 'a/c'
//
// An empty element (e.g. -allow ',a') denotes the empty pattern "".
// Exit code: 0 witness found, 1 none, 2 unknown, 3 usage error.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"pathcheck"
)

func main() {
	allowFlag := flag.String("allow", "", "comma-separated allow patterns")
	denyFlag := flag.String("deny", "", "comma-separated deny patterns")
	budget := flag.Int("budget", 1000000, "maximum number of candidate paths to examine")
	flag.Parse()

	allows, err := parseList(*allowFlag)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(3)
	}
	denies, err := parseList(*denyFlag)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(3)
	}

	res := pathcheck.Check(allows, denies, *budget)
	switch res.Verdict {
	case pathcheck.Witness:
		fmt.Printf("witness: %q (%d segments, %d paths checked)\n",
			res.Path.String(), len(res.Path.Segs), res.Checked)
	case pathcheck.None:
		fmt.Printf("none: every allowed path is denied (%d paths checked, depth bound %d)\n",
			res.Checked, res.Bound)
	default:
		fmt.Printf("unknown: budget exhausted after %d paths (depth bound %d)\n",
			res.Checked, res.Bound)
	}
	os.Exit(map[pathcheck.Verdict]int{
		pathcheck.Witness: 0,
		pathcheck.None:    1,
		pathcheck.Unknown: 2,
	}[res.Verdict])
}

func parseList(csv string) ([]pathcheck.Pattern, error) {
	if csv == "" {
		return nil, nil
	}
	var ps []pathcheck.Pattern
	for _, s := range strings.Split(csv, ",") {
		p, err := pathcheck.ParsePattern(s)
		if err != nil {
			return nil, err
		}
		ps = append(ps, p)
	}
	return ps, nil
}
