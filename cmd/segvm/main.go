// Command segvm is a CLI for the versioned segment manifest store.
//
// Usage:
//
//	segvm init  <dir>
//	segvm put   <dir> <name>=<file> [...]   publish a version adding segments
//	segvm rm    <dir> <name> [...]          publish a version removing segments
//	segvm list  <dir>                       list all versions (adds/removes)
//	segvm show  <dir> [version]             show segment set of a version
//	segvm cat   <dir> <name>                print segment content (current version)
//	segvm gc    <dir>                       reclaim unreferenced segment files
//	segvm check <dir>                       validate chain, digests, log tail
package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"segvm"
)

func die(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "segvm: "+format+"\n", args...)
	os.Exit(1)
}

func main() {
	if len(os.Args) < 3 {
		die("usage: segvm <init|put|rm|list|show|cat|gc|check> <dir> [args...]")
	}
	cmd, dir := os.Args[1], os.Args[2]
	args := os.Args[3:]

	m, err := segvm.Open(segvm.OSFS{}, dir)
	if err != nil {
		die("open: %v", err)
	}

	switch cmd {
	case "init":
		fmt.Printf("initialized %s (version %d)\n", dir, m.Version())

	case "put":
		if len(args) == 0 {
			die("put needs at least one <name>=<file> pair")
		}
		var adds []segvm.Change
		for _, pair := range args {
			name, file, ok := strings.Cut(pair, "=")
			if !ok || name == "" || file == "" {
				die("bad put argument %q, want name=file", pair)
			}
			data, err := os.ReadFile(file)
			if err != nil {
				die("read %s: %v", file, err)
			}
			adds = append(adds, segvm.Change{Name: name, Data: data})
		}
		v, err := m.Publish(adds, nil)
		if err != nil {
			die("publish: %v", err)
		}
		fmt.Printf("published version %d (%d segment(s) added)\n", v, len(adds))

	case "rm":
		if len(args) == 0 {
			die("rm needs at least one segment name")
		}
		v, err := m.Publish(nil, args)
		if err != nil {
			die("publish: %v", err)
		}
		fmt.Printf("published version %d (%d segment(s) removed)\n", v, len(args))

	case "list":
		for _, r := range m.Versions() {
			fmt.Printf("version %d\n", r.Version)
			for _, a := range r.Adds {
				fmt.Printf("  + %-16s digest=%s size=%d\n", a.Name, short(a.Digest), a.Size)
			}
			for _, rm := range r.Removes {
				fmt.Printf("  - %s\n", rm)
			}
		}

	case "show":
		v := m.Version()
		if len(args) == 1 {
			parsed, err := strconv.ParseUint(args[0], 10, 64)
			if err != nil {
				die("bad version %q", args[0])
			}
			v = parsed
		} else if len(args) > 1 {
			die("show takes at most one version")
		}
		state, err := m.StateAt(v)
		if err != nil {
			die("%v", err)
		}
		fmt.Printf("version %d: %d segment(s)\n", v, len(state))
		for name, meta := range state {
			fmt.Printf("  %-16s digest=%s size=%d\n", name, short(meta.Digest), meta.Size)
		}

	case "cat":
		if len(args) != 1 {
			die("cat needs exactly one segment name")
		}
		data, err := m.Read(args[0])
		if err != nil {
			die("%v", err)
		}
		os.Stdout.Write(data)

	case "gc":
		res, err := m.GC()
		if err != nil {
			die("gc: %v", err)
		}
		fmt.Printf("removed %d segment file(s), %d tmp file(s); %d orphan(s) kept:\n",
			len(res.Removed), len(res.Tmp), len(res.Orphans))
		for _, o := range res.Orphans {
			fmt.Printf("  orphan: %s\n", o)
		}

	case "check":
		fmt.Printf("OK: version %d, %d live segment(s), chain/digests/log-tail verified\n",
			m.Version(), len(m.Segments()))

	default:
		die("unknown command %q", cmd)
	}
}

func short(digest string) string {
	if len(digest) > 12 {
		return digest[:12] + "..."
	}
	return digest
}
