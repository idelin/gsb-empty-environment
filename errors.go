package netsec

import (
	"errors"
	"fmt"
)

var (
	// ErrInvalidArchive reports an archive that cannot be decoded as ZIP.
	ErrInvalidArchive = errors.New("netsec: invalid zip archive")

	// ErrMalformedExtra reports a structurally invalid extra field.
	ErrMalformedExtra = errors.New("netsec: malformed zip extra field")

	// ErrPathTraversal reports a path that is absolute or leaves the root.
	ErrPathTraversal = errors.New("netsec: unsafe archive path")

	// ErrUnsafePath and ErrAbsolutePath are compatibility aliases for the same
	// adjudication failure.
	ErrUnsafePath   = ErrPathTraversal
	ErrAbsolutePath = ErrPathTraversal
)

// PathError identifies the archive field whose path failed the root check.
type PathError struct {
	Entry  string
	Field  string
	Path   string
	Reason string
}

func (e *PathError) Error() string {
	if e.Entry != "" {
		return fmt.Sprintf("netsec: %s %q for entry %q: %s", e.Field, e.Path, e.Entry, e.Reason)
	}
	return fmt.Sprintf("netsec: %s %q: %s", e.Field, e.Path, e.Reason)
}

func (e *PathError) Unwrap() error {
	return ErrPathTraversal
}
