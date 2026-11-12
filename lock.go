// Package netsec verifies NuGet packages.lock.json files for a build
// gateway: version-range compliance of direct dependencies, contentHash
// integrity against the actual .nupkg payloads, per-framework resolved
// uniqueness, and rejection of orphaned transitive entries.
package netsec

import (
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

// EntryType is the lock-file dependency type.
type EntryType string

const (
	EntryTypeDirect            EntryType = "Direct"
	EntryTypeTransitive        EntryType = "Transitive"
	EntryTypeCentralTransitive EntryType = "CentralTransitive"
)

// Entry is a single package entry inside one target framework section.
type Entry struct {
	Type         EntryType         `json:"type"`
	Requested    string            `json:"requested,omitempty"`
	Resolved     string            `json:"resolved"`
	ContentHash  string            `json:"contentHash,omitempty"`
	Dependencies map[string]string `json:"dependencies,omitempty"`
}

// LockFile mirrors the packages.lock.json structure: target framework ->
// package ID -> entry.
type LockFile struct {
	Version      int                          `json:"version"`
	Dependencies map[string]map[string]*Entry `json:"dependencies"`
}

// ErrorKind classifies verification failures for programmatic handling.
type ErrorKind int

const (
	ErrKindParse ErrorKind = iota + 1
	ErrKindUnknownType
	ErrKindBadVersion
	ErrKindMissingRequested
	ErrKindBadRange
	ErrKindRangeViolation
	ErrKindMissingHash
	ErrKindInvalidHash
	ErrKindHashMismatch
	ErrKindDuplicateResolved
	ErrKindDanglingDependency
	ErrKindDependencyMismatch
	ErrKindOrphanTransitive
)

func (k ErrorKind) String() string {
	switch k {
	case ErrKindParse:
		return "parse error"
	case ErrKindUnknownType:
		return "unknown entry type"
	case ErrKindBadVersion:
		return "unparseable resolved version"
	case ErrKindMissingRequested:
		return "direct entry missing requested range"
	case ErrKindBadRange:
		return "unparseable version range"
	case ErrKindRangeViolation:
		return "resolved version outside requested range"
	case ErrKindMissingHash:
		return "missing contentHash"
	case ErrKindInvalidHash:
		return "contentHash is not base64 SHA-512"
	case ErrKindHashMismatch:
		return "contentHash mismatch"
	case ErrKindDuplicateResolved:
		return "conflicting resolved versions for package ID"
	case ErrKindDanglingDependency:
		return "dependency references unknown package"
	case ErrKindDependencyMismatch:
		return "dependency range not satisfied by resolved version"
	case ErrKindOrphanTransitive:
		return "transitive entry not referenced by any dependency"
	}
	return "unknown error"
}

// Error describes a single verification failure.
type Error struct {
	Kind      ErrorKind
	Framework string
	PackageID string
	Msg       string
}

func (e *Error) Error() string {
	loc := e.PackageID
	if e.Framework != "" {
		loc = e.Framework + "/" + e.PackageID
	}
	if loc == "" {
		return fmt.Sprintf("lock verification failed: %s: %s", e.Kind, e.Msg)
	}
	return fmt.Sprintf("lock verification failed: %s: %s: %s", e.Kind, loc, e.Msg)
}

// ActualHashKey builds the lookup key for the caller-supplied map of actual
// .nupkg SHA-512 hashes. Package IDs are matched case-insensitively.
func ActualHashKey(packageID, resolved string) string {
	return strings.ToLower(packageID) + "@" + resolved
}

// Parse decodes a packages.lock.json document.
func Parse(r io.Reader) (*LockFile, error) {
	var lf LockFile
	if err := json.NewDecoder(r).Decode(&lf); err != nil {
		return nil, &Error{Kind: ErrKindParse, Msg: err.Error()}
	}
	if lf.Dependencies == nil {
		lf.Dependencies = map[string]map[string]*Entry{}
	}
	return &lf, nil
}

// Verify checks every entry in every target framework. actualHashes maps
// ActualHashKey(packageID, resolved) to the base64 SHA-512 of the real
// .nupkg payload; every lock entry must have a matching actual hash.
// The first violation is returned as *Error.
func (lf *LockFile) Verify(actualHashes map[string]string) error {
	for _, fw := range sortedKeys(lf.Dependencies) {
		if err := verifyFramework(fw, lf.Dependencies[fw], actualHashes); err != nil {
			return err
		}
	}
	return nil
}

func verifyFramework(fw string, entries map[string]*Entry, actualHashes map[string]string) error {
	ids := sortedKeys(entries)

	resolvedByID := make(map[string]string, len(entries))
	for _, id := range ids {
		e := entries[id]
		switch e.Type {
		case EntryTypeDirect, EntryTypeTransitive, EntryTypeCentralTransitive:
		default:
			return &Error{Kind: ErrKindUnknownType, Framework: fw, PackageID: id,
				Msg: fmt.Sprintf("type %q is not Direct, Transitive or CentralTransitive", e.Type)}
		}
		resolved, err := ParseVersion(e.Resolved)
		if err != nil {
			return &Error{Kind: ErrKindBadVersion, Framework: fw, PackageID: id, Msg: err.Error()}
		}
		if e.ContentHash == "" {
			return &Error{Kind: ErrKindMissingHash, Framework: fw, PackageID: id,
				Msg: "entry has no contentHash"}
		}
		if raw, err := base64.StdEncoding.DecodeString(e.ContentHash); err != nil || len(raw) != sha512.Size {
			return &Error{Kind: ErrKindInvalidHash, Framework: fw, PackageID: id,
				Msg: "contentHash must be the standard-base64 encoding of a 64-byte SHA-512 digest"}
		}
		if e.Type == EntryTypeDirect {
			if strings.TrimSpace(e.Requested) == "" {
				return &Error{Kind: ErrKindMissingRequested, Framework: fw, PackageID: id,
					Msg: "direct entry has no requested version range"}
			}
			rng, err := ParseVersionRange(e.Requested)
			if err != nil {
				return &Error{Kind: ErrKindBadRange, Framework: fw, PackageID: id, Msg: err.Error()}
			}
			if !rng.SatisfiedBy(resolved) {
				return &Error{Kind: ErrKindRangeViolation, Framework: fw, PackageID: id,
					Msg: fmt.Sprintf("resolved %s does not satisfy requested %s", e.Resolved, e.Requested)}
			}
		}
		lower := strings.ToLower(id)
		if prev, ok := resolvedByID[lower]; ok {
			if prev != e.Resolved {
				return &Error{Kind: ErrKindDuplicateResolved, Framework: fw, PackageID: id,
					Msg: fmt.Sprintf("package ID %q already resolved to %s in this framework, conflicting entry resolves to %s", id, prev, e.Resolved)}
			}
		} else {
			resolvedByID[lower] = e.Resolved
		}
	}

	for _, id := range ids {
		e := entries[id]
		key := ActualHashKey(id, e.Resolved)
		actual, ok := actualHashes[key]
		if !ok {
			return &Error{Kind: ErrKindHashMismatch, Framework: fw, PackageID: id,
				Msg: fmt.Sprintf("no actual .nupkg hash provided for %s", key)}
		}
		if actual != e.ContentHash {
			return &Error{Kind: ErrKindHashMismatch, Framework: fw, PackageID: id,
				Msg: "contentHash does not match the SHA-512 of the actual .nupkg"}
		}
	}

	referenced := make(map[string]bool, len(entries))
	for _, id := range ids {
		e := entries[id]
		for _, depID := range sortedKeys(e.Dependencies) {
			target, ok := lookupEntry(entries, depID)
			if !ok {
				return &Error{Kind: ErrKindDanglingDependency, Framework: fw, PackageID: id,
					Msg: fmt.Sprintf("dependency %q has no entry in framework %s", depID, fw)}
			}
			rng, err := ParseVersionRange(e.Dependencies[depID])
			if err != nil {
				return &Error{Kind: ErrKindBadRange, Framework: fw, PackageID: id,
					Msg: fmt.Sprintf("dependency %q: %s", depID, err)}
			}
			targetResolved, _ := ParseVersion(target.Resolved)
			if !rng.SatisfiedBy(targetResolved) {
				return &Error{Kind: ErrKindDependencyMismatch, Framework: fw, PackageID: id,
					Msg: fmt.Sprintf("dependency %q requires %s but resolved entry is %s", depID, e.Dependencies[depID], target.Resolved)}
			}
			referenced[strings.ToLower(depID)] = true
		}
	}

	for _, id := range ids {
		e := entries[id]
		if e.Type == EntryTypeTransitive || e.Type == EntryTypeCentralTransitive {
			if !referenced[strings.ToLower(id)] {
				return &Error{Kind: ErrKindOrphanTransitive, Framework: fw, PackageID: id,
					Msg: fmt.Sprintf("%s entry is not referenced by any other entry's dependencies", e.Type)}
			}
		}
	}
	return nil
}

func lookupEntry(entries map[string]*Entry, id string) (*Entry, bool) {
	if e, ok := entries[id]; ok {
		return e, true
	}
	for k, e := range entries {
		if strings.EqualFold(k, id) {
			return e, true
		}
	}
	return nil, false
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
