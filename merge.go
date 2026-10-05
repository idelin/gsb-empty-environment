package main

import (
	"fmt"
	"path"
	"sort"
	"strings"
)

// EntryType is the kind of an archive entry.
type EntryType string

const (
	TypeFile      EntryType = "file"
	TypeDir       EntryType = "dir"
	TypeTombstone EntryType = "tombstone"
)

// Entry is a single item in a source archive.
// Digest is required for files and must be empty for dirs and tombstones.
type Entry struct {
	Path    string    `json:"path"`
	Type    EntryType `json:"type"`
	Version int64     `json:"version"`
	Digest  string    `json:"digest,omitempty"`
}

// Source is one input archive: a named set of entries.
type Source struct {
	Name    string  `json:"name"`
	Entries []Entry `json:"entries"`
}

// ManifestEntry is a merged item plus the sources it came from.
type ManifestEntry struct {
	Path    string    `json:"path"`
	Type    EntryType `json:"type"`
	Version int64     `json:"version"`
	Digest  string    `json:"digest,omitempty"`
	Sources []string  `json:"sources"`
}

// Conflict describes a merge problem. Kinds:
//   type-mismatch      same path, same top version, different entry types
//   digest-mismatch    same path, same top version, files with different digests
//   prefix-conflict    a file entry has surviving entries beneath it
//   tombstone-conflict a tombstone has surviving (newer) entries beneath it
type Conflict struct {
	Path    string `json:"path"`
	Kind    string `json:"kind"`
	Message string `json:"message"`
}

// Result is the merge manifest plus any conflicts.
// The inputs are never modified.
type Result struct {
	Entries   []ManifestEntry `json:"entries"`
	Conflicts []Conflict      `json:"conflicts,omitempty"`
}

type sourcedEntry struct {
	entry  Entry
	source string
}

// Merge combines sources into a manifest. It is a pure function of the
// multiset of sources: input order never affects the output.
//
// Rules:
//  1. Per path, the highest version wins. Ties must agree on type, and
//     files must agree on digest, otherwise it is a conflict.
//  2. A winning tombstone deletes every entry beneath its path whose
//     version is <= the tombstone version. A newer entry surviving under
//     a tombstone is a tombstone-conflict.
//  3. Directories and files are never interchanged implicitly: a file
//     may not have surviving entries beneath it (prefix-conflict), and
//     a same-version type change at one path is a type-mismatch.
func Merge(sources []Source) Result {
	byPath := make(map[string][]sourcedEntry)
	for _, src := range sources {
		for _, e := range src.Entries {
			byPath[e.Path] = append(byPath[e.Path], sourcedEntry{entry: e, source: src.Name})
		}
	}

	winners := make(map[string]ManifestEntry)
	var conflicts []Conflict

	// Rule 1: per-path version/digest/type resolution.
	for p, list := range byPath {
		maxV := list[0].entry.Version
		for _, se := range list[1:] {
			if se.entry.Version > maxV {
				maxV = se.entry.Version
			}
		}
		var tops []sourcedEntry
		for _, se := range list {
			if se.entry.Version == maxV {
				tops = append(tops, se)
			}
		}
		first := tops[0].entry
		typeMismatch, digestMismatch := false, false
		for _, se := range tops {
			if se.entry.Type != first.Type {
				typeMismatch = true
			}
			if first.Type == TypeFile && se.entry.Digest != first.Digest {
				digestMismatch = true
			}
		}
		srcs := sourceNames(tops)
		switch {
		case typeMismatch:
			conflicts = append(conflicts, Conflict{
				Path: p, Kind: "type-mismatch",
				Message: fmt.Sprintf("path %q has conflicting types at version %d across %s",
					p, maxV, strings.Join(srcs, ", ")),
			})
		case digestMismatch:
			conflicts = append(conflicts, Conflict{
				Path: p, Kind: "digest-mismatch",
				Message: fmt.Sprintf("path %q has conflicting digests at version %d across %s",
					p, maxV, strings.Join(srcs, ", ")),
			})
		default:
			winners[p] = ManifestEntry{
				Path: p, Type: first.Type, Version: first.Version,
				Digest: first.Digest, Sources: srcs,
			}
		}
	}

	// Rule 2: tombstones shadow their subtree.
	for _, tw := range sortedWinners(winners) {
		if tw.Type != TypeTombstone {
			continue
		}
		for q, wq := range winners {
			if q == tw.Path || !isUnder(tw.Path, q) {
				continue
			}
			if wq.Version <= tw.Version {
				delete(winners, q)
			} else {
				conflicts = append(conflicts, Conflict{
					Path: tw.Path, Kind: "tombstone-conflict",
					Message: fmt.Sprintf("tombstone %q (v%d) has surviving descendant %q (v%d)",
						tw.Path, tw.Version, q, wq.Version),
				})
			}
		}
	}

	// Rule 3: a file may not have surviving entries beneath it.
	isFile := make(map[string]bool)
	for p, w := range winners {
		if w.Type == TypeFile {
			isFile[p] = true
		}
	}
	seen := make(map[string]bool)
	for q := range winners {
		for _, a := range ancestors(q) {
			if !isFile[a] {
				continue
			}
			key := a + "\x00" + q
			if seen[key] {
				continue
			}
			seen[key] = true
			conflicts = append(conflicts, Conflict{
				Path: a, Kind: "prefix-conflict",
				Message: fmt.Sprintf("file %q (v%d) cannot contain entry %q",
					a, winners[a].Version, q),
			})
		}
	}

	entries := sortedWinners(winners)
	sort.Slice(conflicts, func(i, j int) bool {
		if conflicts[i].Path != conflicts[j].Path {
			return conflicts[i].Path < conflicts[j].Path
		}
		if conflicts[i].Kind != conflicts[j].Kind {
			return conflicts[i].Kind < conflicts[j].Kind
		}
		return conflicts[i].Message < conflicts[j].Message
	})
	return Result{Entries: entries, Conflicts: conflicts}
}

func sourceNames(list []sourcedEntry) []string {
	set := make(map[string]bool)
	for _, se := range list {
		set[se.source] = true
	}
	names := make([]string, 0, len(set))
	for n := range set {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func sortedWinners(winners map[string]ManifestEntry) []ManifestEntry {
	out := make([]ManifestEntry, 0, len(winners))
	for _, w := range winners {
		out = append(out, w)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// isUnder reports whether q is strictly beneath directory p.
func isUnder(p, q string) bool {
	return strings.HasPrefix(q, p+"/")
}

// ancestors returns the proper ancestors of p, nearest first.
func ancestors(p string) []string {
	var out []string
	for {
		i := strings.LastIndex(p, "/")
		if i < 0 {
			return out
		}
		p = p[:i]
		out = append(out, p)
	}
}

// cleanPath normalizes an entry path and rejects unsafe forms.
func cleanPath(p string) (string, error) {
	if p == "" {
		return "", fmt.Errorf("empty path")
	}
	if strings.HasPrefix(p, "/") {
		return "", fmt.Errorf("absolute path %q", p)
	}
	c := path.Clean(p)
	if c == "." || c == ".." || strings.HasPrefix(c, "../") {
		return "", fmt.Errorf("path %q escapes the archive root", p)
	}
	return c, nil
}
