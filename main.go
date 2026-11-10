package main

import (
	"flag"
	"fmt"
	"os"
)

var defaultDictionary = []string{"'", "\\", ";", "';"}

func dedupFragments(inputs []string) [][]byte {
	seen := map[string]bool{}
	fragments := make([][]byte, 0, len(inputs))
	for _, input := range inputs {
		if input == "" || seen[input] {
			continue
		}
		seen[input] = true
		fragments = append(fragments, []byte(input))
	}
	return fragments
}

func run(args []string, stdout, stderr *os.File) int {
	flags := flag.NewFlagSet("sql-firewall-lexdiff", flag.ContinueOnError)
	flags.SetOutput(stderr)
	maxConcat := flags.Int("n", 4, "maximum number of dictionary fragments to concatenate")
	asJSON := flags.Bool("json", false, "emit the report as JSON")
	if err := flags.Parse(args); err != nil {
		return 1
	}

	raw := flags.Args()
	if len(raw) == 0 {
		raw = defaultDictionary
	}
	if *maxConcat < 1 {
		fmt.Fprintln(stderr, "-n must be at least 1")
		return 1
	}

	dictionary := dedupFragments(raw)
	if len(dictionary) == 0 {
		fmt.Fprintln(stderr, "the fragment dictionary is empty")
		return 1
	}

	result := shortestDivergence(dictionary, *maxConcat)
	if result.best == nil {
		if *asJSON {
			_ = renderJSON(stdout, result, dictionary, *maxConcat)
		} else {
			fmt.Fprintf(stderr, "no differential found within %d concatenations\n", *maxConcat)
		}
		return 2
	}

	if *asJSON {
		if err := renderJSON(stdout, result, dictionary, *maxConcat); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
	} else {
		renderHuman(stdout, result, dictionary, *maxConcat)
	}
	return 0
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}
