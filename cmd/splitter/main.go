// Command splitter is a replayable CLI front-end for the dual-target splitter.
//
// Usage:
//
//	splitter ingest -dir DATA -events events.jsonl   # replay an event log
//	splitter get -dir DATA -key KEY
//	splitter scan -dir DATA                          # committed pointer view
//	splitter scan-reference -dir DATA                # full-scan highest-version oracle
//	splitter snapshot create|release|list -dir DATA [-id ID]
//	splitter gc -dir DATA
//	splitter recover -dir DATA
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"

	"splitter/splitter"
)

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: splitter <ingest|get|scan|scan-reference|snapshot|gc|recover> [flags]")
		os.Exit(2)
	}
	cmd := os.Args[1]
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	dir := fs.String("dir", "data", "data directory")
	events := fs.String("events", "", "JSONL event file (ingest)")
	key := fs.String("key", "", "primary key (get)")
	id := fs.String("id", "", "snapshot id (snapshot release)")
	fs.Parse(os.Args[2:])

	s, err := splitter.Open(*dir)
	if err != nil {
		fatal(err)
	}

	switch cmd {
	case "ingest":
		cmdIngest(s, *events)
	case "get":
		rec, err := s.Get(*key)
		if err != nil {
			fatal(err)
		}
		printJSON(rec)
	case "scan":
		recs, err := s.Scan()
		if err != nil {
			fatal(err)
		}
		for _, r := range recs {
			printJSON(r)
		}
	case "scan-reference":
		ref, err := splitter.ScanReference(s.Online, s.Archive)
		if err != nil {
			fatal(err)
		}
		keys := make([]string, 0, len(ref))
		for k := range ref {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			printJSON(ref[k])
		}
	case "snapshot":
		cmdSnapshot(s, fs.Args(), *id)
	case "gc":
		removed, err := s.GC()
		if err != nil {
			fatal(err)
		}
		for _, r := range removed {
			fmt.Printf("removed %s %s@%d\n", r.Location, r.Key, r.Version)
		}
		fmt.Printf("gc: %d copies removed\n", len(removed))
	case "recover":
		if err := s.Recover(); err != nil {
			fatal(err)
		}
		fmt.Println("recover: ok")
	default:
		fmt.Fprintln(os.Stderr, "unknown command:", cmd)
		os.Exit(2)
	}
}

func cmdIngest(s *splitter.Store, path string) {
	f, err := os.Open(path)
	if err != nil {
		fatal(err)
	}
	defer f.Close()
	var applied, idempotent, stale, conflict int
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	line := 0
	for sc.Scan() {
		line++
		var ev splitter.Event
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			fatal(fmt.Errorf("line %d: %v", line, err))
		}
		prev, existed := s.Pointer(ev.Key)
		err := s.Apply(ev)
		switch err {
		case nil:
			if existed && prev.Version == ev.Version {
				idempotent++
			} else {
				applied++
			}
		case splitter.ErrStale:
			stale++
			fmt.Printf("line %d: stale %s@%d (delayed/duplicate, ignored)\n", line, ev.Key, ev.Version)
		case splitter.ErrConflict:
			conflict++
			fmt.Printf("line %d: CONFLICT %s@%d (same version, different content)\n", line, ev.Key, ev.Version)
		default:
			fatal(fmt.Errorf("line %d: %v", line, err))
		}
	}
	if err := sc.Err(); err != nil {
		fatal(err)
	}
	fmt.Printf("ingest: applied=%d idempotent=%d stale=%d conflict=%d\n", applied, idempotent, stale, conflict)
}

func cmdSnapshot(s *splitter.Store, args []string, id string) {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: splitter snapshot <create|release|list>")
		os.Exit(2)
	}
	switch args[0] {
	case "create":
		sid, err := s.BeginSnapshot()
		if err != nil {
			fatal(err)
		}
		fmt.Println(sid)
	case "release":
		if err := s.ReleaseSnapshot(id); err != nil {
			fatal(err)
		}
		fmt.Println("released", id)
	case "list":
		ids, err := s.Snapshots()
		if err != nil {
			fatal(err)
		}
		for _, sid := range ids {
			fmt.Println(sid)
		}
	default:
		fmt.Fprintln(os.Stderr, "usage: splitter snapshot <create|release|list>")
		os.Exit(2)
	}
}

func printJSON(v interface{}) {
	data, _ := json.Marshal(v)
	fmt.Println(string(data))
}
