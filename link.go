package oci

import (
	"errors"
	"fmt"
	"path"
	"strings"
)

var errSymlinkLoop = errors.New("symbolic link loop")

func (fs *FileSystem) resolveHardlink(node *Node) (bool, LinkRecord) {
	raw := firstNonEmpty(node.HardlinkTarget, node.Linkname, node.Target)
	location := AnalyzeLinkTarget(node.Path, raw)
	record := LinkRecord{
		Path:        node.Path,
		Kind:        "hardlink",
		RawTarget:   raw,
		Absolute:    location.Absolute,
		EscapesRoot: location.EscapesRoot,
		LayerIndex:  node.LayerIndex,
		Order:       node.Order,
	}

	if location.EscapesRoot {
		return false, record
	}

	candidates := hardlinkCandidates(raw, location)
	for _, candidate := range candidates {
		target, err := fs.Stat(candidate)
		if err != nil || target == nil {
			continue
		}
		fs.adoptHardlink(node, target)
		record.ResolvedTarget = target.Path
		record.TargetLayer = target.LayerIndex
		record.CrossLayer = node.LayerIndex != target.LayerIndex
		return true, record
	}

	// A target can already have been removed by a whiteout/type replacement.
	// An explicit upper hard link may still preserve that inode's payload.
	for i := len(fs.Residue) - 1; i >= 0; i-- {
		residue := fs.Residue[i]
		for _, candidate := range candidates {
			if residue.Path != candidate || residue.Node == nil {
				continue
			}
			target := residue.Node
			fs.adoptHardlink(node, target)
			record.ResolvedTarget = target.Path
			record.TargetLayer = target.LayerIndex
			record.TargetHidden = true
			record.CrossLayer = node.LayerIndex != target.LayerIndex
			return true, record
		}
	}

	return false, record
}

func hardlinkCandidates(raw string, location LinkLocation) []string {
	var candidates []string
	seen := map[string]bool{}
	add := func(value string) {
		if value == "" || seen[value] {
			return
		}
		seen[value] = true
		candidates = append(candidates, value)
	}

	if clean, err := CleanPath(raw); err == nil {
		add(clean)
	}
	add(location.RootRelative)
	add(location.Resolved)
	if !location.Absolute && raw != "" {
		add(path.Base(raw))
	}
	return candidates
}

func (fs *FileSystem) adoptHardlink(dst, src *Node) {
	ownMode := dst.Mode
	ownCapability := dst.Capability
	ownCapabilityError := dst.CapabilityError
	ownXattrs := cloneXattrs(dst.Xattrs)

	dst.Type = src.Type
	dst.Mode = src.Mode
	// Explicit setuid/setgid bits on the hard-link header are applied to the
	// shared inode and therefore cannot make the result less dangerous.
	if ownMode&ModeSetUID != 0 {
		dst.Mode |= ModeSetUID
	}
	if ownMode&ModeSetGID != 0 {
		dst.Mode |= ModeSetGID
	}
	if ownMode&ModeSticky != 0 {
		dst.Mode |= ModeSticky
	}
	dst.UID, dst.Uid = src.UID, src.UID
	dst.GID, dst.Gid = src.GID, src.GID
	dst.Uname, dst.Gname = src.Uname, src.Gname
	dst.Size = src.Size
	dst.Xattrs = cloneXattrs(src.Xattrs)
	if dst.Xattrs == nil {
		dst.Xattrs = map[string][]byte{}
	}
	for key, value := range ownXattrs {
		dst.Xattrs[key] = append([]byte(nil), value...)
	}
	dst.Capability = src.Capability
	if ownCapability != nil {
		dst.Capability = ownCapability
	}
	if ownCapabilityError != "" {
		dst.CapabilityError = ownCapabilityError
	}
	dst.Content = append([]byte(nil), src.Content...)
	dst.ContentTruncated = src.ContentTruncated
	dst.InodeID = src.InodeID
	dst.ResolvedHardlinkTarget = src.Path
	dst.LinkCount = src.LinkCount + 1

	if fs != nil && fs.Inodes != nil {
		if inode, ok := fs.Inodes[src.InodeID]; ok {
			inode.Nlink++
			if inode.Paths == nil {
				inode.Paths = map[string]struct{}{}
			}
			inode.Paths[dst.Path] = struct{}{}
		}
	}
}

// ResolvePath follows symbolic links and returns the terminal visible node.
// The second return value is the cleaned root-relative terminal path.
func (fs *FileSystem) ResolvePath(name string) (*Node, string, error) {
	clean, err := CleanPath(name)
	if err != nil {
		return nil, "", err
	}

	currentPath := clean
	for follow := 0; follow < 40; follow++ {
		link, linkPath, rawTarget, remaining, err := fs.firstSymlink(currentPath)
		if err != nil {
			return nil, currentPath, err
		}
		if link == nil {
			node, statErr := fs.Stat(currentPath)
			return node, currentPath, statErr
		}

		location := AnalyzeLinkTarget(linkPath, rawTarget)
		if location.EscapesRoot {
			return nil, location.Resolved, ErrPathEscape
		}
		if location.Resolved == "" {
			return nil, currentPath, ErrInvalidPath
		}
		currentPath = location.Resolved
		if remaining != "" {
			currentPath = path.Join(currentPath, remaining)
		}
	}
	return nil, currentPath, errSymlinkLoop
}

func (fs *FileSystem) firstSymlink(name string) (link *Node, linkPath, rawTarget, remaining string, err error) {
	clean, err := CleanPath(name)
	if err != nil {
		return nil, "", "", "", err
	}
	if clean == "." {
		return nil, "", "", "", nil
	}

	current := fs.Root
	segments := splitPath(clean)
	built := ""
	for i, segment := range segments {
		if built == "" {
			built = segment
		} else {
			built = built + "/" + segment
		}
		if current == nil || current.Children == nil {
			return nil, built, "", "", fmt.Errorf("%w: %s", ErrInvalidPath, built)
		}
		child, ok := current.Children[segment]
		if !ok {
			return nil, built, "", "", fmt.Errorf("path not found: %s", built)
		}
		if child.IsSymlink() {
			raw := firstNonEmpty(child.SymlinkTarget, child.Target, child.Linkname)
			rest := ""
			if i+1 < len(segments) {
				rest = path.Join(segments[i+1:]...)
			}
			return child, built, raw, rest, nil
		}
		if !child.IsDir() && i != len(segments)-1 {
			return nil, built, "", "", fmt.Errorf("%w: %s is not a directory", ErrPathConflict, built)
		}
		current = child
	}
	return nil, "", "", "", nil
}

func splitPath(p string) []string {
	var out []string
	for _, segment := range strings.Split(p, "/") {
		if segment == "" || segment == "." {
			continue
		}
		out = append(out, segment)
	}
	return out
}
