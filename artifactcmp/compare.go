// Package artifactcmp compares two independently produced build artifacts.
//
// The gate accepts two parsed archive trees and decides whether they are
// equivalent despite allowed metadata drift (for example mtimes). The package
// deliberately never treats un-compared bytes as trustworthy: file types,
// symlink targets, executable bits and executable file contents are always
// compared, and any content that is ignored but is reachable through an
// executable closure is rejected.
package artifactcmp

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"
)

// NodeType is the object type recorded by an archive parser.
type NodeType string

const (
	// TypeFile is a regular file.
	TypeFile NodeType = "file"
	// TypeDir is a directory.
	TypeDir NodeType = "dir"
	// TypeSymlink is a symbolic link whose target is in Target.
	TypeSymlink NodeType = "symlink"
)

// ExecutableBits are the mode bits that grant execution. None of them may be
// ignored by comparison rules.
const ExecutableBits uint32 = 0o111

// Node is one entry of a parsed archive tree.
//
// Regular file bytes live in Content; a symlink's target string lives in
// Target. ModTime, UID and GID are metadata that rules may ignore. Builder
// records the builder identity that produced the node.
type Node struct {
	Path    string
	Type    NodeType
	Mode    uint32
	Content []byte
	Target  string
	ModTime time.Time
	UID     int
	GID     int
	Builder string
}

// Tree is a parsed archive as produced by one builder.
type Tree struct {
	Builder string
	Nodes   []Node
}

// Rules declares what the caller is willing to ignore. Every ignore is opt-in
// and the constructor rejects attempts to ignore anything security relevant.
type Rules struct {
	// IgnoreModTime permits modification-time differences.
	IgnoreModTime bool
	// IgnoreOwner permits UID/GID differences.
	IgnoreOwner bool
	// IgnoredContentPaths lists explicit regular, non-executable file paths
	// whose contents may differ. Patterns are not supported: a rule cannot
	// silently reach a file the caller did not name.
	IgnoredContentPaths []string
}

// ExecutableClosure resolves the set of archive paths actually read when
// executing from the given entry paths. The callback is supplied by the caller
// (for example a loader/tracer over the archive).
type ExecutableClosure func(entries []string) (reads []string, err error)

// Evidence is the per-side record attached to a reported difference.
type Evidence struct {
	Builder     string
	Present     bool
	Type        NodeType
	Mode        uint32
	Digest      string
	SymlinkHash string
	ModTime     time.Time
	UID         int
	GID         int
}

// Difference is one minimal differing path with evidence from both sides.
type Difference struct {
	Path    string
	Reasons []string
	Left    Evidence
	Right   Evidence
}

// Report is the outcome of a comparison. Equal is true only when no path was
// rejected, including content-ignored paths that an executable closure reads.
type Report struct {
	Equal       bool
	Differences []Difference
	RootLeft    string
	RootRight   string
}

// Receipt is issued only for fully equivalent artifacts and binds the source
// commit, both builder identities, the rule summary and the canonical roots.
type Receipt struct {
	SourceCommit  string
	BuilderLeft   string
	BuilderRight  string
	RuleSummary   string
	RootLeft      string
	RootRight     string
	CanonicalText string
	ReceiptDigest string
}

// Compare normalizes both trees under rules, requires full path/type
// congruence, compares file digests and symlink targets, applies the
// executable closure policy and either returns a report of minimal
// differences or a receipt when the trees are fully equivalent.
func Compare(left, right Tree, sourceCommit string, rules Rules, closure ExecutableClosure) (*Report, *Receipt, error) {
	if closure == nil {
		return nil, nil, errors.New("artifactcmp: executable closure callback is required")
	}
	if strings.TrimSpace(sourceCommit) == "" {
		return nil, nil, errors.New("artifactcmp: source commit is required for a binding receipt")
	}

	leftMap, err := normalize(left)
	if err != nil {
		return nil, nil, fmt.Errorf("artifactcmp: left tree invalid: %w", err)
	}
	rightMap, err := normalize(right)
	if err != nil {
		return nil, nil, fmt.Errorf("artifactcmp: right tree invalid: %w", err)
	}
	if err := validateRules(rules, leftMap, rightMap); err != nil {
		return nil, nil, err
	}

	ignoredContent := make(map[string]bool, len(rules.IgnoredContentPaths))
	for _, p := range rules.IgnoredContentPaths {
		ignoredContent[p] = true
	}

	entries := executableEntries(leftMap)
	reads, err := closure(entries)
	if err != nil {
		return nil, nil, fmt.Errorf("artifactcmp: executable closure failed: %w", err)
	}
	readSet, err := normalizeClosureReads(reads, leftMap)
	if err != nil {
		return nil, nil, err
	}

	diffs := compareTrees(leftMap, rightMap, rules, ignoredContent, readSet)

	rootLeft := canonicalRoot(leftMap, rules, ignoredContent)
	rootRight := canonicalRoot(rightMap, rules, ignoredContent)

	report := &Report{
		Equal:       len(diffs) == 0,
		Differences: diffs,
		RootLeft:    rootLeft,
		RootRight:   rootRight,
	}
	if !report.Equal {
		return report, nil, nil
	}
	if rootLeft != rootRight {
		return nil, nil, fmt.Errorf("artifactcmp: internal error: equal trees have different roots %s vs %s", rootLeft, rootRight)
	}

	receipt := issueReceipt(sourceCommit, left.Builder, right.Builder, rules, rootLeft, rootRight)
	return report, receipt, nil
}

// ValidateRelPath checks that p is a canonical relative path: non-empty,
// unrooted, without ".", ".." or empty segments. It is exported so archive
// parsers can reuse the same path rules.
func ValidateRelPath(p string) error {
	if p == "" {
		return errors.New("empty path")
	}
	if path.IsAbs(p) {
		return fmt.Errorf("absolute path not allowed: %q", p)
	}
	if cleaned := path.Clean(p); cleaned != p {
		return fmt.Errorf("path is not canonical (contains dot segments or redundant separators): %q", p)
	}
	if p == "." || p == ".." {
		return fmt.Errorf("path must name an archive entry: %q", p)
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return fmt.Errorf("illegal path segment in %q", p)
		}
	}
	return nil
}

func normalize(tree Tree) (map[string]Node, error) {
	if strings.TrimSpace(tree.Builder) == "" {
		return nil, errors.New("tree has no builder identity")
	}
	out := make(map[string]Node, len(tree.Nodes))
	for _, node := range tree.Nodes {
		if err := ValidateRelPath(node.Path); err != nil {
			return nil, fmt.Errorf("node %q: %w", node.Path, err)
		}
		switch node.Type {
		case TypeFile, TypeDir, TypeSymlink:
		default:
			return nil, fmt.Errorf("node %q: unknown type %q", node.Path, node.Type)
		}
		if node.Builder != "" && node.Builder != tree.Builder {
			return nil, fmt.Errorf("node %q: builder %q does not match tree builder %q", node.Path, node.Builder, tree.Builder)
		}
		if _, dup := out[node.Path]; dup {
			return nil, fmt.Errorf("duplicate path %q", node.Path)
		}
		parent := path.Dir(node.Path)
		if parent != "." {
			ancestor, ok := out[parent]
			if !ok {
				return nil, fmt.Errorf("node %q: parent %q is not present", node.Path, parent)
			}
			if ancestor.Type == TypeSymlink {
				return nil, fmt.Errorf("node %q: path traverses symlink parent %q", node.Path, parent)
			}
			if ancestor.Type != TypeDir {
				return nil, fmt.Errorf("node %q: parent %q is not a directory", node.Path, parent)
			}
		}
		out[node.Path] = node
	}
	return out, nil
}

func validateRules(rules Rules, left, right map[string]Node) error {
	for _, p := range rules.IgnoredContentPaths {
		if err := ValidateRelPath(p); err != nil {
			return fmt.Errorf("artifactcmp: ignored content path %q invalid: %w", p, err)
		}
		for sideName, tree := range map[string]map[string]Node{"left": left, "right": right} {
			node, ok := tree[p]
			if !ok {
				return fmt.Errorf("artifactcmp: ignored content path %q absent from %s tree", p, sideName)
			}
			if node.Type != TypeFile {
				return fmt.Errorf("artifactcmp: ignored content path %q must be a regular file, got %s", p, node.Type)
			}
			if node.Mode&ExecutableBits != 0 {
				return fmt.Errorf("artifactcmp: cannot ignore content of executable file %q", p)
			}
		}
	}
	return nil
}

func executableEntries(nodes map[string]Node) []string {
	var entries []string
	for p, node := range nodes {
		if node.Type == TypeFile && node.Mode&ExecutableBits != 0 {
			entries = append(entries, p)
		}
	}
	sort.Strings(entries)
	return entries
}

func normalizeClosureReads(reads []string, nodes map[string]Node) (map[string]bool, error) {
	out := make(map[string]bool, len(reads))
	for _, p := range reads {
		if err := ValidateRelPath(p); err != nil {
			return nil, fmt.Errorf("artifactcmp: closure returned invalid path %q: %w", p, err)
		}
		if _, ok := nodes[p]; !ok {
			return nil, fmt.Errorf("artifactcmp: closure returned unknown path %q", p)
		}
		out[p] = true
	}
	return out, nil
}

func compareTrees(left, right map[string]Node, rules Rules, ignoredContent, closureReads map[string]bool) []Difference {
	paths := unionSortedPaths(left, right)
	var diffs []Difference
	index := make(map[string]int)

	add := func(p, reason string, l, r Node, lPresent, rPresent bool) {
		if idx, ok := index[p]; ok {
			diffs[idx].Reasons = append(diffs[idx].Reasons, reason)
			return
		}
		index[p] = len(diffs)
		diffs = append(diffs, Difference{
			Path:    p,
			Reasons: []string{reason},
			Left:    evidenceOf(l, lPresent),
			Right:   evidenceOf(r, rPresent),
		})
	}

	for _, p := range paths {
		l, lOK := left[p]
		r, rOK := right[p]

		if !lOK || !rOK {
			add(p, "path exists on only one side", l, r, lOK, rOK)
			continue
		}
		if l.Type != r.Type {
			add(p, fmt.Sprintf("type mismatch: %s != %s", l.Type, r.Type), l, r, true, true)
			continue
		}

		// Mode is always compared in full; the executable bits are part of
		// the mode and no rule can suppress them.
		if l.Mode != r.Mode {
			add(p, fmt.Sprintf("mode mismatch: %#o != %#o", l.Mode, r.Mode), l, r, true, true)
		}
		if !rules.IgnoreOwner && (l.UID != r.UID || l.GID != r.GID) {
			add(p, fmt.Sprintf("owner mismatch: %d:%d != %d:%d", l.UID, l.GID, r.UID, r.GID), l, r, true, true)
		}
		if !rules.IgnoreModTime && !l.ModTime.Equal(r.ModTime) {
			add(p, fmt.Sprintf("mtime mismatch: %s != %s",
				l.ModTime.UTC().Format(time.RFC3339Nano),
				r.ModTime.UTC().Format(time.RFC3339Nano)), l, r, true, true)
		}

		switch l.Type {
		case TypeFile:
			ld := digestBytes(l.Content)
			rd := digestBytes(r.Content)
			if ld == rd {
				continue
			}
			if ignoredContent[p] {
				// Ignored bytes are not trusted. If execution can reach the
				// file, the ignore is void and the build is rejected.
				if closureReads[p] {
					add(p, "content ignored by rules but path is reached by the executable closure", l, r, true, true)
				}
				continue
			}
			add(p, fmt.Sprintf("content digest mismatch: %s != %s", ld, rd), l, r, true, true)
		case TypeSymlink:
			lh := digestString(l.Target)
			rh := digestString(r.Target)
			if lh != rh {
				add(p, fmt.Sprintf("symlink target mismatch: %q != %q", l.Target, r.Target), l, r, true, true)
			}
		}
	}
	return diffs
}

func unionSortedPaths(left, right map[string]Node) []string {
	seen := make(map[string]bool, len(left)+len(right))
	for p := range left {
		seen[p] = true
	}
	for p := range right {
		seen[p] = true
	}
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

func evidenceOf(node Node, present bool) Evidence {
	if !present {
		return Evidence{Present: false}
	}
	e := Evidence{
		Builder: node.Builder,
		Present: true,
		Type:    node.Type,
		Mode:    node.Mode,
		ModTime: node.ModTime,
		UID:     node.UID,
		GID:     node.GID,
	}
	switch node.Type {
	case TypeFile:
		e.Digest = digestBytes(node.Content)
	case TypeSymlink:
		e.SymlinkHash = digestString(node.Target)
	}
	return e
}

func digestBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func digestString(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func canonicalRoot(nodes map[string]Node, rules Rules, ignoredContent map[string]bool) string {
	paths := make([]string, 0, len(nodes))
	for p := range nodes {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	h := sha256.New()
	for _, p := range paths {
		node := nodes[p]
		fmt.Fprintf(h, "path=%s\ntype=%s\nmode=%#o\n", p, node.Type, node.Mode)
		if !rules.IgnoreOwner {
			fmt.Fprintf(h, "uid=%d\ngid=%d\n", node.UID, node.GID)
		}
		if !rules.IgnoreModTime {
			fmt.Fprintf(h, "mtime=%d\n", node.ModTime.UTC().UnixNano())
		}
		switch node.Type {
		case TypeFile:
			if ignoredContent[p] {
				fmt.Fprint(h, "content=<ignored>\n")
			} else {
				fmt.Fprintf(h, "sha256=%s\n", digestBytes(node.Content))
			}
		case TypeSymlink:
			fmt.Fprintf(h, "target-sha256=%s\n", digestString(node.Target))
		case TypeDir:
			fmt.Fprint(h, "entry=dir\n")
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

// RuleSummary renders the effective rules as a stable string so a receipt
// records exactly what was and was not compared.
func RuleSummary(rules Rules) string {
	ignored := append([]string(nil), rules.IgnoredContentPaths...)
	sort.Strings(ignored)
	return fmt.Sprintf(
		"ignore_mtime=%t ignore_uid_gid=%t ignored_content_paths=%s; type_symlink_target_exec_bit_and_executable_content_always_compared=true",
		rules.IgnoreModTime, rules.IgnoreOwner, strings.Join(ignored, ","))
}

func issueReceipt(sourceCommit, builderLeft, builderRight string, rules Rules, rootLeft, rootRight string) *Receipt {
	text := strings.Join([]string{
		"artifact-equivalence-receipt v1",
		"source_commit=" + sourceCommit,
		"builder_left=" + builderLeft,
		"builder_right=" + builderRight,
		"rules=" + RuleSummary(rules),
		"canonical_root_left=" + rootLeft,
		"canonical_root_right=" + rootRight,
		"",
	}, "\n")
	sum := sha256.Sum256([]byte(text))
	return &Receipt{
		SourceCommit:  sourceCommit,
		BuilderLeft:   builderLeft,
		BuilderRight:  builderRight,
		RuleSummary:   RuleSummary(rules),
		RootLeft:      rootLeft,
		RootRight:     rootRight,
		CanonicalText: text,
		ReceiptDigest: hex.EncodeToString(sum[:]),
	}
}
