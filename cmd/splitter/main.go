// Command splitter is a replayable local runner for the dual-target
// splitter. It applies a JSONL event log, and inspects or collects the
// resulting state. Opening the data dir always runs recovery first, so
// replaying the same log after a crash converges to the last legal event.
//
// Usage:
//
//	splitter -dir DATA replay events.jsonl   # apply events (use "-" for stdin)
//	splitter -dir DATA scan                  # committed pointer per key
//	splitter -dir DATA scan -reference       # full blob-store scan, highest version per key
//	splitter -dir DATA get KEY               # resolve one key via its pointer
//	splitter -dir DATA gc                    # delete unreferenced old copies
package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"splitter/splitter"
)

type jsonEvent struct {
	Key       string `json:"key"`
	Version   uint64 `json:"version"`
	Content   string `json:"content"`
	IsDeleted bool   `json:"is_deleted"`
}

type applyLine struct {
	Key     string `json:"key"`
	Version uint64 `json:"version"`
	Result  string `json:"result"`
	Detail  string `json:"detail,omitempty"`
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(argv []string) error {
	fs := flag.NewFlagSet("splitter", flag.ContinueOnError)
	dir := fs.String("dir", "splitter-data", "data directory")
	reference := fs.Bool("reference", false, "scan blob stores directly instead of pointers")
	if err := fs.Parse(argv); err != nil {
		return err
	}
	args := fs.Args()
	if len(args) == 0 {
		fs.Usage()
		return errors.New("missing command: replay|scan|get|gc")
	}

	switch args[0] {
	case "replay":
		if len(args) != 2 {
			return errors.New("usage: splitter -dir DATA replay events.jsonl")
		}
		sp, err := splitter.Open(splitter.Config{Dir: *dir})
		if err != nil {
			return err
		}
		return replay(sp, args[1])
	case "scan":
		sp, err := splitter.Open(splitter.Config{Dir: *dir})
		if err != nil {
			return err
		}
		return scan(sp, *reference)
	case "get":
		if len(args) != 2 {
			return errors.New("usage: splitter -dir DATA get KEY")
		}
		sp, err := splitter.Open(splitter.Config{Dir: *dir})
		if err != nil {
			return err
		}
		return get(sp, args[1])
	case "gc":
		sp, err := splitter.Open(splitter.Config{Dir: *dir})
		if err != nil {
			return err
		}
		return gc(sp)
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func replay(sp *splitter.Splitter, path string) error {
	var in io.Reader
	if path == "-" {
		in = os.Stdin
	} else {
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		in = f
	}
	enc := json.NewEncoder(os.Stdout)
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	counts := map[string]int{}
	lineNo := 0
	for sc.Scan() {
		lineNo++
		if len(sc.Bytes()) == 0 {
			continue
		}
		var je jsonEvent
		if err := json.Unmarshal(sc.Bytes(), &je); err != nil {
			return fmt.Errorf("line %d: %w", lineNo, err)
		}
		res, err := sp.Apply(splitter.Event{
			Key: je.Key, Version: je.Version,
			Content: []byte(je.Content), IsDeleted: je.IsDeleted,
		})
		out := applyLine{Key: je.Key, Version: je.Version}
		switch {
		case errors.Is(err, splitter.ErrConflict):
			out.Result, out.Detail = "conflict", err.Error()
		case err != nil:
			return fmt.Errorf("line %d: %w", lineNo, err)
		default:
			out.Result = string(res)
		}
		counts[out.Result]++
		if err := enc.Encode(out); err != nil {
			return err
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "summary: %v\n", counts)
	if counts["conflict"] > 0 {
		os.Exit(2)
	}
	return nil
}

func scan(sp *splitter.Splitter, reference bool) error {
	if reference {
		ptrs, err := sp.ReferenceScan()
		if err != nil {
			return err
		}
		for _, p := range ptrs {
			fmt.Printf("%s\t%d\t%s\t(reference)\n", p.Key, p.Version, p.Location)
		}
		return nil
	}
	for _, p := range sp.ScanAll() {
		fmt.Printf("%s\t%d\t%s\tdeleted=%v\n", p.Key, p.Version, p.Location, p.Deleted)
	}
	return nil
}

func get(sp *splitter.Splitter, key string) error {
	rec, err := sp.Get(key)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "key=%s version=%d location=%s deleted=%v\n",
		rec.Pointer.Key, rec.Pointer.Version, rec.Pointer.Location, rec.Pointer.Deleted)
	_, err = os.Stdout.Write(rec.Content)
	return err
}

func gc(sp *splitter.Splitter) error {
	removed, err := sp.GC()
	if err != nil {
		return err
	}
	for _, r := range removed {
		fmt.Println("removed", r)
	}
	fmt.Fprintf(os.Stderr, "gc: removed %d blob(s)\n", len(removed))
	return nil
}
