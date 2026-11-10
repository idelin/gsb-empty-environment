package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
)

func main() {
	configPath := flag.String("config", "", "path to ssh_config file (required)")
	alias := flag.String("alias", "", "target host alias to resolve (required)")
	forbid := flag.String("forbid", "", "comma-separated forbidden host:port endpoints")
	flag.Parse()

	if *configPath == "" || *alias == "" {
		fmt.Fprintln(os.Stderr, "usage: offlinecli -config FILE -alias ALIAS [-forbid host:port,...]")
		os.Exit(2)
	}

	cfg, err := ParseFile(*configPath)
	if err != nil {
		var ice *IncludeCycleError
		if errors.As(err, &ice) {
			fmt.Fprintf(os.Stderr, "error: %s\n", ice)
		} else {
			fmt.Fprintf(os.Stderr, "error: reading config: %v\n", err)
		}
		os.Exit(2)
	}

	forbidden, err := ParseForbiddenSet(*forbid)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(2)
	}

	chain, err := Resolve(cfg, *alias)
	if err != nil {
		var jle *JumpLoopError
		if errors.As(err, &jle) {
			fmt.Fprintf(os.Stderr, "error: %s\n", jle)
		} else {
			fmt.Fprintf(os.Stderr, "error: resolving %q: %v\n", *alias, err)
		}
		os.Exit(2)
	}

	violation := chain.MarkForbidden(forbidden)

	fmt.Printf("chain for alias %q:\n", *alias)
	for i, h := range chain.Hops {
		via := "target"
		if h.ViaJump {
			via = "jump"
		}
		user := ""
		if h.User != "" {
			user = h.User + "@"
		}
		mark := ""
		if h.Forbidden {
			mark = "  [FORBIDDEN]"
		}
		fmt.Printf("  %d. %-6s alias=%-16s endpoint=%s%s:%s%s\n",
			i+1, via, h.Alias, user, h.HostName, h.Port, mark)
	}

	if len(forbidden) > 0 {
		keys := make([]string, 0, len(forbidden))
		for k := range forbidden {
			keys = append(keys, k)
		}
		fmt.Printf("forbidden endpoints: %s\n", strings.Join(keys, ", "))
	}

	if violation {
		fmt.Println("result: VIOLATION - chain reaches a forbidden host:port")
		os.Exit(1)
	}
	fmt.Println("result: OK - no forbidden endpoint reachable")
}
