package netsec

import (
	"errors"
	"strings"
)

var (
	// ErrAbsolutePath is returned when the input path is absolute.
	ErrAbsolutePath = errors.New("netsec: absolute path not allowed")
	// ErrEmptySegment is returned when the path contains an empty segment
	// ("a//b", a trailing slash, or an empty path).
	ErrEmptySegment = errors.New("netsec: empty path segment")
	// ErrEscapesRoot is returned when ".." or a symlink target would
	// resolve above the preopen root.
	ErrEscapesRoot = errors.New("netsec: path escapes preopen root")
	// ErrSymlinkLoop is returned when symlink expansions exceed the limit.
	ErrSymlinkLoop = errors.New("netsec: too many symlink expansions")
	// ErrNotDirectory is returned when a non-final segment is not a
	// directory, or OpenDirectory is set on a non-directory.
	ErrNotDirectory = errors.New("netsec: not a directory")
	// ErrNotCapable is returned when open-flags require a capability the
	// effective descriptor flags do not grant.
	ErrNotCapable = errors.New("netsec: open flags require capabilities not held by parent descriptor")
)

// DefaultMaxSymlinkExpansions bounds total symlink expansions per
// resolution, matching the 8 allowed by the sandbox policy.
const DefaultMaxSymlinkExpansions = 8

// Resolver resolves component-supplied paths against a preopen root.
type Resolver struct {
	FS FS
	// MaxSymlinkExpansions bounds total symlink expansions per call.
	// Values <= 0 select DefaultMaxSymlinkExpansions.
	MaxSymlinkExpansions int
}

// Request describes one descriptor.open-at call.
type Request struct {
	Path      string
	PathFlags PathFlags
	OpenFlags OpenFlags
	// Requested is the descriptor-flags set the component asks for.
	Requested DescriptorFlags
	// Parent is the descriptor-flags set of the descriptor open-at is
	// invoked on.
	Parent DescriptorFlags
}

// Result is the outcome of a successful resolution.
type Result struct {
	// Path is the resolved path relative to the preopen root. It never
	// contains "." or ".." and never escapes the root.
	Path string
	// Flags is the effective descriptor-flags set: Requested narrowed to
	// a subset of the parent's flags.
	Flags DescriptorFlags
	// FinalEntry describes the entry the resolved path refers to. Valid
	// is false when the final segment does not exist and OpenCreate is
	// set.
	FinalEntry Entry
	Exists     bool
}

// Resolve performs sandboxed path resolution for descriptor.open-at.
func (r *Resolver) Resolve(req Request) (Result, error) {
	maxExpansions := r.MaxSymlinkExpansions
	if maxExpansions <= 0 {
		maxExpansions = DefaultMaxSymlinkExpansions
	}

	segs, err := splitPath(req.Path)
	if err != nil {
		return Result{}, err
	}

	// A child descriptor's rights may only be a subset of its parent's:
	// a read-only parent can never yield a descriptor with write access.
	effective := req.Requested & req.Parent

	// Creating or truncating requires write capability.
	if req.OpenFlags&(OpenCreate|OpenTruncate) != 0 && effective&FlagWrite == 0 {
		return Result{}, ErrNotCapable
	}

	followFinal := req.PathFlags&PathFlagSymlinkFollow != 0

	var stack []string
	expansions := 0
	var final Entry
	exists := false

	for i := 0; i < len(segs); i++ {
		seg := segs[i]
		switch seg {
		case ".":
			continue
		case "..":
			if len(stack) == 0 {
				return Result{}, ErrEscapesRoot
			}
			stack = stack[:len(stack)-1]
			continue
		}

		last := i == len(segs)-1
		entry, err := r.FS.Lookup(strings.Join(stack, "/"), seg)
		if err != nil {
			if errors.Is(err, ErrNotExist) && last && req.OpenFlags&OpenCreate != 0 {
				stack = append(stack, seg)
				exists = false
				final = Entry{}
				goto done
			}
			return Result{}, err
		}

		if entry.Type == EntrySymlink && (!last || followFinal) {
			expansions++
			if expansions > maxExpansions {
				return Result{}, ErrSymlinkLoop
			}
			target, err := splitPath(entry.Target)
			if err != nil {
				if errors.Is(err, ErrAbsolutePath) {
					// An absolute symlink target would resolve
					// outside the preopen root.
					return Result{}, ErrEscapesRoot
				}
				return Result{}, err
			}
			// The target is resolved relative to the directory
			// holding the symlink, i.e. the current stack.
			rest := make([]string, 0, len(target)+len(segs)-i-1)
			rest = append(rest, target...)
			rest = append(rest, segs[i+1:]...)
			segs = rest
			i = -1
			continue
		}

		if !last && entry.Type != EntryDirectory {
			return Result{}, ErrNotDirectory
		}
		stack = append(stack, seg)
		if last {
			final = entry
			exists = true
		}
	}

done:
	if req.OpenFlags&OpenDirectory != 0 && exists && final.Type != EntryDirectory {
		return Result{}, ErrNotDirectory
	}
	return Result{
		Path:       strings.Join(stack, "/"),
		Flags:      effective,
		FinalEntry: final,
		Exists:     exists,
	}, nil
}

// splitPath splits a relative path into segments, rejecting absolute paths
// and empty segments.
func splitPath(p string) ([]string, error) {
	if p == "" {
		return nil, ErrEmptySegment
	}
	if strings.HasPrefix(p, "/") {
		return nil, ErrAbsolutePath
	}
	parts := strings.Split(p, "/")
	for _, part := range parts {
		if part == "" {
			return nil, ErrEmptySegment
		}
	}
	return parts, nil
}
