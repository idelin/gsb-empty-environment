package netsec

import (
	"crypto/subtle"
	"encoding/binary"
	"fmt"
	"sort"
)

const (
	nodeHeaderSize = 128
	maxNodeEntries = 300

	nodeLeaf     = 1
	nodeInternal = 2
)

type internalEntry struct {
	Max uint64
	Ref physicalRef
}

type mapNode struct {
	Kind     uint8
	Level    uint16
	Leaf     []mappingEntry
	Internal []internalEntry
}

type treeSplit struct {
	Right physicalRef
	Max   uint64
}

func (v *Volume) loadNode(ref physicalRef) (*mapNode, error) {
	if ref.Sector < DataStartSector || ref.Sector >= v.sectorCount || ref.Generation == 0 {
		return nil, fmt.Errorf("%w: node reference %d/%d", ErrCorrupt, ref.Sector, ref.Generation)
	}
	record := make([]byte, PhysicalSectorSize)
	if err := v.device.ReadSector(ref.Sector, record); err != nil {
		return nil, err
	}
	return v.decodeNode(record, ref)
}

func (v *Volume) decodeNode(record []byte, ref physicalRef) (*mapNode, error) {
	if len(record) != PhysicalSectorSize {
		return nil, fmt.Errorf("%w: node has %d bytes", ErrInvalidSize, len(record))
	}
	if subtle.ConstantTimeCompare(record[0:8], nodeMagic[:]) != 1 {
		return nil, ErrAuthentication
	}
	if binary.BigEndian.Uint16(record[8:10]) != formatVersion {
		return nil, fmt.Errorf("%w: unsupported node version", ErrCorrupt)
	}
	if !verifyMetadata(v.metaKey, record) {
		return nil, ErrAuthentication
	}

	kind := record[10]
	level := binary.BigEndian.Uint16(record[12:14])
	count := int(binary.BigEndian.Uint32(record[16:20]))
	nodeVolume := binary.BigEndian.Uint64(record[20:28])
	physical := binary.BigEndian.Uint64(record[28:36])
	generation := binary.BigEndian.Uint64(record[36:44])
	if nodeVolume != v.volumeID {
		return nil, ErrAuthentication
	}
	if physical != ref.Sector || generation != ref.Generation {
		return nil, ErrPhysicalGeneration
	}
	if count < 0 || count > maxNodeEntries {
		return nil, fmt.Errorf("%w: node entry count %d", ErrCorrupt, count)
	}
	if kind != nodeLeaf && kind != nodeInternal {
		return nil, fmt.Errorf("%w: node kind %d", ErrCorrupt, kind)
	}
	if kind == nodeLeaf && level != 1 {
		return nil, fmt.Errorf("%w: leaf level %d", ErrCorrupt, level)
	}
	if kind == nodeInternal && level < 2 {
		return nil, fmt.Errorf("%w: internal level %d", ErrCorrupt, level)
	}

	node := &mapNode{Kind: kind, Level: level}
	off := nodeHeaderSize
	switch kind {
	case nodeLeaf:
		if count == 0 {
			return nil, fmt.Errorf("%w: empty leaf", ErrCorrupt)
		}
		node.Leaf = make([]mappingEntry, count)
		for i := 0; i < count; i++ {
			entry := mappingEntry{
				Logical:            binary.BigEndian.Uint64(record[off : off+8]),
				Physical:           binary.BigEndian.Uint64(record[off+8 : off+16]),
				PhysicalGeneration: binary.BigEndian.Uint64(record[off+16 : off+24]),
			}
			off += 24
			if entry.Physical < DataStartSector || entry.Physical >= v.sectorCount || entry.PhysicalGeneration == 0 {
				return nil, fmt.Errorf("%w: invalid mapping", ErrCorrupt)
			}
			if i > 0 && node.Leaf[i-1].Logical >= entry.Logical {
				return nil, fmt.Errorf("%w: unordered leaf", ErrCorrupt)
			}
			node.Leaf[i] = entry
		}
	case nodeInternal:
		if count < 2 {
			return nil, fmt.Errorf("%w: internal node has %d children", ErrCorrupt, count)
		}
		node.Internal = make([]internalEntry, count)
		for i := 0; i < count; i++ {
			entry := internalEntry{
				Max: binary.BigEndian.Uint64(record[off : off+8]),
				Ref: physicalRef{
					Sector:     binary.BigEndian.Uint64(record[off+8 : off+16]),
					Generation: binary.BigEndian.Uint64(record[off+16 : off+24]),
				},
			}
			off += 24
			if entry.Ref.Sector < DataStartSector || entry.Ref.Sector >= v.sectorCount || entry.Ref.Generation == 0 {
				return nil, fmt.Errorf("%w: invalid child reference", ErrCorrupt)
			}
			if i > 0 && node.Internal[i-1].Max >= entry.Max {
				return nil, fmt.Errorf("%w: unordered internal node", ErrCorrupt)
			}
			node.Internal[i] = entry
		}
	}
	return node, nil
}

func (v *Volume) persistNode(tx *writeTx, node *mapNode, writeEpoch uint64) (physicalRef, error) {
	count := len(node.Leaf)
	if node.Kind == nodeInternal {
		count = len(node.Internal)
	}
	if count == 0 || count > maxNodeEntries {
		return physicalRef{}, fmt.Errorf("%w: persist node count %d", ErrCorrupt, count)
	}

	ref, err := v.allocPhysical(tx, writeEpoch)
	if err != nil {
		return physicalRef{}, err
	}
	record := make([]byte, PhysicalSectorSize)
	copy(record[0:8], nodeMagic[:])
	binary.BigEndian.PutUint16(record[8:10], formatVersion)
	record[10] = node.Kind
	binary.BigEndian.PutUint16(record[12:14], node.Level)
	binary.BigEndian.PutUint32(record[16:20], uint32(count))
	binary.BigEndian.PutUint64(record[20:28], v.volumeID)
	binary.BigEndian.PutUint64(record[28:36], ref.Sector)
	binary.BigEndian.PutUint64(record[36:44], ref.Generation)
	binary.BigEndian.PutUint64(record[44:52], writeEpoch)

	off := nodeHeaderSize
	switch node.Kind {
	case nodeLeaf:
		for _, entry := range node.Leaf {
			binary.BigEndian.PutUint64(record[off:off+8], entry.Logical)
			binary.BigEndian.PutUint64(record[off+8:off+16], entry.Physical)
			binary.BigEndian.PutUint64(record[off+16:off+24], entry.PhysicalGeneration)
			off += 24
		}
	case nodeInternal:
		for _, entry := range node.Internal {
			binary.BigEndian.PutUint64(record[off:off+8], entry.Max)
			binary.BigEndian.PutUint64(record[off+8:off+16], entry.Ref.Sector)
			binary.BigEndian.PutUint64(record[off+16:off+24], entry.Ref.Generation)
			off += 24
		}
	default:
		return physicalRef{}, fmt.Errorf("%w: node kind %d", ErrCorrupt, node.Kind)
	}
	authenticateMetadata(v.metaKey, record)
	if err := v.writePhysical(ref, record); err != nil {
		return physicalRef{}, err
	}
	return ref, nil
}

func (v *Volume) insertIntoTree(
	tx *writeTx,
	oldRef physicalRef,
	oldLevel uint16,
	logical uint64,
	physical uint64,
	physicalGeneration uint64,
	writeEpoch uint64,
) (physicalRef, uint16, uint64, *treeSplit, error) {
	if oldRef.Sector == 0 {
		node := &mapNode{
			Kind:  nodeLeaf,
			Level: 1,
			Leaf: []mappingEntry{{
				Logical:            logical,
				Physical:           physical,
				PhysicalGeneration: physicalGeneration,
			}},
		}
		ref, err := v.persistNode(tx, node, writeEpoch)
		if err != nil {
			return physicalRef{}, 0, 0, nil, err
		}
		return ref, 1, logical, nil, nil
	}

	node, err := v.loadNode(oldRef)
	if err != nil {
		return physicalRef{}, 0, 0, nil, err
	}
	if node.Level != oldLevel {
		return physicalRef{}, 0, 0, nil, fmt.Errorf("%w: expected level %d, got %d", ErrCorrupt, oldLevel, node.Level)
	}

	if node.Kind == nodeLeaf {
		entries := append([]mappingEntry(nil), node.Leaf...)
		index := sort.Search(len(entries), func(i int) bool { return entries[i].Logical >= logical })
		entry := mappingEntry{
			Logical:            logical,
			Physical:           physical,
			PhysicalGeneration: physicalGeneration,
		}
		if index < len(entries) && entries[index].Logical == logical {
			entries[index] = entry
		} else {
			entries = append(entries, mappingEntry{})
			copy(entries[index+1:], entries[index:])
			entries[index] = entry
		}
		if len(entries) <= maxNodeEntries {
			left := &mapNode{Kind: nodeLeaf, Level: 1, Leaf: entries}
			ref, err := v.persistNode(tx, left, writeEpoch)
			if err != nil {
				return physicalRef{}, 0, 0, nil, err
			}
			return ref, 1, entries[len(entries)-1].Logical, nil, nil
		}
		mid := len(entries) / 2
		left := &mapNode{Kind: nodeLeaf, Level: 1, Leaf: append([]mappingEntry(nil), entries[:mid]...)}
		right := &mapNode{Kind: nodeLeaf, Level: 1, Leaf: append([]mappingEntry(nil), entries[mid:]...)}
		leftRef, err := v.persistNode(tx, left, writeEpoch)
		if err != nil {
			return physicalRef{}, 0, 0, nil, err
		}
		rightRef, err := v.persistNode(tx, right, writeEpoch)
		if err != nil {
			return physicalRef{}, 0, 0, nil, err
		}
		return leftRef, 1, left.Leaf[len(left.Leaf)-1].Logical, &treeSplit{
			Right: rightRef,
			Max:   right.Leaf[len(right.Leaf)-1].Logical,
		}, nil
	}

	children := append([]internalEntry(nil), node.Internal...)
	childIndex := sort.Search(len(children), func(i int) bool { return children[i].Max >= logical })
	if childIndex == len(children) {
		childIndex = len(children) - 1
	}
	child := children[childIndex]
	newChildRef, childLevel, newChildMax, split, err := v.insertIntoTree(
		tx,
		child.Ref,
		node.Level-1,
		logical,
		physical,
		physicalGeneration,
		writeEpoch,
	)
	if err != nil {
		return physicalRef{}, 0, 0, nil, err
	}
	if childLevel != node.Level-1 {
		return physicalRef{}, 0, 0, nil, fmt.Errorf("%w: child level changed", ErrCorrupt)
	}
	children[childIndex].Ref = newChildRef
	children[childIndex].Max = newChildMax
	if split != nil {
		children = append(children, internalEntry{})
		copy(children[childIndex+2:], children[childIndex+1:])
		children[childIndex+1] = internalEntry{Max: split.Max, Ref: split.Right}
	}
	if len(children) <= maxNodeEntries {
		updated := &mapNode{Kind: nodeInternal, Level: node.Level, Internal: children}
		ref, err := v.persistNode(tx, updated, writeEpoch)
		if err != nil {
			return physicalRef{}, 0, 0, nil, err
		}
		return ref, node.Level, children[len(children)-1].Max, nil, nil
	}

	mid := len(children) / 2
	left := &mapNode{Kind: nodeInternal, Level: node.Level, Internal: append([]internalEntry(nil), children[:mid]...)}
	right := &mapNode{Kind: nodeInternal, Level: node.Level, Internal: append([]internalEntry(nil), children[mid:]...)}
	leftRef, err := v.persistNode(tx, left, writeEpoch)
	if err != nil {
		return physicalRef{}, 0, 0, nil, err
	}
	rightRef, err := v.persistNode(tx, right, writeEpoch)
	if err != nil {
		return physicalRef{}, 0, 0, nil, err
	}
	return leftRef, node.Level, left.Internal[len(left.Internal)-1].Max, &treeSplit{
		Right: rightRef,
		Max:   right.Internal[len(right.Internal)-1].Max,
	}, nil
}

func (v *Volume) finalizeRoot(
	tx *writeTx,
	ref physicalRef,
	level uint16,
	leftMax uint64,
	split *treeSplit,
	writeEpoch uint64,
) (physicalRef, uint16, error) {
	if split == nil {
		return ref, level, nil
	}
	root := &mapNode{
		Kind:  nodeInternal,
		Level: level + 1,
		Internal: []internalEntry{
			{Max: leftMax, Ref: ref},
			{Max: split.Max, Ref: split.Right},
		},
	}
	rootRef, err := v.persistNode(tx, root, writeEpoch)
	if err != nil {
		return physicalRef{}, 0, err
	}
	return rootRef, level + 1, nil
}

func (v *Volume) lookupInTree(ref physicalRef, level uint16, logical uint64) (mappingEntry, bool, error) {
	for {
		if ref.Sector == 0 {
			return mappingEntry{}, false, nil
		}
		node, err := v.loadNode(ref)
		if err != nil {
			return mappingEntry{}, false, err
		}
		if node.Level != level {
			return mappingEntry{}, false, fmt.Errorf("%w: lookup level mismatch", ErrCorrupt)
		}
		if node.Kind == nodeLeaf {
			index := sort.Search(len(node.Leaf), func(i int) bool { return node.Leaf[i].Logical >= logical })
			if index == len(node.Leaf) || node.Leaf[index].Logical != logical {
				return mappingEntry{}, false, nil
			}
			return node.Leaf[index], true, nil
		}
		index := sort.Search(len(node.Internal), func(i int) bool { return node.Internal[i].Max >= logical })
		if index == len(node.Internal) {
			return mappingEntry{}, false, nil
		}
		ref = node.Internal[index].Ref
		level--
	}
}

func (v *Volume) collectReferenced(ref physicalRef, level uint16) (map[uint64]struct{}, error) {
	used := make(map[uint64]struct{})
	visited := make(map[physicalRef]struct{})
	if ref.Sector == 0 {
		return used, nil
	}
	if err := v.collectNode(ref, level, used, visited); err != nil {
		return nil, err
	}
	return used, nil
}

func (v *Volume) collectNode(
	ref physicalRef,
	level uint16,
	used map[uint64]struct{},
	visited map[physicalRef]struct{},
) error {
	if _, ok := visited[ref]; ok {
		return fmt.Errorf("%w: mapping tree cycle", ErrCorrupt)
	}
	visited[ref] = struct{}{}
	if _, exists := used[ref.Sector]; exists {
		return fmt.Errorf("%w: physical sector referenced twice", ErrCorrupt)
	}
	used[ref.Sector] = struct{}{}

	node, err := v.loadNode(ref)
	if err != nil {
		return err
	}
	if node.Level != level {
		return fmt.Errorf("%w: tree level mismatch", ErrCorrupt)
	}
	if node.Kind == nodeLeaf {
		for i, entry := range node.Leaf {
			if entry.Physical < DataStartSector || entry.Physical >= v.sectorCount || entry.PhysicalGeneration == 0 {
				return fmt.Errorf("%w: invalid physical mapping", ErrCorrupt)
			}
			if i > 0 && node.Leaf[i-1].Logical >= entry.Logical {
				return fmt.Errorf("%w: unordered leaf during traversal", ErrCorrupt)
			}
			if _, exists := used[entry.Physical]; exists {
				return fmt.Errorf("%w: physical sector mapped more than once", ErrCorrupt)
			}
			used[entry.Physical] = struct{}{}
		}
		return nil
	}
	for _, child := range node.Internal {
		if err := v.collectNode(child.Ref, level-1, used, visited); err != nil {
			return err
		}
	}
	return nil
}
