package netsec

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

// Digest is a SHA-256 digest used for every object in the source closure.
type Digest [sha256.Size]byte

// Quote-style domain separators. They are part of the on-disk/on-wire format
// and must not be changed without changing the format version.
const (
	BlobTag   = "gsb-source-closure/v1/blob\x00"
	TreeTag   = "gsb-source-closure/v1/tree\x00"
	CommitTag = "gsb-source-closure/v1/commit\x00"
)

// ZeroDigest is the all-zero digest, which is never a valid object key.
var ZeroDigest Digest

func (d Digest) String() string {
	return hex.EncodeToString(d[:])
}

// Hex is an explicit spelling used by callers that want to avoid depending on
// the String method's formatting.
func (d Digest) Hex() string {
	return d.String()
}

func (d Digest) IsZero() bool {
	return d == ZeroDigest
}

// DigestFromBytes copies exactly 32 bytes into a Digest.
func DigestFromBytes(b []byte) (Digest, error) {
	var d Digest
	if len(b) != len(d) {
		return d, fmt.Errorf("%w: digest length %d", ErrMalformedObject, len(b))
	}
	copy(d[:], b)
	return d, nil
}

// ParseDigest accepts the 64-character hexadecimal representation produced by
// Digest.String.
func ParseDigest(s string) (Digest, error) {
	var d Digest
	b, err := hex.DecodeString(strings.TrimSpace(s))
	if err != nil {
		return d, fmt.Errorf("%w: %v", ErrMalformedObject, err)
	}
	if len(b) != len(d) {
		return d, fmt.Errorf("%w: digest length %d", ErrMalformedObject, len(b))
	}
	copy(d[:], b)
	return d, nil
}

// Kind identifies an object or a tree entry. A gitlink is a tree-entry kind,
// not an object stored in the current repository: its digest names a commit in
// the child repository.
type Kind uint8

const (
	KindInvalid Kind = iota
	KindBlob
	KindTree
	KindCommit
	KindGitlink
)

func (k Kind) String() string {
	switch k {
	case KindBlob:
		return "blob"
	case KindTree:
		return "tree"
	case KindCommit:
		return "commit"
	case KindGitlink:
		return "gitlink"
	default:
		return "invalid"
	}
}

// Mode is the canonical octal mode stored in a tree entry. The mode also
// determines the entry kind.
type Mode string

const (
	ModeRegular    Mode = "100644"
	ModeExecutable Mode = "100755"
	ModeSymlink    Mode = "120000"
	ModeTree       Mode = "40000"
	ModeGitlink    Mode = "160000"
)

func canonicalMode(m Mode) (Mode, Kind, error) {
	switch string(m) {
	case string(ModeRegular):
		return ModeRegular, KindBlob, nil
	case string(ModeExecutable):
		return ModeExecutable, KindBlob, nil
	case string(ModeSymlink):
		return ModeSymlink, KindBlob, nil
	case string(ModeTree), "040000", "0040000":
		return ModeTree, KindTree, nil
	case string(ModeGitlink):
		return ModeGitlink, KindGitlink, nil
	default:
		return "", KindInvalid, fmt.Errorf("%w: unsupported mode %q", ErrInvalidObject, string(m))
	}
}

// EntryKind returns the kind implied by the mode.
func (m Mode) EntryKind() (Kind, error) {
	_, kind, err := canonicalMode(m)
	return kind, err
}

func (m Mode) IsSymlink() bool {
	canonical, _, err := canonicalMode(m)
	return err == nil && canonical == ModeSymlink
}

// Object is an immutable payload in the host object store. Data is the raw
// blob content for KindBlob, the canonical tree body for KindTree, and the
// canonical commit body for KindCommit.
type Object struct {
	Type Kind
	Data []byte
}

// Clone returns an independent copy of the object data.
func (o Object) Clone() Object {
	if o.Data == nil {
		return Object{Type: o.Type}
	}
	return Object{Type: o.Type, Data: append([]byte(nil), o.Data...)}
}

func hashDomain(tag string, data []byte) Digest {
	h := sha256.New()
	h.Write([]byte(tag))
	h.Write(data)
	var d Digest
	copy(d[:], h.Sum(nil))
	return d
}

func putUint64(buf *bytes.Buffer, n uint64) {
	var tmp [8]byte
	binary.BigEndian.PutUint64(tmp[:], n)
	buf.Write(tmp[:])
}

// HashBlob computes SHA256(blob tag || uint64be(length) || content).
func HashBlob(content []byte) Digest {
	h := sha256.New()
	h.Write([]byte(BlobTag))
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(content)))
	h.Write(length[:])
	h.Write(content)
	var d Digest
	copy(d[:], h.Sum(nil))
	return d
}

// TreeEntry is a single entry in a canonical tree. Kind may be left zero, in
// which case it is inferred from Mode; when non-zero it must agree with Mode.
type TreeEntry struct {
	Name   string
	Mode   Mode
	Kind   Kind
	Digest Digest
}

func validateComponent(name string) error {
	switch {
	case name == "":
		return fmt.Errorf("%w: empty path component", ErrInvalidPath)
	case name == "." || name == "..":
		return fmt.Errorf("%w: reserved path component %q", ErrInvalidPath, name)
	case strings.IndexByte(name, 0) >= 0:
		return fmt.Errorf("%w: NUL in path component", ErrInvalidPath)
	case strings.ContainsRune(name, '/'):
		return fmt.Errorf("%w: slash in path component %q", ErrInvalidPath, name)
	}
	return nil
}

func normalizeTreeEntries(entries []TreeEntry) ([]TreeEntry, error) {
	out := make([]TreeEntry, len(entries))
	copy(out, entries)
	for i := range out {
		canonical, implied, err := canonicalMode(out[i].Mode)
		if err != nil {
			return nil, err
		}
		if out[i].Kind != KindInvalid && out[i].Kind != implied {
			return nil, fmt.Errorf("%w: mode %q is %s, entry says %s", ErrInvalidObject, out[i].Mode, implied, out[i].Kind)
		}
		if err := validateComponent(out[i].Name); err != nil {
			return nil, err
		}
		if out[i].Digest.IsZero() {
			return nil, fmt.Errorf("%w: zero digest for %q", ErrInvalidObject, out[i].Name)
		}
		out[i].Mode = canonical
		out[i].Kind = implied
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})
	for i := 1; i < len(out); i++ {
		if out[i-1].Name == out[i].Name {
			return nil, fmt.Errorf("%w: name %q", ErrDuplicateEntry, out[i].Name)
		}
	}
	return out, nil
}

func encodeTreeEntries(entries []TreeEntry) ([]byte, error) {
	normalized, err := normalizeTreeEntries(entries)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	for _, entry := range normalized {
		buf.WriteString(string(entry.Mode))
		buf.WriteByte(0)
		putUint64(&buf, uint64(len(entry.Name)))
		buf.WriteString(entry.Name)
		buf.Write(entry.Digest[:])
	}
	return buf.Bytes(), nil
}

// HashTree computes the canonical tree digest for entries. Entries are sorted
// by the raw bytes of Name, and duplicate names are rejected.
func HashTree(entries []TreeEntry) (Digest, error) {
	body, err := encodeTreeEntries(entries)
	if err != nil {
		return ZeroDigest, err
	}
	return hashDomain(TreeTag, body), nil
}

// NewTree builds a canonical tree object.
func NewTree(entries []TreeEntry) (Object, error) {
	body, err := encodeTreeEntries(entries)
	if err != nil {
		return Object{}, err
	}
	return Object{Type: KindTree, Data: body}, nil
}

func parseTreeBody(body []byte) ([]TreeEntry, error) {
	var entries []TreeEntry
	var previous string
	first := true
	for len(body) > 0 {
		nul := bytes.IndexByte(body, 0)
		if nul < 0 {
			return nil, fmt.Errorf("%w: unterminated tree mode", ErrMalformedObject)
		}
		rawMode := string(body[:nul])
		mode, kind, err := canonicalMode(Mode(rawMode))
		if err != nil {
			return nil, err
		}
		if string(mode) != rawMode {
			return nil, fmt.Errorf("%w: non-canonical tree mode %q", ErrMalformedObject, rawMode)
		}
		body = body[nul+1:]
		if len(body) < 8 {
			return nil, fmt.Errorf("%w: truncated tree name length", ErrMalformedObject)
		}
		nameLen := binary.BigEndian.Uint64(body[:8])
		body = body[8:]
		if nameLen > uint64(len(body)) || nameLen > uint64(^uint(0)>>1) {
			return nil, fmt.Errorf("%w: tree name length out of range", ErrMalformedObject)
		}
		if uint64(len(body)) < nameLen+uint64(len(Digest{})) {
			return nil, fmt.Errorf("%w: truncated tree entry", ErrMalformedObject)
		}
		name := string(body[:nameLen])
		body = body[nameLen:]
		if err := validateComponent(name); err != nil {
			return nil, err
		}
		if !first && name <= previous {
			if name == previous {
				return nil, fmt.Errorf("%w: duplicate name %q", ErrDuplicateEntry, name)
			}
			return nil, fmt.Errorf("%w: tree entries are not in byte order", ErrMalformedObject)
		}
		first = false
		previous = name
		var digest Digest
		copy(digest[:], body[:len(digest)])
		body = body[len(digest):]
		if digest.IsZero() {
			return nil, fmt.Errorf("%w: zero digest for %q", ErrInvalidObject, name)
		}
		entries = append(entries, TreeEntry{
			Name:   name,
			Mode:   mode,
			Kind:   kind,
			Digest: digest,
		})
	}
	return entries, nil
}

// ParseTree validates and decodes a canonical tree object.
func ParseTree(object Object) ([]TreeEntry, error) {
	if object.Type != KindTree {
		return nil, fmt.Errorf("%w: expected tree, got %s", ErrInvalidObject, object.Type)
	}
	return parseTreeBody(object.Data)
}

// Commit is the canonical commit payload: one tree digest and an ordered list
// of parent commit digests.
type Commit struct {
	Tree    Digest
	Parents []Digest
}

// NewCommit builds a canonical commit object.
func NewCommit(tree Digest, parents []Digest) Object {
	var buf bytes.Buffer
	buf.Write(tree[:])
	putUint64(&buf, uint64(len(parents)))
	for _, parent := range parents {
		buf.Write(parent[:])
	}
	return Object{Type: KindCommit, Data: buf.Bytes()}
}

// ParseCommit validates and decodes a canonical commit object.
func ParseCommit(object Object) (Commit, error) {
	var commit Commit
	if object.Type != KindCommit {
		return commit, fmt.Errorf("%w: expected commit, got %s", ErrInvalidObject, object.Type)
	}
	if len(object.Data) < len(Digest{})+8 {
		return commit, fmt.Errorf("%w: truncated commit", ErrMalformedObject)
	}
	copy(commit.Tree[:], object.Data[:len(commit.Tree)])
	if commit.Tree.IsZero() {
		return Commit{}, fmt.Errorf("%w: zero tree digest", ErrInvalidObject)
	}
	count := binary.BigEndian.Uint64(object.Data[len(commit.Tree) : len(commit.Tree)+8])
	rest := object.Data[len(commit.Tree)+8:]
	if count > uint64(len(rest))/uint64(len(Digest{})) || count > uint64(^uint(0)>>1) {
		return Commit{}, fmt.Errorf("%w: parent count out of range", ErrMalformedObject)
	}
	if uint64(len(rest)) != count*uint64(len(Digest{})) {
		return Commit{}, fmt.Errorf("%w: trailing commit data", ErrMalformedObject)
	}
	commit.Parents = make([]Digest, 0, count)
	for i := uint64(0); i < count; i++ {
		var parent Digest
		start := i * uint64(len(parent))
		copy(parent[:], rest[start:start+uint64(len(parent))])
		if parent.IsZero() {
			return Commit{}, fmt.Errorf("%w: zero parent digest", ErrInvalidObject)
		}
		commit.Parents = append(commit.Parents, parent)
	}
	return commit, nil
}

// ObjectDigest recomputes the content address of an object.
func ObjectDigest(object Object) (Digest, error) {
	switch object.Type {
	case KindBlob:
		return HashBlob(object.Data), nil
	case KindTree:
		return hashDomain(TreeTag, object.Data), nil
	case KindCommit:
		return hashDomain(CommitTag, object.Data), nil
	default:
		return ZeroDigest, fmt.Errorf("%w: cannot hash %s", ErrInvalidObject, object.Type)
	}
}
