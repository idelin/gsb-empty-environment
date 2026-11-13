package artifactcmp

import (
	"errors"
	"path"
	"sort"
	"strings"
	"testing"
	"time"
)

// This test suite deliberately implements its own tree walker and reference
// oracle instead of reusing the package internals. The point is that the
// security properties are checked by an independent traversal, so a shared
// implementation bug cannot make a content replacement look reproducible.

const (
	builderA = "builder-a@host1"
	builderB = "builder-b@host2"
	commit   = "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b"
)

var (
	baseTime = time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	later    = baseTime.Add(7 * time.Hour)
)

func indexTree(t *testing.T, tree Tree) map[string]Node {
	t.Helper()
	out := make(map[string]Node, len(tree.Nodes))
	for _, node := range tree.Nodes {
		if _, exists := out[node.Path]; exists {
			t.Fatalf("fixture duplicate path %q", node.Path)
		}
		out[node.Path] = node
	}
	return out
}

// referenceClosure is an independent walker: starting at executable entries
// it opens files through the given tree map and follows every referenced
// relative path, resolving symlink hops itself. Executable entries are the
// seed set; a non-executable file becomes part of the closure only when an
// executable actually reads it.
func referenceClosure(t *testing.T, treeMap map[string]Node) ExecutableClosure {
	t.Helper()
	return func(entries []string) ([]string, error) {
		reads := map[string]bool{}
		var visit func(rel string, hops int) error
		visit = func(rel string, hops int) error {
			if hops > 32 {
				return errors.New("symlink cycle in reference walker")
			}
			node, ok := treeMap[rel]
			if !ok {
				return errors.New("dangling reference " + rel)
			}
			reads[rel] = true
			switch node.Type {
			case TypeSymlink:
				target := node.Target
				if path.IsAbs(target) {
					return nil
				}
				return visit(path.Clean(path.Join(path.Dir(rel), target)), hops+1)
			case TypeFile:
				if node.Mode&ExecutableBits == 0 {
					return nil
				}
				for _, line := range strings.Split(string(node.Content), "\n") {
					line = strings.TrimSpace(line)
					if !strings.HasPrefix(line, "load ") {
						continue
					}
					ref := strings.TrimSpace(strings.TrimPrefix(line, "load "))
					if ref == "" || path.IsAbs(ref) {
						continue
					}
					if err := visit(path.Clean(path.Join(path.Dir(rel), ref)), hops+1); err != nil {
						return err
					}
				}
			}
			return nil
		}
		for _, entry := range entries {
			if err := visit(entry, 0); err != nil {
				return nil, err
			}
		}
		out := make([]string, 0, len(reads))
		for p := range reads {
			out = append(out, p)
		}
		sort.Strings(out)
		return out, nil
	}
}

// referenceOracle is the independent comparison implementation used to
// cross-check which paths the package reports. It does not share any helper
// with the production code.
func referenceOracle(a, b map[string]Node, rules Rules, ignored map[string]bool, reads map[string]bool) []string {
	all := map[string]bool{}
	for p := range a {
		all[p] = true
	}
	for p := range b {
		all[p] = true
	}
	var diffs []string
	for p := range all {
		an, aok := a[p]
		bn, bok := b[p]
		if !aok || !bok {
			diffs = append(diffs, p)
			continue
		}
		if an.Type != bn.Type || an.Mode != bn.Mode {
			diffs = append(diffs, p)
			continue
		}
		if !rules.IgnoreOwner && (an.UID != bn.UID || an.GID != bn.GID) {
			diffs = append(diffs, p)
			continue
		}
		if !rules.IgnoreModTime && !an.ModTime.Equal(bn.ModTime) {
			diffs = append(diffs, p)
			continue
		}
		switch an.Type {
		case TypeFile:
			if string(an.Content) != string(bn.Content) {
				if ignored[p] && reads[p] {
					diffs = append(diffs, p)
				} else if !ignored[p] {
					diffs = append(diffs, p)
				}
			}
		case TypeSymlink:
			if an.Target != bn.Target {
				diffs = append(diffs, p)
			}
		}
	}
	sort.Strings(diffs)
	return diffs
}

func baseTree(builder string, t0 time.Time) Tree {
	return Tree{
		Builder: builder,
		Nodes: []Node{
			{Path: "bin", Type: TypeDir, Mode: 0o755, ModTime: t0, UID: 1000, GID: 1000, Builder: builder},
			{Path: "bin/app", Type: TypeFile, Mode: 0o755, Content: []byte("#!/bin/sh\nload ../etc/config.txt\necho ok\n"), ModTime: t0, UID: 1000, GID: 1000, Builder: builder},
			{Path: "etc", Type: TypeDir, Mode: 0o755, ModTime: t0, UID: 0, GID: 0, Builder: builder},
			{Path: "etc/config.txt", Type: TypeFile, Mode: 0o644, Content: []byte("mode=safe\n"), ModTime: t0, UID: 0, GID: 0, Builder: builder},
			{Path: "etc/debug.log", Type: TypeFile, Mode: 0o644, Content: []byte("debug=off\n"), ModTime: t0, UID: 0, GID: 0, Builder: builder},
			{Path: "usr", Type: TypeDir, Mode: 0o755, ModTime: t0, UID: 0, GID: 0, Builder: builder},
			{Path: "usr/run.sh", Type: TypeFile, Mode: 0o644, Content: []byte("echo stale\n"), ModTime: t0, UID: 0, GID: 0, Builder: builder},
			{Path: "link", Type: TypeSymlink, Mode: 0o777, Target: "etc/config.txt", ModTime: t0, UID: 0, GID: 0, Builder: builder},
		},
	}
}

func mutate(t *testing.T, tree Tree, fn func(*Node)) Tree {
	t.Helper()
	out := Tree{Builder: tree.Builder, Nodes: make([]Node, len(tree.Nodes))}
	copy(out.Nodes, tree.Nodes)
	for i := range out.Nodes {
		fn(&out.Nodes[i])
	}
	return out
}

func findNode(t *testing.T, tree Tree, p string) *Node {
	t.Helper()
	for i := range tree.Nodes {
		if tree.Nodes[i].Path == p {
			return &tree.Nodes[i]
		}
	}
	t.Fatalf("fixture missing path %q", p)
	return nil
}

func diffPaths(report *Report) []string {
	out := make([]string, 0, len(report.Differences))
	for _, d := range report.Differences {
		out = append(out, d.Path)
	}
	sort.Strings(out)
	return out
}

func assertOracleAgrees(t *testing.T, left, right Tree, rules Rules, report *Report) {
	t.Helper()
	lm := indexTree(t, left)
	rm := indexTree(t, right)
	ignored := map[string]bool{}
	for _, p := range rules.IgnoredContentPaths {
		ignored[p] = true
	}
	closure := referenceClosure(t, lm)
	readsList, err := closure(executableEntries(lm))
	if err != nil {
		t.Fatalf("reference closure failed: %v", err)
	}
	reads := map[string]bool{}
	for _, p := range readsList {
		reads[p] = true
	}
	want := referenceOracle(lm, rm, rules, ignored, reads)
	got := diffPaths(report)
	if strings.Join(want, "|") != strings.Join(got, "|") {
		t.Fatalf("oracle mismatch: oracle=%v package=%v", want, got)
	}
}

func TestEquivalentTreesIssueReceipt(t *testing.T) {
	left := baseTree(builderA, baseTime)
	right := baseTree(builderB, baseTime)
	rules := Rules{IgnoreModTime: true, IgnoreOwner: true}

	report, receipt, err := Compare(left, right, commit, rules, referenceClosure(t, indexTree(t, left)))
	if err != nil {
		t.Fatalf("compare failed: %v", err)
	}
	if !report.Equal || len(report.Differences) != 0 {
		t.Fatalf("expected equal trees, got %+v", report.Differences)
	}
	if receipt == nil {
		t.Fatal("receipt not issued for equivalent trees")
	}
	if receipt.SourceCommit != commit || receipt.BuilderLeft != builderA || receipt.BuilderRight != builderB {
		t.Fatalf("receipt bindings wrong: %+v", receipt)
	}
	if receipt.RootLeft != receipt.RootRight || receipt.RootLeft == "" {
		t.Fatalf("canonical roots not bound: %+v", receipt)
	}
	if !strings.Contains(receipt.CanonicalText, commit) || receipt.ReceiptDigest == "" {
		t.Fatalf("receipt text/digest missing: %+v", receipt)
	}
	assertOracleAgrees(t, left, right, rules, report)
}

func TestTimestampDifferenceIsAllowedOnlyWhenIgnored(t *testing.T) {
	left := baseTree(builderA, baseTime)
	right := baseTree(builderB, later)

	strictReport, _, err := Compare(left, right, commit, Rules{}, referenceClosure(t, indexTree(t, left)))
	if err != nil {
		t.Fatalf("strict compare failed: %v", err)
	}
	if strictReport.Equal {
		t.Fatal("mtime drift must be rejected without an explicit ignore rule")
	}

	rules := Rules{IgnoreModTime: true}
	report, receipt, err := Compare(left, right, commit, rules, referenceClosure(t, indexTree(t, left)))
	if err != nil {
		t.Fatalf("compare failed: %v", err)
	}
	if !report.Equal || receipt == nil {
		t.Fatalf("allowed mtime drift should pass, got %+v", report.Differences)
	}
	assertOracleAgrees(t, left, right, Rules{}, strictReport)
	assertOracleAgrees(t, left, right, rules, report)
}

func TestDebugCommentDifferenceInNamedNonExecutable(t *testing.T) {
	left := baseTree(builderA, baseTime)
	right := baseTree(builderB, baseTime)
	findNode(t, right, "etc/debug.log").Content = []byte("# builder B debug note\ndebug=off\n")

	rules := Rules{IgnoredContentPaths: []string{"etc/debug.log"}}
	report, receipt, err := Compare(left, right, commit, rules, referenceClosure(t, indexTree(t, left)))
	if err != nil {
		t.Fatalf("compare failed: %v", err)
	}
	if !report.Equal || receipt == nil {
		t.Fatalf("explicitly ignored non-executable content should pass: %+v", report.Differences)
	}

	// Without the explicit ignore the same byte difference is a hard failure:
	// reproducibility must never be extended to bytes that were not compared.
	strictReport, strictReceipt, err := Compare(left, right, commit, Rules{}, referenceClosure(t, indexTree(t, left)))
	if err != nil {
		t.Fatalf("strict compare failed: %v", err)
	}
	if strictReport.Equal || strictReceipt != nil {
		t.Fatal("un-ignored content difference must block the receipt")
	}
	assertOracleAgrees(t, left, right, rules, report)
	assertOracleAgrees(t, left, right, Rules{}, strictReport)
}

func TestScriptExecutableBitDifferenceRejected(t *testing.T) {
	left := baseTree(builderA, baseTime)
	right := baseTree(builderB, baseTime)
	findNode(t, right, "usr/run.sh").Mode = 0o755

	report, receipt, err := Compare(left, right, commit, Rules{IgnoreModTime: true, IgnoreOwner: true},
		referenceClosure(t, indexTree(t, left)))
	if err != nil {
		t.Fatalf("compare failed: %v", err)
	}
	if report.Equal || receipt != nil {
		t.Fatal("executable bit change must never be ignored")
	}
	if len(report.Differences) != 1 || report.Differences[0].Path != "usr/run.sh" {
		t.Fatalf("expected single minimal diff at usr/run.sh, got %+v", report.Differences)
	}
	if !strings.Contains(strings.Join(report.Differences[0].Reasons, ";"), "mode mismatch") {
		t.Fatalf("evidence must cite mode mismatch: %+v", report.Differences[0].Reasons)
	}
	if report.Differences[0].Left.Mode != 0o644 || report.Differences[0].Right.Mode != 0o755 {
		t.Fatalf("per-side mode evidence wrong: %+v", report.Differences[0])
	}
	assertOracleAgrees(t, left, right, Rules{IgnoreModTime: true, IgnoreOwner: true}, report)
}

func TestSymlinkTargetSwapRejected(t *testing.T) {
	left := baseTree(builderA, baseTime)
	right := baseTree(builderB, baseTime)
	findNode(t, right, "link").Target = "etc/debug.log"

	report, receipt, err := Compare(left, right, commit, Rules{IgnoreModTime: true, IgnoreOwner: true},
		referenceClosure(t, indexTree(t, left)))
	if err != nil {
		t.Fatalf("compare failed: %v", err)
	}
	if report.Equal || receipt != nil {
		t.Fatal("symlink target replacement must be rejected")
	}
	if len(report.Differences) != 1 || report.Differences[0].Path != "link" {
		t.Fatalf("expected minimal diff at link, got %+v", report.Differences)
	}
	if report.Differences[0].Left.SymlinkHash == "" || report.Differences[0].Right.SymlinkHash == "" {
		t.Fatal("symlink evidence must carry per-side target hashes")
	}
	assertOracleAgrees(t, left, right, Rules{IgnoreModTime: true, IgnoreOwner: true}, report)
}

func TestIgnoredContentEnteringClosureIsRejected(t *testing.T) {
	left := baseTree(builderA, baseTime)
	right := baseTree(builderB, baseTime)
	// The executable bin/app loads etc/config.txt, so its bytes are in the
	// execution closure even though it is a non-executable file.
	findNode(t, right, "etc/config.txt").Content = []byte("mode=EVIL\n")

	rules := Rules{IgnoredContentPaths: []string{"etc/config.txt"}}
	report, receipt, err := Compare(left, right, commit, rules, referenceClosure(t, indexTree(t, left)))
	if err != nil {
		t.Fatalf("compare failed: %v", err)
	}
	if report.Equal || receipt != nil {
		t.Fatal("ignored bytes reached by an executable must not be trusted")
	}
	if len(report.Differences) != 1 || report.Differences[0].Path != "etc/config.txt" {
		t.Fatalf("expected closure rejection at etc/config.txt, got %+v", report.Differences)
	}
	if !strings.Contains(report.Differences[0].Reasons[0], "executable closure") {
		t.Fatalf("reason must mention closure: %+v", report.Differences[0].Reasons)
	}
	assertOracleAgrees(t, left, right, rules, report)

	// The same ignored file that is never read stays acceptable: trust is
	// scoped strictly to bytes that execution cannot reach.
	left2 := baseTree(builderA, baseTime)
	right2 := baseTree(builderB, baseTime)
	findNode(t, right2, "etc/debug.log").Content = []byte("debug=on\n")
	rules2 := Rules{IgnoredContentPaths: []string{"etc/debug.log"}}
	report2, receipt2, err := Compare(left2, right2, commit, rules2, referenceClosure(t, indexTree(t, left2)))
	if err != nil {
		t.Fatalf("compare failed: %v", err)
	}
	if !report2.Equal || receipt2 == nil {
		t.Fatalf("unread ignored content should pass: %+v", report2.Differences)
	}
	assertOracleAgrees(t, left2, right2, rules2, report2)
}

func TestOneSidedPathAndExecutableContentRejected(t *testing.T) {
	left := baseTree(builderA, baseTime)
	right := baseTree(builderB, baseTime)
	findNode(t, right, "bin/app").Content = []byte("#!/bin/sh\necho compromised\n")

	report, _, err := Compare(left, right, commit, Rules{IgnoreModTime: true, IgnoreOwner: true},
		referenceClosure(t, indexTree(t, left)))
	if err != nil {
		t.Fatalf("compare failed: %v", err)
	}
	if report.Equal {
		t.Fatal("executable content replacement must be rejected")
	}
	if len(report.Differences) != 1 || report.Differences[0].Path != "bin/app" {
		t.Fatalf("expected minimal diff at bin/app, got %+v", report.Differences)
	}
	assertOracleAgrees(t, left, right, Rules{IgnoreModTime: true, IgnoreOwner: true}, report)

	oneSided := baseTree(builderB, baseTime)
	oneSided.Nodes = append(oneSided.Nodes, Node{
		Path: "etc/extra.txt", Type: TypeFile, Mode: 0o644, Content: []byte("only here"),
		ModTime: baseTime, Builder: builderB,
	})
	report2, receipt2, err := Compare(baseTree(builderA, baseTime), oneSided, commit, Rules{},
		referenceClosure(t, indexTree(t, baseTree(builderA, baseTime))))
	if err != nil {
		t.Fatalf("compare failed: %v", err)
	}
	if report2.Equal || receipt2 != nil || len(report2.Differences) != 1 ||
		report2.Differences[0].Path != "etc/extra.txt" {
		t.Fatalf("one-sided path must be the single minimal diff: %+v", report2)
	}
}

func TestInvalidPathsRejectedAtNormalization(t *testing.T) {
	bad := []string{
		"/etc/passwd",
		"../escape",
		"foo/../bar",
		"foo/./bar",
		"foo//bar",
		".",
		"foo/..",
		"",
	}
	for _, p := range bad {
		if err := ValidateRelPath(p); err == nil {
			t.Fatalf("path %q must be rejected", p)
		}
		tree := Tree{Builder: builderA, Nodes: []Node{
			{Path: p, Type: TypeFile, Mode: 0o644, Builder: builderA},
		}}
		if _, err := normalize(tree); err == nil {
			t.Fatalf("normalize must reject path %q", p)
		}
	}

	// A path whose parent is a symlink escapes the archive hierarchy.
	throughSymlink := Tree{Builder: builderA, Nodes: []Node{
		{Path: "ln", Type: TypeSymlink, Mode: 0o777, Target: "etc", Builder: builderA},
		{Path: "ln/escape.txt", Type: TypeFile, Mode: 0o644, Builder: builderA},
	}}
	if _, err := normalize(throughSymlink); err == nil {
		t.Fatal("path traversing a symlink parent must be rejected")
	}

	duplicate := Tree{Builder: builderA, Nodes: []Node{
		{Path: "a", Type: TypeFile, Mode: 0o644, Builder: builderA},
		{Path: "a", Type: TypeFile, Mode: 0o644, Builder: builderA},
	}}
	if _, err := normalize(duplicate); err == nil {
		t.Fatal("duplicate paths must be rejected")
	}
}

func TestIgnoreRulesCannotReachExecutableOrNonExplicitPaths(t *testing.T) {
	left := baseTree(builderA, baseTime)
	right := baseTree(builderB, baseTime)
	lm := indexTree(t, left)
	rm := indexTree(t, right)

	execRule := Rules{IgnoredContentPaths: []string{"bin/app"}}
	if err := validateRules(execRule, lm, rm); err == nil {
		t.Fatal("ignoring executable file content must be rejected")
	}

	dirRule := Rules{IgnoredContentPaths: []string{"etc"}}
	if err := validateRules(dirRule, lm, rm); err == nil {
		t.Fatal("ignoring a directory path must be rejected")
	}

	globRule := Rules{IgnoredContentPaths: []string{"etc/*.log"}}
	if err := validateRules(globRule, lm, rm); err == nil {
		t.Fatal("glob patterns are not allowed: only explicit caller-named paths")
	}

	missingRule := Rules{IgnoredContentPaths: []string{"etc/nope.txt"}}
	if err := validateRules(missingRule, lm, rm); err == nil {
		t.Fatal("ignoring an absent path must be rejected")
	}
}

func TestReceiptRefusesWeakBindings(t *testing.T) {
	left := baseTree(builderA, baseTime)
	right := baseTree(builderB, baseTime)
	closure := referenceClosure(t, indexTree(t, left))

	if _, _, err := Compare(left, right, "", Rules{}, closure); err == nil {
		t.Fatal("empty source commit must block receipt issuance")
	}
	if _, _, err := Compare(left, right, commit, Rules{}, nil); err == nil {
		t.Fatal("missing closure callback must be rejected")
	}

	noBuilder := left
	noBuilder.Builder = ""
	if _, _, err := Compare(noBuilder, right, commit, Rules{}, closure); err == nil {
		t.Fatal("tree without builder identity must be rejected")
	}
}

func TestRootHashIgnoresOnlyAllowedMetadata(t *testing.T) {
	left := baseTree(builderA, baseTime)
	right := baseTree(builderB, later)
	rules := Rules{IgnoreModTime: true}

	report, receipt, err := Compare(left, right, commit, rules, referenceClosure(t, indexTree(t, left)))
	if err != nil {
		t.Fatalf("compare failed: %v", err)
	}
	if !report.Equal || receipt.RootLeft != receipt.RootRight {
		t.Fatalf("canonical root must be stable under ignored mtime: %+v", report)
	}

	// A digest difference on a compared file must move the canonical root.
	mutated := baseTree(builderB, baseTime)
	findNode(t, mutated, "bin/app").Content = []byte("different-bytes")
	report2, _, err := Compare(baseTree(builderA, baseTime), mutated, commit, Rules{},
		referenceClosure(t, indexTree(t, baseTree(builderA, baseTime))))
	if err != nil {
		t.Fatalf("compare failed: %v", err)
	}
	if report2.Equal || report2.RootLeft == report2.RootRight {
		t.Fatal("content replacement must change the canonical root")
	}
}
