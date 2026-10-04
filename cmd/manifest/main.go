// Command manifest is the CLI for the versioned manifest store.
//
// Usage:
//
//	manifest init   <dir>
//	manifest put    <dir> <file>...        add files as segments, commit new version
//	manifest rm     <dir> <seg-id>...      remove segments, commit new version
//	manifest show   <dir>                  show current version and segments
//	manifest log    <dir>                  dump all committed versions
//	manifest cat    <dir> <seg-id>         print a segment's content to stdout
//	manifest verify <dir>                  reopen and fully validate the store
//	manifest gc     <dir>                  collect unreferenced segments
package main

import (
	"fmt"
	"os"

	"manifestmgr"
)

func die(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}

func openStore(dir string) *manifestmgr.Manager {
	m, err := manifestmgr.Open(manifestmgr.OSFS{Root: dir})
	if err != nil {
		die(err)
	}
	return m
}

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: manifest <init|put|rm|show|log|cat|verify|gc> <dir> [args...]")
		os.Exit(2)
	}
	cmd, dir := os.Args[1], os.Args[2]
	args := os.Args[3:]

	switch cmd {
	case "init":
		_, err := manifestmgr.Create(manifestmgr.OSFS{Root: dir})
		if err != nil {
			die(err)
		}
		fmt.Println("initialized empty store at", dir)

	case "put":
		if len(args) == 0 {
			die(fmt.Errorf("put requires at least one file"))
		}
		m := openStore(dir)
		adds := map[string][]byte{}
		for _, f := range args {
			data, err := os.ReadFile(f)
			if err != nil {
				die(err)
			}
			adds[f] = data
		}
		v, err := m.Publish(adds, nil)
		if err != nil {
			die(err)
		}
		fmt.Printf("committed version %d (%d segment(s) added)\n", v, len(adds))

	case "rm":
		if len(args) == 0 {
			die(fmt.Errorf("rm requires at least one segment id"))
		}
		m := openStore(dir)
		v, err := m.Publish(nil, args)
		if err != nil {
			die(err)
		}
		fmt.Printf("committed version %d (%d segment(s) removed)\n", v, len(args))

	case "show":
		m := openStore(dir)
		cur := m.Current()
		if cur == nil {
			fmt.Println("version 0 (empty)")
			return
		}
		printRecord(cur)

	case "log":
		m := openStore(dir)
		for _, r := range m.Versions() {
			printRecord(r)
			fmt.Println()
		}

	case "cat":
		if len(args) != 1 {
			die(fmt.Errorf("cat requires exactly one segment id"))
		}
		m := openStore(dir)
		snap := m.AcquireSnapshot()
		defer snap.Close()
		data, err := snap.Read(args[0])
		if err != nil {
			die(err)
		}
		os.Stdout.Write(data)

	case "verify":
		m := openStore(dir) // Open performs full validation
		n := 0
		if cur := m.Current(); cur != nil {
			n = len(cur.Segments)
		}
		fmt.Printf("ok: version %d, %d segment(s) verified\n", m.Version(), n)

	case "gc":
		m := openStore(dir)
		rep, err := m.GC()
		if err != nil {
			die(err)
		}
		fmt.Printf("deleted: %v\nkept:    %v\norphans: %v\n", rep.Deleted, rep.Kept, rep.Orphans)

	default:
		fmt.Fprintln(os.Stderr, "unknown command:", cmd)
		os.Exit(2)
	}
}

func printRecord(r *manifestmgr.Record) {
	fmt.Printf("version %d (prev %s)\n", r.Version, short(r.PrevDigest))
	for _, s := range r.Added {
		fmt.Printf("  + %s size=%d sha256=%s\n", s.ID, s.Size, short(s.Digest))
	}
	for _, id := range r.Removed {
		fmt.Printf("  - %s\n", id)
	}
	fmt.Printf("  segments (%d):\n", len(r.Segments))
	for _, s := range r.Segments {
		fmt.Printf("    %s size=%d sha256=%s\n", s.ID, s.Size, short(s.Digest))
	}
}

func short(h string) string {
	if len(h) > 12 {
		return h[:12] + "..."
	}
	if h == "" {
		return "(genesis)"
	}
	return h
}
