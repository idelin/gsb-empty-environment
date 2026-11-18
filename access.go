package netsec

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Access is a set of operations a protected target may be accessed with.
// The zero value is not a valid target access set; callers should select at
// least one operation.
type Access uint32

const (
	AccessRead Access = 1 << iota
	AccessWrite
	AccessCreate
	AccessTruncate
	AccessDelete
	AccessRename
	AccessMetadata
	AccessExecute
)

const (
	// AccessModify covers operations that can change a path or its contents.
	AccessModify = AccessWrite | AccessCreate | AccessTruncate | AccessDelete |
		AccessRename | AccessMetadata
	// AccessAll covers every operation known to this package.
	AccessAll = AccessRead | AccessModify | AccessExecute
)

var accessOrder = []struct {
	bit  Access
	name string
}{
	{AccessRead, "read"},
	{AccessWrite, "write"},
	{AccessCreate, "create"},
	{AccessTruncate, "truncate"},
	{AccessDelete, "delete"},
	{AccessRename, "rename"},
	{AccessMetadata, "metadata"},
	{AccessExecute, "execute"},
}

// Has reports whether every bit in want is present in a.
func (a Access) Has(want Access) bool {
	return a&want == want
}

// Intersects reports whether a and b share at least one operation.
func (a Access) Intersects(b Access) bool {
	return a&b != 0
}

// String returns a stable, comma-separated representation of a.
func (a Access) String() string {
	if a == 0 {
		return "none"
	}
	parts := make([]string, 0, len(accessOrder))
	for _, entry := range accessOrder {
		if a&entry.bit != 0 {
			parts = append(parts, entry.name)
		}
	}
	return strings.Join(parts, ",")
}

// ParseAccess parses access names such as "read", "read,write", "modify" or
// "all". It accepts commas, pipes, plus signs, and whitespace as separators.
func ParseAccess(value string) (Access, error) {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return 0, fmt.Errorf("access mode is empty")
	}

	var result Access
	for _, field := range strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == '|' || r == '+' || r == ' ' || r == '\t' || r == '\n'
	}) {
		switch field {
		case "read":
			result |= AccessRead
		case "write":
			result |= AccessWrite
		case "create":
			result |= AccessCreate
		case "truncate":
			result |= AccessTruncate
		case "delete", "unlink", "remove":
			result |= AccessDelete
		case "rename", "move":
			result |= AccessRename
		case "metadata", "meta", "chmod", "chown", "xattr":
			result |= AccessMetadata
		case "execute", "exec":
			result |= AccessExecute
		case "modify":
			result |= AccessModify
		case "all":
			result |= AccessAll
		case "readwrite", "rw":
			result |= AccessRead | AccessWrite
		default:
			return 0, fmt.Errorf("unknown access mode %q", field)
		}
	}
	if result == 0 {
		return 0, fmt.Errorf("access mode %q selects no operations", value)
	}
	return result, nil
}

// MarshalJSON renders Access as a list of operation names.
func (a Access) MarshalJSON() ([]byte, error) {
	names := make([]string, 0, len(accessOrder))
	for _, entry := range accessOrder {
		if a&entry.bit != 0 {
			names = append(names, entry.name)
		}
	}
	return json.Marshal(names)
}

// UnmarshalJSON accepts either a numeric bit mask, a string, or a list of
// strings.
func (a *Access) UnmarshalJSON(data []byte) error {
	var asNumber uint32
	if err := json.Unmarshal(data, &asNumber); err == nil {
		*a = Access(asNumber)
		return nil
	}

	var asString string
	if err := json.Unmarshal(data, &asString); err == nil {
		parsed, err := ParseAccess(asString)
		if err != nil {
			return err
		}
		*a = parsed
		return nil
	}

	var asStrings []string
	if err := json.Unmarshal(data, &asStrings); err != nil {
		return fmt.Errorf("access must be a number, string, or list of strings: %w", err)
	}
	parsed, err := ParseAccess(strings.Join(asStrings, ","))
	if err != nil {
		return err
	}
	*a = parsed
	return nil
}
