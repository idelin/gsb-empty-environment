package oci

func cloneXattrs(in map[string][]byte) map[string][]byte {
	if in == nil {
		return nil
	}
	out := make(map[string][]byte, len(in))
	for key, value := range in {
		out[key] = append([]byte(nil), value...)
	}
	return out
}

func cloneCapability(in *CapabilitySet) *CapabilitySet {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}

// cloneShallow copies node metadata but detaches child pointers. Residue uses
// this because each removed path is recorded independently.
func (n *Node) cloneShallow() *Node {
	if n == nil {
		return nil
	}
	out := *n
	out.Children = nil
	out.Xattrs = cloneXattrs(n.Xattrs)
	out.Capability = cloneCapability(n.Capability)
	out.Content = append([]byte(nil), n.Content...)
	return &out
}

func cloneNodeDeep(n *Node) *Node {
	if n == nil {
		return nil
	}
	out := n.cloneShallow()
	if n.Children != nil {
		out.Children = make(map[string]*Node, len(n.Children))
		for name, child := range n.Children {
			out.Children[name] = cloneNodeDeep(child)
		}
	}
	return out
}

// Clone returns a deep copy that can safely be retained as an intermediate
// snapshot while the merger continues mutating the active file system.
func (fs *FileSystem) Clone() *FileSystem {
	if fs == nil {
		return nil
	}
	out := &FileSystem{
		Root:          cloneNodeDeep(fs.Root),
		Nodes:         make(map[string]*Node),
		Files:         make(map[string]*Node),
		Inodes:        make(map[uint64]*Inode),
		Residue:       append([]Residue(nil), fs.Residue...),
		Whiteouts:     append([]Whiteout(nil), fs.Whiteouts...),
		Links:         append([]LinkRecord(nil), fs.Links...),
		LayerCount:    fs.LayerCount,
		SnapshotIndex: fs.SnapshotIndex,
		nextInodeID:   fs.nextInodeID,
	}
	for i := range out.Residue {
		out.Residue[i].Node = out.Residue[i].Node.cloneShallow()
	}
	for id, inode := range fs.Inodes {
		copied := *inode
		copied.Xattrs = cloneXattrs(inode.Xattrs)
		copied.Capability = cloneCapability(inode.Capability)
		copied.Content = append([]byte(nil), inode.Content...)
		if inode.Paths != nil {
			copied.Paths = make(map[string]struct{}, len(inode.Paths))
			for path := range inode.Paths {
				copied.Paths[path] = struct{}{}
			}
		}
		out.Inodes[id] = &copied
	}
	_ = out.reindex()
	out.Files = out.Nodes
	return out
}
