package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
)

func formatPath(path []string) string {
	return strings.Join(path, " -> ")
}

func main() {
	specPath := flag.String("spec", "", "path to JSON state machine specification (required)")
	reqProp := flag.String("request", "request", "proposition marking a pending request")
	respProp := flag.String("response", "response", "proposition marking the response")
	authProp := flag.String("authorized", "authorized", "proposition marking authorization")
	dangerProp := flag.String("danger", "danger", "proposition marking the danger state")
	flag.Parse()

	if *specPath == "" {
		fmt.Fprintln(os.Stderr, "error: -spec is required")
		flag.Usage()
		os.Exit(2)
	}

	m, err := LoadMachine(*specPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(2)
	}

	failed := false

	live := m.CheckLiveness(*reqProp, *respProp)
	if live.OK {
		fmt.Printf("LIVENESS  PASS  G(%s -> F %s): every request is eventually responded\n",
			*reqProp, *respProp)
	} else {
		failed = true
		fmt.Printf("LIVENESS  FAIL  request at %q may never be responded\n", live.ReqState)
		fmt.Printf("  witness prefix: %s\n", formatPath(live.Prefix))
		fmt.Printf("  witness cycle:  %s  (repeats forever, no %q)\n",
			formatPath(live.Cycle), *respProp)
	}

	safe := m.CheckSafety(*authProp, *dangerProp)
	if safe.OK {
		fmt.Printf("SAFETY    PASS  %q is only entered after %q\n", *dangerProp, *authProp)
	} else {
		failed = true
		fmt.Printf("SAFETY    FAIL  %q reachable without prior %q\n", *dangerProp, *authProp)
		fmt.Printf("  witness path:   %s\n", formatPath(safe.Path))
	}

	if failed {
		os.Exit(1)
	}
}
