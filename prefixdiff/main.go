// Command prefixdiff computes the permission expansion and shrinkage between
// an old and a new set of prefix rules, expressed as minimal disjoint prefix
// sets, and verifies the result by exhaustive path enumeration.
//
// Rule semantics: a path is allowed iff the deepest matching rule allows it;
// among rules at the same depth (i.e. the same prefix), deny wins. Paths with
// no matching rule fall back to -default.
//
// Rule file format (one rule per line, '#' starts a comment):
//   allow a.b.c
//   deny  a.b
//   allow .          # the root prefix (matches everything)
//
// Usage:
//   go run main.go -alphabet a,b,c -depth 3 -old old.rules -new new.rules
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
)

// rules maps an encoded prefix path to its decision (true = allow).
// Conflicting rules on the same prefix resolve to deny.
type rules map[string]bool

func encode(path []string) string { return strings.Join(path, "\x00") }

func decode(key string) []string {
	if key == "" {
		return nil
	}
	return strings.Split(key, "\x00")
}

// decide reports whether path is allowed: deepest matching prefix wins,
// deny wins ties at the same depth, def applies when nothing matches.
func (r rules) decide(path []string, def bool) bool {
	for d := len(path); d >= 0; d-- {
		if allow, ok := r[encode(path[:d])]; ok {
			return allow
		}
	}
	return def
}

func parseRulesFile(name string, alphabet map[string]bool, maxDepth int) (rules, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	out := rules{}
	sc := bufio.NewScanner(f)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := strings.TrimSpace(sc.Text())
		if i := strings.Index(line, "#"); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 || (fields[0] != "allow" && fields[0] != "deny") {
			return nil, fmt.Errorf("%s:%d: want \"allow|deny <path>\", got %q", name, lineNo, line)
		}
		allow := fields[0] == "allow"
		var path []string
		if fields[1] != "." {
			path = strings.Split(fields[1], ".")
			for _, seg := range path {
				if !alphabet[seg] {
					return nil, fmt.Errorf("%s:%d: segment %q not in alphabet", name, lineNo, seg)
				}
			}
			if len(path) > maxDepth {
				return nil, fmt.Errorf("%s:%d: path depth %d exceeds -depth %d", name, lineNo, len(path), maxDepth)
			}
		}
		key := encode(path)
		// Same prefix (same depth): deny wins.
		if prev, ok := out[key]; ok {
			out[key] = prev && allow
		} else {
			out[key] = allow
		}
	}
	return out, sc.Err()
}

// enumPaths calls fn for every path of exactly the given depth over alphabet.
func enumPaths(alphabet []string, depth int, fn func(path []string)) {
	path := make([]string, 0, depth)
	var walk func(d int)
	walk = func(d int) {
		if d == depth {
			cp := make([]string, depth)
			copy(cp, path)
			fn(cp)
			return
		}
		for _, sym := range alphabet {
			path = append(path, sym)
			walk(d + 1)
			path = path[:len(path)-1]
		}
	}
	walk(0)
}

// minimalCover returns the unique minimal set of disjoint prefixes whose
// leaf expansions (down to depth) union exactly to target. A prefix is
// emitted iff its subtree is fully covered and its parent's is not.
func minimalCover(target map[string]bool, alphabet []string, depth int) [][]string {
	// Count covered leaves under every prefix.
	count := map[string]int{}
	for key := range target {
		path := decode(key)
		for d := 0; d <= len(path); d++ {
			count[encode(path[:d])]++
		}
	}
	full := 1
	for i := 0; i < depth; i++ {
		full *= len(alphabet)
	}

	var cover [][]string
	var walk func(path []string, subtreeSize int)
	walk = func(path []string, subtreeSize int) {
		n := count[encode(path)]
		if n == 0 {
			return
		}
		if n == subtreeSize {
			cp := make([]string, len(path))
			copy(cp, path)
			cover = append(cover, cp)
			return
		}
		childSize := subtreeSize / len(alphabet)
		for _, sym := range alphabet {
			walk(append(path, sym), childSize)
		}
	}
	walk(nil, full)

	sort.Slice(cover, func(i, j int) bool { return encode(cover[i]) < encode(cover[j]) })
	return cover
}

func formatPath(path []string) string {
	if len(path) == 0 {
		return "."
	}
	return strings.Join(path, ".")
}

// verifyCover independently checks, by full leaf enumeration, that cover is
// an exact, disjoint, redundancy-free prefix cover of target.
func verifyCover(name string, target map[string]bool, cover [][]string, alphabet []string, depth int) error {
	// Expand each prefix to its leaves; detect overlap and mismatch.
	seen := map[string]string{} // leaf key -> owning prefix
	for _, prefix := range cover {
		if len(prefix) > depth {
			return fmt.Errorf("%s: prefix %q deeper than %d", name, formatPath(prefix), depth)
		}
		owner := formatPath(prefix)
		enumPaths(alphabet, depth-len(prefix), func(suffix []string) {
			leaf := encode(append(append([]string{}, prefix...), suffix...))
			if prev, dup := seen[leaf]; dup {
				seen[leaf] = prev + " & " + owner + " (OVERLAP)"
			} else {
				seen[leaf] = owner
			}
		})
	}
	for leaf, owner := range seen {
		if strings.HasSuffix(owner, "(OVERLAP)") {
			return fmt.Errorf("%s: leaf %q covered by overlapping prefixes %s", name, formatPath(decode(leaf)), owner)
		}
		if !target[leaf] {
			return fmt.Errorf("%s: prefix %q covers leaf %q outside the target set", name, owner, formatPath(decode(leaf)))
		}
	}
	for leaf := range target {
		if _, ok := seen[leaf]; !ok {
			return fmt.Errorf("%s: target leaf %q not covered by any prefix", name, formatPath(decode(leaf)))
		}
	}
	// Redundancy: every prefix must be full (subtree entirely in target) and
	// maximal (parent subtree not entirely in target); otherwise the cover
	// is either not exact or not minimal.
	count := map[string]int{}
	for key := range target {
		path := decode(key)
		for d := 0; d <= len(path); d++ {
			count[encode(path[:d])]++
		}
	}
	size := map[int]int{0: 1}
	for d := 1; d <= depth; d++ {
		size[d] = size[d-1] * len(alphabet)
	}
	for _, prefix := range cover {
		key := encode(prefix)
		if count[key] != size[depth-len(prefix)] {
			return fmt.Errorf("%s: prefix %q is not fully covered; cover is inexact", name, formatPath(prefix))
		}
		if len(prefix) > 0 && count[encode(prefix[:len(prefix)-1])] == size[depth-len(prefix)+1] {
			return fmt.Errorf("%s: prefix %q is redundant; its parent is fully covered too", name, formatPath(prefix))
		}
	}
	return nil
}

func main() {
	alphaFlag := flag.String("alphabet", "", "comma-separated path alphabet (required)")
	depth := flag.Int("depth", 0, "maximum path depth (required, >= 0)")
	oldFile := flag.String("old", "", "old rules file (required)")
	newFile := flag.String("new", "", "new rules file (required)")
	defFlag := flag.String("default", "deny", "decision when no rule matches: allow|deny")
	noVerify := flag.Bool("no-verify", false, "skip exhaustive enumeration verification")
	flag.Parse()

	if *alphaFlag == "" || *oldFile == "" || *newFile == "" || *depth < 0 {
		flag.Usage()
		os.Exit(2)
	}
	defAllow := *defFlag == "allow"
	if *defFlag != "allow" && *defFlag != "deny" {
		fmt.Fprintln(os.Stderr, "-default must be allow or deny")
		os.Exit(2)
	}

	alphabet := strings.Split(*alphaFlag, ",")
	inAlphabet := map[string]bool{}
	for _, sym := range alphabet {
		if sym == "" || strings.ContainsAny(sym, ". \t") || inAlphabet[sym] {
			fmt.Fprintf(os.Stderr, "invalid alphabet symbol %q\n", sym)
			os.Exit(2)
		}
		inAlphabet[sym] = true
	}

	oldRules, err := parseRulesFile(*oldFile, inAlphabet, *depth)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	newRules, err := parseRulesFile(*newFile, inAlphabet, *depth)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	// Diff over the full leaf space.
	expansion := map[string]bool{} // denied before, allowed now
	shrinkage := map[string]bool{} // allowed before, denied now
	total := 0
	enumPaths(alphabet, *depth, func(path []string) {
		total++
		was := oldRules.decide(path, defAllow)
		now := newRules.decide(path, defAllow)
		switch {
		case !was && now:
			expansion[encode(path)] = true
		case was && !now:
			shrinkage[encode(path)] = true
		}
	})

	expCover := minimalCover(expansion, alphabet, *depth)
	shrCover := minimalCover(shrinkage, alphabet, *depth)

	fmt.Printf("leaves enumerated: %d (alphabet=%d, depth=%d, default=%s)\n",
		total, len(alphabet), *depth, *defFlag)
	fmt.Printf("\nexpansion: %d leaves, %d prefixes\n", len(expansion), len(expCover))
	for _, p := range expCover {
		fmt.Printf("  allow %s\n", formatPath(p))
	}
	fmt.Printf("\nshrinkage: %d leaves, %d prefixes\n", len(shrinkage), len(shrCover))
	for _, p := range shrCover {
		fmt.Printf("  deny %s\n", formatPath(p))
	}

	if !*noVerify {
		if err := verifyCover("expansion", expansion, expCover, alphabet, *depth); err != nil {
			fmt.Fprintln(os.Stderr, "VERIFY FAILED:", err)
			os.Exit(1)
		}
		if err := verifyCover("shrinkage", shrinkage, shrCover, alphabet, *depth); err != nil {
			fmt.Fprintln(os.Stderr, "VERIFY FAILED:", err)
			os.Exit(1)
		}
		fmt.Println("\nverify: OK (exact coverage, disjoint, no redundant prefixes)")
	}
}
