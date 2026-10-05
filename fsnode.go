package packer

import (
	"os"
	"path/filepath"
	"sort"
)

// FSNode adapts a real filesystem path to the Node interface. It always
// uses Lstat, so symlinks are seen as links and never followed.
type FSNode struct {
	Path string
	Info os.FileInfo
}

// NewFSNode returns a node for path; info must come from os.Lstat(path).
func NewFSNode(path string, info os.FileInfo) *FSNode {
	return &FSNode{Path: path, Info: info}
}

func (n *FSNode) Name() string { return n.Info.Name() }

func (n *FSNode) IsDir() bool { return n.Info.IsDir() }

func (n *FSNode) IsSymlink() bool { return n.Info.Mode()&os.ModeSymlink != 0 }

func (n *FSNode) LinkTarget() string {
	t, err := os.Readlink(n.Path)
	if err != nil {
		return ""
	}
	return t
}

func (n *FSNode) Children() ([]Node, error) {
	f, err := os.Open(n.Path)
	if err != nil {
		return nil, err
	}
	names, err := f.Readdirnames(-1)
	f.Close()
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	var out []Node
	for _, name := range names {
		full := filepath.Join(n.Path, name)
		info, err := os.Lstat(full)
		if err != nil {
			if os.IsNotExist(err) {
				continue // vanished between readdir and lstat
			}
			return nil, err
		}
		out = append(out, &FSNode{Path: full, Info: info})
	}
	return out, nil
}
