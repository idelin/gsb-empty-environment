package netsec

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
)

// Config selects the build-time configuration for closure computation.
type Config struct {
	// Root is the on-disk root of the release package. Required.
	Root string
	// GOOS and GOARCH select platform edges; empty values default to the
	// running platform.
	GOOS   string
	GOARCH string
	// Optional lists node names whose optional dependency edges are
	// activated for this build.
	Optional []string
}

// Build computes the dependency closure from roots and hashes every object
// actually readable under cfg. Required-edge objects that are missing,
// unreadable, or not regular files fail the build; the same applies to
// activated optional and matched platform objects, because once selected
// they are part of the signed closure.
func Build(g *Graph, cfg Config, roots ...string) (*Manifest, error) {
	if cfg.Root == "" {
		return nil, fmt.Errorf("netsec: Config.Root is required")
	}
	if len(roots) == 0 {
		return nil, fmt.Errorf("netsec: at least one root node is required")
	}
	goos, goarch := cfg.GOOS, cfg.GOARCH
	if goos == "" {
		goos = runtime.GOOS
	}
	if goarch == "" {
		goarch = runtime.GOARCH
	}
	active := make(map[string]bool, len(cfg.Optional))
	for _, name := range cfg.Optional {
		active[name] = true
	}

	reach := make(map[string]bool)
	usedOptional := make(map[string]bool)
	var stack []string
	push := func(name string) error {
		if g.nodes == nil {
			return fmt.Errorf("netsec: unknown node %q", name)
		}
		if _, ok := g.nodes[name]; !ok {
			return fmt.Errorf("netsec: unknown node %q", name)
		}
		if !reach[name] {
			reach[name] = true
			stack = append(stack, name)
		}
		return nil
	}
	for _, r := range roots {
		if err := push(r); err != nil {
			return nil, err
		}
	}
	for len(stack) > 0 {
		name := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, e := range g.edges[name] {
			follow := false
			switch e.kind {
			case EdgeRequired:
				follow = true
			case EdgeOptional:
				follow = active[e.to]
				if follow {
					usedOptional[e.to] = true
				}
			case EdgePlatform:
				follow = (e.goos == "" || e.goos == goos) &&
					(e.goarch == "" || e.goarch == goarch)
			}
			if follow {
				if err := push(e.to); err != nil {
					return nil, err
				}
			}
		}
	}

	owner := make(map[string]string)
	for _, name := range sortedKeys(reach) {
		for _, f := range g.nodes[name].files {
			rel, err := canonicalRel(f)
			if err != nil {
				return nil, fmt.Errorf("netsec: node %q: %w", name, err)
			}
			if prev, dup := owner[rel]; dup {
				return nil, fmt.Errorf("netsec: path %q claimed by both node %q and node %q", rel, prev, name)
			}
			owner[rel] = name
		}
	}

	entries := make([]Entry, 0, len(owner))
	for _, rel := range sortedKeys(owner) {
		e, err := hashObject(cfg.Root, rel)
		if err != nil {
			return nil, fmt.Errorf("netsec: node %q: %w", owner[rel], err)
		}
		entries = append(entries, e)
	}

	m := &Manifest{
		Version:  ManifestVersion,
		Platform: goos + "/" + goarch,
		Optional: sortedKeys(usedOptional),
		Entries:  entries,
	}
	m.buildIndex()
	return m, nil
}

func hashObject(root, rel string) (Entry, error) {
	full := filepath.Join(root, filepath.FromSlash(rel))
	st, err := os.Lstat(full)
	if errors.Is(err, fs.ErrNotExist) {
		return Entry{}, fmt.Errorf("%w: %s", ErrObjectMissing, rel)
	}
	if err != nil {
		return Entry{}, err
	}
	if !st.Mode().IsRegular() {
		return Entry{}, fmt.Errorf("%w: %s", ErrNotRegular, rel)
	}
	f, err := os.Open(full)
	if err != nil {
		return Entry{}, fmt.Errorf("%w: %s", ErrObjectUnreadable, rel)
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return Entry{}, fmt.Errorf("%w: %s", ErrObjectUnreadable, rel)
	}
	return Entry{
		Path:   rel,
		Mode:   modeBits(st.Mode()),
		Size:   n,
		Digest: digestPrefix + hex.EncodeToString(h.Sum(nil)),
	}, nil
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
