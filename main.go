package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"strings"
)

type registryFile struct {
	Packages []Candidate `json:"packages"`
}

func loadJSON(path string, v interface{}) error {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

func main() {
	regPath := flag.String("registry", "registry.json", "registry JSON file")
	lockPath := flag.String("lock", "lock.json", "lock file JSON")
	rootsPath := flag.String("roots", "roots.json", "root requirements JSON (name -> constraint)")
	add := flag.String("add", "", "extra root requirement to add, e.g. 'foo@>=1.2.0' (repeatable, comma-separated)")
	jsonOut := flag.Bool("json", false, "emit the plan as JSON")
	flag.Parse()

	var rf registryFile
	if err := loadJSON(*regPath, &rf); err != nil {
		fatal("loading registry: %v", err)
	}
	reg, err := NewRegistry(rf.Packages)
	if err != nil {
		fatal("parsing registry: %v", err)
	}
	lock := map[string]LockEntry{}
	if err := loadJSON(*lockPath, &lock); err != nil {
		fatal("loading lock: %v", err)
	}
	roots := map[string]string{}
	if err := loadJSON(*rootsPath, &roots); err != nil {
		fatal("loading roots: %v", err)
	}
	for _, spec := range strings.Split(*add, ",") {
		if spec == "" {
			continue
		}
		parts := strings.SplitN(spec, "@", 2)
		if len(parts) != 2 {
			fatal("bad --add %q, want name@constraint", spec)
		}
		roots[parts[0]] = parts[1]
	}

	resolved, obj, ok := Solve(reg, lock, roots)
	if !ok {
		fatal("no feasible resolution for the given roots")
	}
	plan := Explain(reg, lock, roots, resolved, obj)
	if *jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(plan); err != nil {
			fatal("encoding plan: %v", err)
		}
		return
	}
	fmt.Print(plan.Render())
}

func fatal(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "lockplan: "+format+"\n", args...)
	os.Exit(1)
}
