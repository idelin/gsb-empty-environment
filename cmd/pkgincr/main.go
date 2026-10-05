// Command pkgincr incrementally packages a directory tree into a tar
// archive, driven by an ordered include/exclude rule file.
//
// Usage:
//
//	pkgincr -root DIR -rules FILE -out ARCHIVE.tar [-manifest FILE] [-v]
//
// Rule file format (later rules override earlier ones):
//
//	# comment
//	- /build/          exclude the build directory and everything below it
//	+ /build/keep.txt  ... except this file (sub-rules re-include)
//	- *.o              exclude any base name matching *.o
//	+ **/important/**  re-include anything under an "important" directory
package main

import (
	"flag"
	"fmt"
	"os"

	"pkgincr"
)

func main() {
	root := flag.String("root", ".", "directory tree to package")
	rulesFile := flag.String("rules", "", "file with ordered +/- rules (empty: include everything)")
	outPath := flag.String("out", "", "output tar archive (required)")
	manifestPath := flag.String("manifest", "", "manifest file for incremental runs")
	verbose := flag.Bool("v", false, "print the decisive rule for every visited path")
	flag.Parse()

	if *outPath == "" {
		fmt.Fprintln(os.Stderr, "pkgincr: -out is required")
		flag.Usage()
		os.Exit(2)
	}
	if err := run(*root, *rulesFile, *outPath, *manifestPath, *verbose); err != nil {
		fmt.Fprintln(os.Stderr, "pkgincr:", err)
		os.Exit(1)
	}
}

func run(root, rulesFile, outPath, manifestPath string, verbose bool) error {
	var rules []pkgincr.Rule
	if rulesFile != "" {
		data, err := os.ReadFile(rulesFile)
		if err != nil {
			return err
		}
		rules, err = pkgincr.ParseRules(string(data))
		if err != nil {
			return err
		}
	}

	res, err := pkgincr.Walk(root, rules)
	if err != nil {
		return err
	}

	for _, e := range res.Entries {
		if e.Included || verbose {
			fmt.Printf("%-8s %-40s %s\n", decisionWord(e.Included), e.Path, decisiveText(rules, e))
		}
	}
	for _, p := range res.Pruned {
		fmt.Printf("%-8s %-40s %s\n", "pruned", p, "(subtree skipped: no later rule can match inside)")
	}

	var prev *pkgincr.Manifest
	if manifestPath != "" {
		prev, err = pkgincr.LoadManifest(manifestPath)
		if err != nil {
			return err
		}
	}
	out, err := os.Create(outPath)
	if err != nil {
		return err
	}
	next, stats, err := pkgincr.Pack(root, res, out, prev)
	if err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	if manifestPath != "" {
		if err := next.Save(manifestPath); err != nil {
			return err
		}
	}
	fmt.Printf("packaged %d entries (%d unchanged, %d removed) -> %s\n",
		stats.Written, stats.Unchanged, len(stats.Removed), outPath)
	return nil
}

func decisionWord(included bool) string {
	if included {
		return "include"
	}
	return "exclude"
}

func decisiveText(rules []pkgincr.Rule, e *pkgincr.Entry) string {
	if e.Rule < 0 {
		return "(default: include)"
	}
	via := ""
	if e.ViaAncestor {
		via = " via ancestor"
	}
	return fmt.Sprintf("rule #%d: %s%s", e.Rule+1, rules[e.Rule], via)
}
