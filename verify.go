package netsec

import (
	"encoding/base64"
	"sort"
	"strings"
)

// HashFunc returns the SHA-512 standard base64 content hash of the actual
// .nupkg bytes for the given package id and resolved version. The build
// gateway supplies this function; returning ("", false) means the package
// bytes are unavailable and the entry cannot be verified.
type HashFunc func(packageID, resolvedVersion string) (hash string, ok bool)

// Options configures Verify.
type Options struct {
	// ActualHash resolves the expected SHA-512 standard base64 hash of each
	// referenced .nupkg. Required.
	ActualHash HashFunc

	// RootDependencies lists the root PackageReference ids of each target
	// framework, as declared by the project being built. When a framework is
	// present, every Direct entry in the lock file must appear (compared
	// case-insensitively) in this list. Frameworks absent from the map are
	// skipped by this check.
	RootDependencies map[string][]string
}

// Verify validates a parsed NuGet packages.lock.json against the actual
// package hashes supplied by the caller. Every detected problem is returned;
// a nil result means the lock file passed all checks.
func Verify(lf *LockFile, opts Options) error {
	if lf == nil {
		return VerifyErrors{{Code: ErrInvalidLockFile, Message: "nil lock file"}}
	}
	if opts.ActualHash == nil {
		return VerifyErrors{{Code: ErrInvalidLockFile, Message: "no hash resolver provided"}}
	}
	if len(lf.Dependencies) == 0 {
		return VerifyErrors{{Code: ErrInvalidLockFile, Message: "lock file has no framework dependencies"}}
	}

	var errs VerifyErrors
	add := func(code, framework, packageID, message string) {
		errs = append(errs, VerifyError{Code: code, Framework: framework, PackageID: packageID, Message: message})
	}

	frameworks := make([]string, 0, len(lf.Dependencies))
	for fw := range lf.Dependencies {
		frameworks = append(frameworks, fw)
	}
	sort.Strings(frameworks)

	for _, fw := range frameworks {
		verifyFramework(fw, lf.Dependencies[fw], opts, add)
	}

	if len(errs) == 0 {
		return nil
	}
	return errs
}

type graphNode struct {
	id      string // lower-cased package id
	display string // id as written in the lock file
	deps    []string
}

func verifyFramework(fw string, entries map[string]LockEntry, opts Options,
	add func(code, fw, id, msg string)) {

	nodes := make(map[string]*graphNode, len(entries))
	order := make([]string, 0, len(entries))
	lockEntries := make(map[string]LockEntry, len(entries))

	ids := make([]string, 0, len(entries))
	for id := range entries {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	// Pass 1: per-entry structural, hash and version checks, plus duplicate
	// detection for case-insensitive package ids within this framework.
	for _, id := range ids {
		entry := entries[id]
		key := lowerCase(id)
		if node, exists := nodes[key]; exists {
			if entry.Resolved != lockEntries[node.id].Resolved {
				add(ErrDuplicateResolved, fw, id, "two entries resolve to different versions "+
					lockEntries[node.id].Resolved+" and "+entry.Resolved)
			}
			// Duplicate casing ids identify one package; keep the first
			// occurrence and skip repeated structural checks.
			continue
		}
		nodes[key] = &graphNode{id: key, display: id}
		lockEntries[key] = entry
		order = append(order, key)

		switch entry.Type {
		case TypeDirect, TypeTransitive, TypeCentralTransitive:
		default:
			add(ErrUnknownEntryType, fw, id, "unknown entry type "+entry.Type)
		}

		verifyHash(fw, id, entry, opts, add)
		verifyVersion(fw, id, entry, add)
	}

	// Build dependency edges using case-insensitive id resolution.
	for _, key := range order {
		node := nodes[key]
		entry := lockEntries[key]
		depIDs := make([]string, 0, len(entry.Dependencies))
		for depID := range entry.Dependencies {
			depIDs = append(depIDs, depID)
		}
		sort.Strings(depIDs)
		for _, depID := range depIDs {
			depKey := lowerCase(depID)
			node.deps = append(node.deps, depKey)
			if _, ok := nodes[depKey]; !ok {
				add(ErrDependencyMissing, fw, node.display, "depends on "+depID+" which has no lock entry")
			}
		}
	}

	// Direct entries must appear in the framework root dependency list
	// supplied by the caller. Comparison is case-insensitive.
	rootIDs := map[string]bool{}
	for _, key := range order {
		if lockEntries[key].Type == TypeDirect {
			rootIDs[key] = true
		}
	}
	declaredRoots, hasRootList := opts.RootDependencies[fw]
	if hasRootList {
		declared := map[string]bool{}
		for _, id := range declaredRoots {
			declared[lowerCase(id)] = true
		}
		for key := range rootIDs {
			if !declared[key] {
				add(ErrDirectNotInRoot, fw, nodes[key].display,
					"Direct entry missing from root framework dependencies")
			}
		}
	}

	// Orphan check: every non-Direct entry must be referenced by at least one
	// other entry's dependencies in this framework.
	referenced := map[string]bool{}
	for _, key := range order {
		for _, dep := range nodes[key].deps {
			referenced[dep] = true
		}
	}
	for _, key := range order {
		entry := lockEntries[key]
		if entry.Type != TypeDirect && !referenced[key] {
			add(ErrOrphanEntry, fw, nodes[key].display, entry.Type+" entry is not referenced by any dependency")
		}
	}

	verifyClosure(fw, nodes, order, rootIDs, add)
}

func verifyHash(fw, id string, entry LockEntry, opts Options,
	add func(code, fw, id, msg string)) {

	if strings.TrimSpace(entry.ContentHash) == "" {
		add(ErrMissingContentHash, fw, id, "entry has no contentHash")
		return
	}
	// Reject anything that is not canonical standard base64. The URL-safe
	// alphabet ('-' and '_'), missing padding or stray characters all fail.
	if !isCanonicalStdBase64(entry.ContentHash) {
		add(ErrNonStandardBase64, fw, id, "contentHash is not canonical standard base64")
		return
	}
	actual, ok := opts.ActualHash(id, entry.Resolved)
	if !ok {
		add(ErrHashUnavailable, fw, id, "no actual .nupkg hash supplied by caller")
		return
	}
	if actual != entry.ContentHash {
		add(ErrHashMismatch, fw, id, "contentHash does not match SHA-512 of the actual .nupkg")
	}
}

func verifyVersion(fw, id string, entry LockEntry, add func(code, fw, id, msg string)) {
	resolved, err := parseNugetVersion(entry.Resolved)
	if err != nil {
		add(ErrInvalidVersion, fw, id, "unparseable resolved version "+entry.Resolved)
		return
	}
	if strings.TrimSpace(entry.Requested) == "" {
		return
	}
	rng, err := parseVersionRange(entry.Requested)
	if err != nil {
		add(ErrInvalidVersion, fw, id, "unparseable requested range "+entry.Requested+": "+err.Error())
		return
	}
	if rng.belowLowerBound(resolved) {
		add(ErrVersionDowngrade, fw, id, "resolved "+entry.Resolved+" is below lower bound of "+entry.Requested)
		return
	}
	if !rng.contains(resolved) {
		add(ErrVersionOutOfRange, fw, id, "resolved "+entry.Resolved+" does not satisfy requested range "+entry.Requested)
	}
}

func verifyClosure(fw string, nodes map[string]*graphNode, order []string,
	rootIDs map[string]bool, add func(code, fw, id, msg string)) {

	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := map[string]int{}
	for _, key := range order {
		color[key] = white
	}

	cycleReported := map[string]bool{}

	var visit func(key string)
	visit = func(key string) {
		color[key] = gray
		node, ok := nodes[key]
		if !ok {
			color[key] = black
			return
		}
		for _, dep := range node.deps {
			switch color[dep] {
			case white:
				visit(dep)
			case gray:
				if !cycleReported[dep] {
					cycleReported[dep] = true
					add(ErrDependencyCycle, fw, nodes[dep].display, "dependency cycle detected at package")
				}
			}
		}
		color[key] = black
	}

	// Reachability is rooted at Direct entries; order is deterministic.
	for _, key := range order {
		if rootIDs[key] && color[key] == white {
			visit(key)
		}
	}

	for _, key := range order {
		if color[key] == white {
			add(ErrUnreachable, fw, nodes[key].display, "entry is not reachable from any Direct dependency")
		}
	}
}

// isCanonicalStdBase64 reports whether s decodes as standard (RFC 4648 with
// padding) base64 and is encoded canonically: no URL-safe characters, correct
// padding, no embedded whitespace.
func isCanonicalStdBase64(s string) bool {
	if s == "" {
		return false
	}
	if strings.ContainsAny(s, "-_") {
		return false
	}
	if strings.ContainsAny(s, " \t\r\n") {
		return false
	}
	if len(s)%4 != 0 {
		return false
	}
	decoded, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return false
	}
	// Re-encode and require byte-identical output to reject non-canonical
	// padding or alternative encodings that happen to decode.
	return base64.StdEncoding.EncodeToString(decoded) == s
}
