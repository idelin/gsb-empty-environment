package main

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// maxSymlinkExpansions bounds symlink chasing during path resolution so
// that symlink rings (a -> b -> a, or self-loops) are rejected.
const maxSymlinkExpansions = 40

// vnode is a node in the virtual filesystem built during validation.
type vnode struct {
	kind   entryType
	target string // symlink only: raw link target
}

type vfs struct {
	nodes map[string]*vnode
}

// cleanRel rejects absolute paths and anything escaping the root, and
// returns the canonical slash-separated relative form.
func cleanRel(p string) (string, error) {
	if p == "" {
		return "", fmt.Errorf("empty path")
	}
	if strings.IndexByte(p, 0) >= 0 {
		return "", fmt.Errorf("path %q contains NUL", p)
	}
	if path.IsAbs(p) || filepath.IsAbs(p) {
		return "", fmt.Errorf("absolute path %q is not allowed", p)
	}
	c := path.Clean(p)
	if c == "." || c == ".." || strings.HasPrefix(c, "../") {
		return "", fmt.Errorf("path %q escapes the extraction root", p)
	}
	return c, nil
}

// resolve expands symlinks along p within the virtual filesystem. The
// result is guaranteed to stay inside the root; rings and escapes are
// reported as errors.
func (v *vfs) resolve(p string) (string, error) {
	cur := p
	for depth := 0; ; depth++ {
		if cur == ".." || strings.HasPrefix(cur, "../") {
			return "", fmt.Errorf("path %q escapes the extraction root via symlink", p)
		}
		comps := strings.Split(cur, "/")
		expanded := false
		prefix := ""
		for i, c := range comps {
			cand := c
			if prefix != "" {
				cand = prefix + "/" + c
			}
			n, ok := v.nodes[cand]
			if ok && n.kind == entSymlink {
				if depth >= maxSymlinkExpansions {
					return "", fmt.Errorf("path %q: symlink loop detected", p)
				}
				rest := strings.Join(comps[i+1:], "/")
				joined := n.target
				if base := path.Dir(cand); base != "." {
					joined = base + "/" + joined
				}
				if rest != "" {
					joined += "/" + rest
				}
				cur = path.Clean(joined)
				expanded = true
				break
			}
			prefix = cand
		}
		if !expanded {
			return cur, nil
		}
	}
}

// checkPrefixes ensures every existing ancestor of a resolved path is a
// directory (symlinks were already expanded by resolve).
func (v *vfs) checkPrefixes(resolved string) error {
	comps := strings.Split(resolved, "/")
	prefix := ""
	for _, c := range comps[:len(comps)-1] {
		if prefix == "" {
			prefix = c
		} else {
			prefix += "/" + c
		}
		if n, ok := v.nodes[prefix]; ok && n.kind != entDir {
			return fmt.Errorf("path component %q is not a directory", prefix)
		}
	}
	return nil
}

// validateArchive checks every entry in order against a virtual
// filesystem and annotates entries with their resolved in-root paths.
// Nothing touches disk here.
func validateArchive(entries []*entry) error {
	v := &vfs{nodes: make(map[string]*vnode)}
	for _, e := range entries {
		rel, err := cleanRel(e.path)
		if err != nil {
			return fmt.Errorf("entry %q: %w", e.path, err)
		}
		resolved, err := v.resolve(rel)
		if err != nil {
			return fmt.Errorf("entry %q: %w", rel, err)
		}
		if err := v.checkPrefixes(resolved); err != nil {
			return fmt.Errorf("entry %q: %w", rel, err)
		}
		switch e.typ {
		case entDir:
			if n, ok := v.nodes[resolved]; ok && n.kind != entDir {
				return fmt.Errorf("entry %q: type conflict, already a %s", rel, kindName(n.kind))
			}
			v.nodes[resolved] = &vnode{kind: entDir}
		case entFile:
			if n, ok := v.nodes[resolved]; ok && n.kind != entFile {
				return fmt.Errorf("entry %q: type conflict, already a %s", rel, kindName(n.kind))
			}
			v.nodes[resolved] = &vnode{kind: entFile}
		case entSymlink:
			if e.target == "" {
				return fmt.Errorf("entry %q: empty symlink target", rel)
			}
			if path.IsAbs(e.target) || filepath.IsAbs(e.target) {
				return fmt.Errorf("entry %q: absolute symlink target %q is not allowed", rel, e.target)
			}
			joined := e.target
			if base := path.Dir(resolved); base != "." {
				joined = base + "/" + joined
			}
			if c := path.Clean(joined); c == ".." || strings.HasPrefix(c, "../") {
				return fmt.Errorf("entry %q: symlink target %q escapes the extraction root", rel, e.target)
			}
			if n, ok := v.nodes[resolved]; ok && n.kind != entSymlink {
				return fmt.Errorf("entry %q: type conflict, already a %s", rel, kindName(n.kind))
			}
			v.nodes[resolved] = &vnode{kind: entSymlink, target: e.target}
		case entHardlink:
			trel, err := cleanRel(e.target)
			if err != nil {
				return fmt.Errorf("entry %q: hardlink target: %w", rel, err)
			}
			tres, err := v.resolve(trel)
			if err != nil {
				return fmt.Errorf("entry %q: hardlink target: %w", rel, err)
			}
			n, ok := v.nodes[tres]
			if !ok {
				return fmt.Errorf("entry %q: hardlink target %q is not a previously verified archive object", rel, trel)
			}
			if n.kind != entFile {
				return fmt.Errorf("entry %q: hardlink target %q is a %s, only regular files are allowed", rel, trel, kindName(n.kind))
			}
			if old, ok := v.nodes[resolved]; ok && old.kind != entFile && old.kind != entHardlink {
				return fmt.Errorf("entry %q: type conflict, already a %s", rel, kindName(old.kind))
			}
			v.nodes[resolved] = &vnode{kind: entHardlink}
			e.linkResolved = tres
		default:
			return fmt.Errorf("entry %q: unknown type", rel)
		}
		e.path = rel
		e.resolved = resolved
	}
	return nil
}

func kindName(k entryType) string {
	switch k {
	case entDir:
		return "directory"
	case entFile:
		return "file"
	case entSymlink:
		return "symlink"
	case entHardlink:
		return "hardlink"
	}
	return "unknown"
}

// stagingFor returns the parent directory and name pattern of the
// temporary staging directory used for root. The staging directory is a
// sibling of the destination sharing its name as a prefix, so the final
// os.Rename is atomic on the same filesystem.
func stagingFor(root string) (dir, pattern string) {
	clean := filepath.Clean(root)
	return filepath.Dir(clean), "." + filepath.Base(clean) + ".tmp-"
}

// extractArchive validates entries, materializes them into a staging
// directory next to root, then atomically renames it into place. Any
// failure removes the staging directory created by this call.
func extractArchive(entries []*entry, root string) (err error) {
	if err := validateArchive(entries); err != nil {
		return err
	}
	if _, lerr := os.Lstat(root); lerr == nil {
		return fmt.Errorf("destination %q already exists", root)
	} else if !os.IsNotExist(lerr) {
		return lerr
	}
	dir, pattern := stagingFor(root)
	tmp, err := os.MkdirTemp(dir, pattern)
	if err != nil {
		return fmt.Errorf("creating staging directory: %w", err)
	}
	if err := os.Chmod(tmp, 0o755); err != nil {
		os.RemoveAll(tmp)
		return fmt.Errorf("preparing staging directory: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			os.RemoveAll(tmp)
		}
	}()
	for _, e := range entries {
		dst := filepath.Join(tmp, filepath.FromSlash(e.resolved))
		switch e.typ {
		case entDir:
			if err := os.MkdirAll(dst, 0o755); err != nil {
				return fmt.Errorf("mkdir %q: %w", e.path, err)
			}
		case entFile:
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return fmt.Errorf("mkdir for %q: %w", e.path, err)
			}
			mode := e.mode.Perm()
			if mode == 0 {
				mode = 0o644
			}
			if err := os.WriteFile(dst, e.data, mode); err != nil {
				return fmt.Errorf("writing %q: %w", e.path, err)
			}
		case entSymlink:
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return fmt.Errorf("mkdir for %q: %w", e.path, err)
			}
			if err := os.Symlink(e.target, dst); err != nil {
				return fmt.Errorf("symlink %q: %w", e.path, err)
			}
		case entHardlink:
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return fmt.Errorf("mkdir for %q: %w", e.path, err)
			}
			src := filepath.Join(tmp, filepath.FromSlash(e.linkResolved))
			if err := os.Link(src, dst); err != nil {
				return fmt.Errorf("hardlink %q: %w", e.path, err)
			}
		}
	}
	if err := os.Rename(tmp, root); err != nil {
		return fmt.Errorf("moving staging directory into place: %w", err)
	}
	committed = true
	return nil
}

// extract reads the archive at archivePath and extracts it under root.
func extract(archivePath, root string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()
	entries, err := readArchive(f)
	if err != nil {
		return err
	}
	return extractArchive(entries, root)
}
