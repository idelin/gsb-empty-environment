package gittree

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
)

// Mode is a Git tree entry mode. Only the canonical modes produced by Git
// are accepted.
type Mode uint32

const (
	// ModeTree marks a subdirectory (tree object).
	ModeTree Mode = 0o040000
	// ModeRegular marks a non-executable regular file (blob object).
	ModeRegular Mode = 0o100644
	// ModeExecutable marks an executable regular file (blob object).
	ModeExecutable Mode = 0o100755
	// ModeSymlink marks a symbolic link whose target is the blob content.
	ModeSymlink Mode = 0o120000
	// ModeGitlink marks a submodule commit (gitlink); it cannot be traversed.
	ModeGitlink Mode = 0o160000
)

// IsBlob reports whether the mode references a blob object.
func (m Mode) IsBlob() bool {
	switch m {
	case ModeRegular, ModeExecutable, ModeSymlink:
		return true
	default:
		return false
	}
}

// IsTree reports whether the mode references a tree object.
func (m Mode) IsTree() bool { return m == ModeTree }

// IsGitlink reports whether the mode is a submodule gitlink.
func (m Mode) IsGitlink() bool { return m == ModeGitlink }

// IsRegularFile reports whether the mode is a plain regular file, excluding
// symbolic links.
func (m Mode) IsRegularFile() bool { return m == ModeRegular || m == ModeExecutable }

// String renders the mode in Git's canonical octal form (no leading zero).
func (m Mode) String() string { return strconv.FormatUint(uint64(m), 8) }

// Entry is one name-keyed record inside a Git tree.
type Entry struct {
	Mode Mode
	Name string
	ID   ObjectID
}

// ExpectedType returns the object type the entry mode requires.
func (e Entry) ExpectedType() ObjectType {
	if e.Mode.IsTree() {
		return TypeTree
	}
	return TypeBlob
}

// Tree is a fully parsed and validated Git tree object.
type Tree struct {
	// ID is the content digest of the serialized tree.
	ID ObjectID
	// Algo is the hash algorithm of ID.
	Algo Algorithm
	// Entries are stored in Git tree order.
	Entries []Entry
}

var (
	// ErrMalformedTree indicates a tree object cannot be parsed.
	ErrMalformedTree = errors.New("gittree: malformed tree object")
	// ErrDuplicateName indicates two entries share the same entry name.
	ErrDuplicateName = errors.New("gittree: duplicate tree entry name")
	// ErrDirFileConflict indicates a file and a directory share a base name.
	ErrDirFileConflict = errors.New("gittree: directory/file name conflict")
	// ErrInvalidName indicates an entry or path component is not a valid Git name.
	ErrInvalidName = errors.New("gittree: invalid entry name")
	// ErrUnknownMode indicates a tree entry uses an unsupported mode.
	ErrUnknownMode = errors.New("gittree: unknown tree entry mode")
)

// parseMode parses Git's octal mode text and requires it to be canonical:
// the accepted encoding must be exactly the mode's canonical string form, so
// non-standard spellings such as leading zeros are rejected.
func parseMode(text string) (Mode, error) {
	value, err := strconv.ParseUint(text, 8, 32)
	if err != nil {
		return 0, fmt.Errorf("%w: bad octal mode %q", ErrUnknownMode, text)
	}
	mode := Mode(value)
	switch mode {
	case ModeTree, ModeRegular, ModeExecutable, ModeSymlink, ModeGitlink:
	default:
		return 0, fmt.Errorf("%w: %s", ErrUnknownMode, text)
	}
	if mode.String() != text {
		return 0, fmt.Errorf("%w: non-canonical mode spelling %q", ErrUnknownMode, text)
	}
	return mode, nil
}

// ValidateName checks a single path component. Git forbids NUL bytes, slashes
// (they are the path separator), and the reserved "." and ".." components.
func ValidateName(name string) error {
	if name == "" || len(name) > 255 {
		return fmt.Errorf("%w: %q", ErrInvalidName, name)
	}
	if name == "." || name == ".." {
		return fmt.Errorf("%w: reserved name %q", ErrInvalidName, name)
	}
	for i := 0; i < len(name); i++ {
		if name[i] == 0 || name[i] == '/' {
			return fmt.Errorf("%w: %q", ErrInvalidName, name)
		}
	}
	return nil
}

// sortKey implements Git's tree ordering: tree names compare as if they had a
// trailing '/', all other entries compare the raw name.
func sortKey(e Entry) string {
	if e.Mode.IsTree() {
		return e.Name + "/"
	}
	return e.Name
}

// ParseTree parses serialized tree content, verifies its content digest, and
// validates entry framing, names, modes, ordering, duplicate names and
// directory/file conflicts. The returned Tree is safe to cache.
func ParseTree(id ObjectID, content []byte) (*Tree, error) {
	algo, err := verifyDigest(id, TypeTree, content)
	if err != nil {
		return nil, err
	}

	tree := &Tree{ID: id, Algo: algo}
	rest := content
	for len(rest) > 0 {
		sp := bytes.IndexByte(rest, ' ')
		if sp <= 0 {
			return nil, fmt.Errorf("%w: missing mode separator", ErrMalformedTree)
		}
		mode, err := parseMode(string(rest[:sp]))
		if err != nil {
			return nil, err
		}
		rest = rest[sp+1:]

		nul := bytes.IndexByte(rest, 0)
		if nul < 0 {
			return nil, fmt.Errorf("%w: missing name terminator", ErrMalformedTree)
		}
		name := string(rest[:nul])
		if err := ValidateName(name); err != nil {
			return nil, err
		}
		rest = rest[nul+1:]

		if len(rest) < algo.HexLen()/2 {
			return nil, fmt.Errorf("%w: truncated entry id", ErrMalformedTree)
		}
		entryID := ObjectID(hex.EncodeToString(rest[:algo.HexLen()/2]))
		if _, _, err := ParseID(string(entryID)); err != nil {
			return nil, err
		}
		rest = rest[algo.HexLen()/2:]

		tree.Entries = append(tree.Entries, Entry{Mode: mode, Name: name, ID: entryID})
	}

	if err := validateEntryOrder(tree.Entries); err != nil {
		return nil, err
	}
	return tree, nil
}

func validateEntryOrder(entries []Entry) error {
	for i := 1; i < len(entries); i++ {
		prev, cur := entries[i-1], entries[i]
		// Git never allows two entries with the same raw name. Two entries
		// with identical modes are plain duplicates; a tree and a non-tree
		// sharing the name is a directory/file conflict.
		if prev.Name == cur.Name {
			if prev.Mode == cur.Mode {
				return fmt.Errorf("%w: %q", ErrDuplicateName, cur.Name)
			}
			return fmt.Errorf("%w: %q", ErrDirFileConflict, cur.Name)
		}
		prevKey, curKey := sortKey(prev), sortKey(cur)
		if prevKey > curKey {
			return fmt.Errorf("%w: entries out of order (%q after %q)", ErrMalformedTree, cur.Name, prev.Name)
		}
		if prevKey == curKey {
			// Equal sort keys with different names is a directory/file
			// conflict on the shared base name.
			return fmt.Errorf("%w: %q", ErrDirFileConflict, cur.Name)
		}
	}
	return nil
}

// MarshalTree serializes entries in Git tree format after validating every
// entry and the required ordering. It is intended for constructing fixtures
// and for callers producing tree objects.
func MarshalTree(algo Algorithm, entries []Entry) ([]byte, error) {
	copied := make([]Entry, len(entries))
	copy(copied, entries)
	for _, e := range copied {
		if err := ValidateName(e.Name); err != nil {
			return nil, err
		}
		if _, _, err := ParseID(string(e.ID)); err != nil {
			return nil, fmt.Errorf("entry %q: %w", e.Name, err)
		}
		if len(e.ID) != algo.HexLen() {
			return nil, fmt.Errorf("entry %q: id does not use %s", e.Name, algo)
		}
		switch e.Mode {
		case ModeTree, ModeRegular, ModeExecutable, ModeSymlink, ModeGitlink:
		default:
			return nil, fmt.Errorf("entry %q: %w", e.Name, ErrUnknownMode)
		}
	}
	if err := validateEntryOrder(copied); err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	for _, e := range copied {
		fmt.Fprintf(&buf, "%s %s\x00", e.Mode, e.Name)
		raw, err := hex.DecodeString(string(e.ID))
		if err != nil {
			return nil, fmt.Errorf("entry %q: %w", e.Name, err)
		}
		buf.Write(raw)
	}
	return buf.Bytes(), nil
}
