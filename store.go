// Package triestore implements an immutable, path-copying byte-prefix trie
// persisted to disk. Nodes are content-addressed (SHA-256) and CRC-checked,
// roots are committed as atomic snapshots, and historical roots stay readable.
package triestore

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Faults groups optional fault-injection hooks. A nil *Faults disables all
// injection. Hooks let tests simulate crashes and torn writes at precise
// points; production code passes nil.
type Faults struct {
	// BeforeNodeWrite fires before a node file is written.
	BeforeNodeWrite func(id string) error
	// AfterNodeWrite fires after the node file is renamed into place but
	// before the write path continues (nodes may already be durable here
	// while the root is not yet committed — the classic crash window).
	AfterNodeWrite func(id string) error
	// CorruptNodeData may transform node bytes before they hit disk,
	// simulating media corruption.
	CorruptNodeData func(id string, data []byte) []byte
	// BeforeRootRename fires after the root temp file is written (and
	// synced, if Sync is on) but before the atomic rename.
	BeforeRootRename func(seq uint64) error
	// AfterRootCommit fires after the root rename + directory sync.
	AfterRootCommit func(seq uint64) error
}

// Options configures a Store.
type Options struct {
	// Sync enables fsync of node files, root files and directories, giving
	// power-loss durability for committed roots. With Sync=false the store
	// only survives process crashes, not OS/machine power loss.
	Sync bool
	// Faults are fault-injection hooks; nil in production.
	Faults *Faults
}

// Root identifies one committed snapshot.
type Root struct {
	Seq uint64
	ID  string // emptyID for the empty tree
}

// Store is a persistent immutable trie rooted at a directory.
type Store struct {
	dir      string
	nodesDir string
	rootsDir string
	sync     bool
	faults   *Faults

	root string // current root node ID ("" = empty tree)
	seq  uint64 // sequence of the last committed root
}

// Open creates (if needed) the directory layout under dir and recovers the
// newest valid committed root. Torn or invalid root files left by a crash are
// skipped; uncommitted node files become harmless orphans.
func Open(dir string, opts *Options) (*Store, error) {
	if opts == nil {
		opts = &Options{}
	}
	s := &Store{
		dir:      dir,
		nodesDir: filepath.Join(dir, "nodes"),
		rootsDir: filepath.Join(dir, "roots"),
		sync:     opts.Sync,
		faults:   opts.Faults,
		root:     emptyID,
	}
	for _, d := range []string{s.nodesDir, s.rootsDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, err
		}
	}
	if err := s.recover(); err != nil {
		return nil, err
	}
	return s, nil
}

// recover scans roots/ and adopts the highest-sequence root file that parses
// and passes its CRC. Lower valid roots remain available as history.
func (s *Store) recover() error {
	roots, err := s.listRoots()
	if err != nil {
		return err
	}
	if len(roots) == 0 {
		return nil
	}
	last := roots[len(roots)-1]
	s.root, s.seq = last.ID, last.Seq
	return nil
}

// RootID returns the current root node ID ("" for the empty tree).
func (s *Store) RootID() string { return s.root }

// Seq returns the sequence number of the current committed root.
func (s *Store) Seq() uint64 { return s.seq }

// Roots lists all committed, valid root snapshots in order.
func (s *Store) Roots() ([]Root, error) { return s.listRoots() }

func (s *Store) listRoots() ([]Root, error) {
	ents, err := os.ReadDir(s.rootsDir)
	if err != nil {
		return nil, err
	}
	var roots []Root
	for _, e := range ents {
		name := e.Name()
		if !strings.HasSuffix(name, ".root") {
			continue // ignore *.tmp and anything else
		}
		seq, err := strconv.ParseUint(strings.TrimSuffix(name, ".root"), 10, 64)
		if err != nil {
			continue
		}
		id, err := s.readRootFile(filepath.Join(s.rootsDir, name))
		if err != nil {
			continue // torn/corrupt root from a crash: skip it
		}
		roots = append(roots, Root{Seq: seq, ID: id})
	}
	sort.Slice(roots, func(i, j int) bool { return roots[i].Seq < roots[j].Seq })
	return roots, nil
}

// nodePath maps a node ID to its file.
func (s *Store) nodePath(id string) string {
	return filepath.Join(s.nodesDir, id+".node")
}

// loadNode reads and fully verifies a node: CRC first, then the payload hash
// against the referencing ID. Any mismatch is reported with the node ID so a
// corrupt reference can be located.
func (s *Store) loadNode(id string) (*node, error) {
	data, err := os.ReadFile(s.nodePath(id))
	if err != nil {
		return nil, fmt.Errorf("triestore: load node %s: %w", id, err)
	}
	n, err := decodeNode(data)
	if err != nil {
		return nil, fmt.Errorf("triestore: corrupt node %s: %w", id, err)
	}
	if got := payloadID(data[:len(data)-4]); got != id {
		return nil, fmt.Errorf("triestore: corrupt node %s: content hashes to %s", id, got)
	}
	return n, nil
}

// writeNode persists n and returns its ID. Identical content is never
// rewritten (content addressing deduplicates shared subtrees).
func (s *Store) writeNode(n *node) (string, error) {
	data := n.encode()
	id := payloadID(data[:len(data)-4])
	if s.faults != nil && s.faults.BeforeNodeWrite != nil {
		if err := s.faults.BeforeNodeWrite(id); err != nil {
			return "", err
		}
	}
	if _, err := os.Stat(s.nodePath(id)); err == nil {
		return id, nil // already on disk, content-addressed dedup
	}
	if s.faults != nil && s.faults.CorruptNodeData != nil {
		data = s.faults.CorruptNodeData(id, data)
	}
	tmp := s.nodePath(id) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return "", err
	}
	if s.sync {
		if err := syncFile(tmp); err != nil {
			return "", err
		}
	}
	if err := os.Rename(tmp, s.nodePath(id)); err != nil {
		return "", err
	}
	if s.sync {
		if err := syncDir(s.nodesDir); err != nil {
			return "", err
		}
	}
	if s.faults != nil && s.faults.AfterNodeWrite != nil {
		if err := s.faults.AfterNodeWrite(id); err != nil {
			return "", err
		}
	}
	return id, nil
}

// Commit publishes the current in-memory root as the next snapshot. The root
// file is written to a temp file and atomically renamed; with Sync enabled
// the file and directory are fsynced so committed roots survive power loss.
func (s *Store) Commit() (Root, error) {
	seq := s.seq + 1
	data := encodeRoot(s.root)
	final := filepath.Join(s.rootsDir, fmt.Sprintf("%06d.root", seq))
	tmp := final + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return Root{}, err
	}
	if s.sync {
		if err := syncFile(tmp); err != nil {
			return Root{}, err
		}
	}
	if s.faults != nil && s.faults.BeforeRootRename != nil {
		if err := s.faults.BeforeRootRename(seq); err != nil {
			return Root{}, err
		}
	}
	if err := os.Rename(tmp, final); err != nil {
		return Root{}, err
	}
	if s.sync {
		if err := syncDir(s.rootsDir); err != nil {
			return Root{}, err
		}
	}
	s.seq = seq
	if s.faults != nil && s.faults.AfterRootCommit != nil {
		if err := s.faults.AfterRootCommit(seq); err != nil {
			return Root{}, err
		}
	}
	return Root{Seq: seq, ID: s.root}, nil
}

// rootFile layout: "TRR1" || 32-byte root hash (all zero = empty) || crc32.
func encodeRoot(id string) []byte {
	buf := make([]byte, 0, 4+hashLen+4)
	buf = append(buf, "TRR1"...)
	if id == emptyID {
		buf = append(buf, make([]byte, hashLen)...)
	} else {
		raw, err := hex.DecodeString(id)
		if err != nil || len(raw) != hashLen {
			panic(fmt.Sprintf("triestore: invalid root id %q", id))
		}
		buf = append(buf, raw...)
	}
	var crcb [4]byte
	binary.LittleEndian.PutUint32(crcb[:], crc32.ChecksumIEEE(buf))
	return append(buf, crcb[:]...)
}

func (s *Store) readRootFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if len(data) != 4+hashLen+4 || string(data[:4]) != "TRR1" {
		return "", errors.New("triestore: bad root file")
	}
	if crc32.ChecksumIEEE(data[:len(data)-4]) != binary.LittleEndian.Uint32(data[len(data)-4:]) {
		return "", errors.New("triestore: root file crc mismatch")
	}
	raw := data[4 : 4+hashLen]
	zero := true
	for _, b := range raw {
		if b != 0 {
			zero = false
			break
		}
	}
	if zero {
		return emptyID, nil
	}
	return hex.EncodeToString(raw), nil
}

func syncFile(path string) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

func syncDir(path string) error {
	d, err := os.Open(path)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
