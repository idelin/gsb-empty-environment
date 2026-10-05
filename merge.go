package main

import (
	"path"
	"sort"
	"strings"
)

// EntryType classifies an archive item.
type EntryType string

const (
	TypeFile      EntryType = "file"
	TypeDir       EntryType = "dir"
	TypeTombstone EntryType = "tombstone"
)

// Conflict / suppression reasons.
const (
	ReasonDigestMismatch     = "digest-mismatch"
	ReasonTypeMismatch       = "type-mismatch"
	ReasonTombstoneVsContent = "tombstone-vs-content"

	SuppUnderFile      = "ancestor-is-file"
	SuppUnderTombstone = "ancestor-deleted"
)

// Entry is a single item inside one source archive.
// Digest is meaningful only for files (content hash).
type Entry struct {
	Path    string
	Type    EntryType
	Version int64
	Digest  string
}

// Source is one input archive.
type Source struct {
	Name    string
	Entries []Entry
}

// Candidate is an entry tagged with the source it came from.
type Candidate struct {
	Source  string    `json:"source"`
	Type    EntryType `json:"type"`
	Version int64     `json:"version"`
	Digest  string    `json:"digest,omitempty"`
}

// Provenance records which source an item (or a superseded item) came from.
type Provenance struct {
	Source  string `json:"source"`
	Version int64  `json:"version"`
	Digest  string `json:"digest,omitempty"`
}

// ManifestEntry is one item of the merged manifest.
type ManifestEntry struct {
	Path       string       `json:"path"`
	Type       EntryType    `json:"type"`
	Version    int64        `json:"version"`
	Digest     string       `json:"digest,omitempty"`
	Sources    []Provenance `json:"sources"`              // winning contributors
	Superseded []Provenance `json:"superseded,omitempty"` // older versions lost here
}

// Conflict is a path that could not be resolved automatically.
type Conflict struct {
	Path       string      `json:"path"`
	Reason     string      `json:"reason"`
	Candidates []Candidate `json:"candidates"`
}

// Suppressed is an entry dropped because an ancestor path resolved to a
// file or a tombstone (parent type determines the whole subtree).
type Suppressed struct {
	Path      string `json:"path"`
	Source    string `json:"source"`
	Reason    string `json:"reason"`
	BlockedBy string `json:"blockedBy"`
}

// Deletion records a tombstone that won: the path (and its subtree) is
// absent from the merged manifest.
type Deletion struct {
	Path       string       `json:"path"`
	Version    int64        `json:"version"`
	Sources    []Provenance `json:"sources"`
	Superseded []Provenance `json:"superseded,omitempty"`
}

// Result is the merge outcome. It is a pure function of the multiset of
// input sources: input order never affects it.
type Result struct {
	Sources    []string        `json:"sources"`
	Entries    []ManifestEntry `json:"entries"`
	Conflicts  []Conflict      `json:"conflicts,omitempty"`
	Suppressed []Suppressed    `json:"suppressed,omitempty"`
	Deleted    []Deletion      `json:"deleted,omitempty"`
}

func normPath(p string) string {
	p = strings.Trim(p, "/")
	if p == "" || p == "." {
		return ""
	}
	return path.Clean(p)
}

func sortCandidates(cs []Candidate) {
	sort.Slice(cs, func(i, j int) bool {
		a, b := cs[i], cs[j]
		if a.Source != b.Source {
			return a.Source < b.Source
		}
		if a.Type != b.Type {
			return a.Type < b.Type
		}
		if a.Version != b.Version {
			return a.Version < b.Version
		}
		return a.Digest < b.Digest
	})
}

func provOf(cs []Candidate) []Provenance {
	ps := make([]Provenance, 0, len(cs))
	for _, c := range cs {
		ps = append(ps, Provenance{Source: c.Source, Version: c.Version, Digest: c.Digest})
	}
	return ps
}

// blockedAncestor returns the shallowest strict ancestor of p that resolved
// to a file or a tombstone.
func blockedAncestor(p string, blocked map[string]EntryType) (string, EntryType, bool) {
	for i := 0; i < len(p); i++ {
		if p[i] == '/' {
			if t, ok := blocked[p[:i]]; ok {
				return p[:i], t, true
			}
		}
	}
	return "", "", false
}

// Merge combines any number of sources into a single manifest.
//
// Rules:
//  1. At each path the highest version wins.
//  2. Same version, same type, same digest: sources tie and share provenance.
//  3. Same version, files with different digests: conflict (digest-mismatch).
//  4. Same version, file vs dir: conflict (type-mismatch); files and
//     directories are never implicitly interchanged.
//  5. Same version, tombstone vs real content: conflict (tombstone-vs-content).
//  6. A winning tombstone deletes the path and its whole subtree.
//  7. A winning file blocks the whole subtree beneath its path: entries
//     under it from other sources are suppressed (parent path type
//     determines the whole subtree).
//  8. Conflicted paths are excluded from the manifest but do not block
//     their subtree; children are resolved independently.
func Merge(sources []Source) Result {
	type key struct {
		path string
		Candidate
	}
	seen := make(map[key]bool)
	byPath := make(map[string][]Candidate)
	names := make([]string, 0, len(sources))
	nameSeen := make(map[string]bool)

	for _, s := range sources {
		if !nameSeen[s.Name] {
			nameSeen[s.Name] = true
			names = append(names, s.Name)
		}
		for _, e := range s.Entries {
			p := normPath(e.Path)
			if p == "" {
				continue
			}
			c := Candidate{Source: s.Name, Type: e.Type, Version: e.Version}
			if e.Type == TypeFile {
				c.Digest = e.Digest
			}
			k := key{p, c}
			if seen[k] {
				continue
			}
			seen[k] = true
			byPath[p] = append(byPath[p], c)
		}
	}
	sort.Strings(names)

	paths := make([]string, 0, len(byPath))
	for p := range byPath {
		paths = append(paths, p)
	}
	sort.Strings(paths) // strict prefixes sort before their descendants

	res := Result{Sources: names}
	blocked := make(map[string]EntryType) // path -> winning file|tombstone

	for _, p := range paths {
		cands := byPath[p]
		sortCandidates(cands)

		if bp, bt, ok := blockedAncestor(p, blocked); ok {
			reason := SuppUnderFile
			if bt == TypeTombstone {
				reason = SuppUnderTombstone
			}
			for _, c := range cands {
				res.Suppressed = append(res.Suppressed, Suppressed{
					Path: p, Source: c.Source, Reason: reason, BlockedBy: bp,
				})
			}
			continue
		}

		maxV := cands[0].Version
		for _, c := range cands {
			if c.Version > maxV {
				maxV = c.Version
			}
		}
		var top, older []Candidate
		for _, c := range cands {
			if c.Version == maxV {
				top = append(top, c)
			} else {
				older = append(older, c)
			}
		}
		superseded := provOf(older)

		var hasFile, hasDir, hasTomb bool
		for _, c := range top {
			switch c.Type {
			case TypeFile:
				hasFile = true
			case TypeDir:
				hasDir = true
			case TypeTombstone:
				hasTomb = true
			}
		}

		switch {
		case hasTomb && (hasFile || hasDir):
			res.Conflicts = append(res.Conflicts, Conflict{p, ReasonTombstoneVsContent, top})
		case hasFile && hasDir:
			res.Conflicts = append(res.Conflicts, Conflict{p, ReasonTypeMismatch, top})
		case hasTomb:
			res.Deleted = append(res.Deleted, Deletion{
				Path: p, Version: maxV, Sources: provOf(top), Superseded: superseded,
			})
			blocked[p] = TypeTombstone
		case hasFile:
			same := true
			for _, c := range top[1:] {
				if c.Digest != top[0].Digest {
					same = false
					break
				}
			}
			if !same {
				res.Conflicts = append(res.Conflicts, Conflict{p, ReasonDigestMismatch, top})
			} else {
				res.Entries = append(res.Entries, ManifestEntry{
					Path: p, Type: TypeFile, Version: maxV, Digest: top[0].Digest,
					Sources: provOf(top), Superseded: superseded,
				})
				blocked[p] = TypeFile
			}
		default: // dir
			res.Entries = append(res.Entries, ManifestEntry{
				Path: p, Type: TypeDir, Version: maxV,
				Sources: provOf(top), Superseded: superseded,
			})
		}
	}
	return res
}
