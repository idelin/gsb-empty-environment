package netsec

import (
	"errors"
	"fmt"
)

// Errors returned by the object codec, the in-memory object store, and the
// source-closure builder. Callers should use errors.Is rather than comparing
// error strings.
var (
	ErrObjectNotFound    = errors.New("netsec: object not found")
	ErrObjectExists      = errors.New("netsec: object already exists")
	ErrDigestMismatch    = errors.New("netsec: object digest mismatch")
	ErrInvalidObject     = errors.New("netsec: invalid object")
	ErrMalformedObject   = errors.New("netsec: malformed object")
	ErrInvalidPath       = errors.New("netsec: invalid path")
	ErrDuplicateEntry    = errors.New("netsec: duplicate tree entry")
	ErrPathConflict      = errors.New("netsec: workspace path conflict")
	ErrUndeclaredGitlink = errors.New("netsec: undeclared gitlink")
	ErrUntrustedCommit   = errors.New("netsec: commit outside trusted closure")
	ErrSubmoduleCycle    = errors.New("netsec: repository/commit cycle")
	ErrTreeCycle         = errors.New("netsec: tree cycle")
	ErrSubmoduleDepth    = errors.New("netsec: submodule depth exceeded")
	ErrInvalidPolicy     = errors.New("netsec: invalid trust policy")
)

// ClosureError adds repository and path context to an error while preserving
// errors.Is/errors.As behavior through Unwrap.
type ClosureError struct {
	Op         string
	Repository RepositoryID
	Path       string
	Err        error
}

func (e *ClosureError) Error() string {
	var where string
	switch {
	case e.Repository != "" && e.Path != "":
		where = fmt.Sprintf(" repository=%q path=%q", e.Repository, e.Path)
	case e.Repository != "":
		where = fmt.Sprintf(" repository=%q", e.Repository)
	case e.Path != "":
		where = fmt.Sprintf(" path=%q", e.Path)
	}
	if e.Op == "" {
		return fmt.Sprintf("%v%s", e.Err, where)
	}
	return fmt.Sprintf("%s: %v%s", e.Op, e.Err, where)
}

func (e *ClosureError) Unwrap() error {
	return e.Err
}

func closureError(repo RepositoryID, path string, err error, op string) error {
	if err == nil {
		return nil
	}
	return &ClosureError{Op: op, Repository: repo, Path: path, Err: err}
}
