// Command gsbsecure demonstrates the virtual filesystem, mount-tree
// adjudication and anti-escape path resolver on a small container scenario,
// then runs the bounded adversarial searcher.
package main

import (
	"fmt"
	"os"

	"formal.local/w27-6467acc7b4/adversarial"
	"formal.local/w27-6467acc7b4/mount"
	"formal.local/w27-6467acc7b4/resolve"
	"formal.local/w27-6467acc7b4/userns"
	"formal.local/w27-6467acc7b4/vfs"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	fs := vfs.New()
	secret, err := fs.Mkfile(fs.HostRoot, "host_secret", []byte("TOP-SECRET"), 0o600, 0, 0)
	if err != nil {
		return err
	}
	root, err := fs.Mkdir(fs.HostRoot, "container", 0o755, 0, 0)
	if err != nil {
		return err
	}
	work, err := fs.Mkdir(root, "work", 0o777, 0, 0)
	if err != nil {
		return err
	}
	if _, err := fs.Symlink(work, "evil", "../../host_secret", 0, 0); err != nil {
		return err
	}

	userNS := userns.NewInitial()
	ns := mount.NewRootNamespace("container_mnt", root, userNS)
	ns.Lock(ns.RootMount())

	secure := resolve.NewSecure(fs, ns)
	vulnerable := resolve.NewPrefix(fs, ns)

	fmt.Println("== escape attempts against /work/evil ==")
	if res, err := vulnerable.Open("/work/evil", resolve.Flags{}, nil); err != nil {
		fmt.Println("prefix resolver: error:", err)
	} else {
		fmt.Printf("prefix resolver: reached %q (escaped=%v)\n", vfs.PathOf(res.Node), !res.InRoot)
	}
	if _, err := secure.Open("/work/evil", resolve.DefaultSecureFlags(), nil); err != nil {
		fmt.Println("secure resolver: refused ->", err)
	} else {
		fmt.Println("secure resolver: unexpectedly succeeded")
	}

	fmt.Println()
	fmt.Println("== bounded adversarial search (max 6 steps) ==")
	sc := adversarial.BuildStandardScenario()
	report := adversarial.NewSearcher(sc).Run()
	fmt.Printf("states visited : %d\n", report.StatesVisited)
	fmt.Printf("sequences      : %d\n", report.Sequences)
	fmt.Printf("interleavings  : %d\n", report.Interleavings)
	fmt.Printf("prefix escapes : %d\n", len(report.Findings))
	fmt.Printf("secure escapes : %d\n", report.SecureEscapes)

	minimal := shortestFinding(report.Findings)
	if minimal != nil {
		fmt.Println()
		fmt.Println("== minimal demonstrated escape ==")
		fmt.Printf("open %s -> %s\n", minimal.OpenPath, minimal.HostPath)
		for _, action := range minimal.Sequence {
			fmt.Println("  ", action.String())
		}
		fmt.Println("secure verdict:", minimal.SecureError)
	}

	_ = secret
	return nil
}

func shortestFinding(findings []adversarial.Finding) *adversarial.Finding {
	var best *adversarial.Finding
	for i := range findings {
		if best == nil || len(findings[i].Sequence) < len(best.Sequence) {
			best = &findings[i]
		}
	}
	return best
}
