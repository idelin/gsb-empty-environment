package netsec

import (
	"io/fs"
	"os"
	"strings"
)

// FileInfo is the caller-provided ownership and permission snapshot for a
// path. Mode should include fs.ModeDir for directories and fs.ModeSymlink for
// symbolic links. Symlinks are treated as unverifiable unless the caller
// resolves them before building the view.
type FileInfo struct {
	Mode fs.FileMode
	UID  uint32
	GID  uint32
}

// FileView is a read-only snapshot of the target filesystem. Lookup must
// return false only when the exact cleaned path does not exist in the
// snapshot. The release service should populate every ancestor of a path that
// it expects to verify; an incomplete view can turn a writable ancestor into
// an apparent cache miss.
//
// Implementations must be safe for concurrent use if Analyze itself is called
// concurrently with the same view.
type FileView interface {
	Lookup(path string) (FileInfo, bool)
}

// MapFileView is a convenient FileView backed by a map. Keys must be
// normalized, slash-separated paths.
type MapFileView map[string]FileInfo

// Lookup implements FileView.
func (m MapFileView) Lookup(path string) (FileInfo, bool) {
	info, ok := m[path]
	return info, ok
}

// TrustPolicy defines which owners and groups are trusted not to modify files
// or directories. A nil or empty TrustedUIDs slice trusts only UID 0; a nil or
// empty TrustedGIDs slice trusts only GID 0.
//
// Owners outside TrustedUIDs are considered able to replace or chmod their
// entries even when the write mode bits are clear. A non-trusted group is
// considered able to write whenever the group-write bit is set.
//
// This policy models POSIX owner/group/mode bits. ACLs, capabilities, and
// read-only mounts must be folded into the supplied view by the caller.
type TrustPolicy struct {
	TrustedUIDs []uint32
	TrustedGIDs []uint32
}

func (p TrustPolicy) trustedUID(uid uint32) bool {
	if len(p.TrustedUIDs) == 0 {
		return uid == 0
	}
	for _, trusted := range p.TrustedUIDs {
		if uid == trusted {
			return true
		}
	}
	return false
}

func (p TrustPolicy) trustedGID(gid uint32) bool {
	if len(p.TrustedGIDs) == 0 {
		return gid == 0
	}
	for _, trusted := range p.TrustedGIDs {
		if gid == trusted {
			return true
		}
	}
	return false
}

// CacheEntry is one record from an ld.so.cache snapshot. Entries with the
// same Name are searched in slice order, matching the cache's precedence.
type CacheEntry struct {
	Name string
	Path string
}

// Options supplies the environment that cannot be read from the ELF file
// itself. Analyze never reads the live filesystem for ownership, permissions,
// the cache, or preload contents; the caller supplies those views.
type Options struct {
	// Files is the ownership and mode snapshot used for all paths. It is
	// required.
	Files FileView

	// Cache is the relevant snapshot of /etc/ld.so.cache entries. It is
	// searched after RUNPATH (or RPATH) and before DefaultDirs.
	Cache []CacheEntry

	// DefaultDirs are the system default library directories in loader order,
	// for example /lib, /lib64, /usr/lib, /usr/lib64. They are used only when
	// the ELF does not set DF_1_NODEFLIB. No platform defaults are assumed.
	DefaultDirs []string

	// Preload contains the whitespace-separated entries from
	// /etc/ld.so.preload. ParsePreloadList can split the file contents.
	Preload []string

	// WorkingDir is the process working directory used to make relative
	// search path elements absolute for reporting. Relative elements are
	// always reported as unsafe because a caller of a setuid executable can
	// choose the working directory. An empty value is treated as "/".
	WorkingDir string

	// Origin overrides the $ORIGIN expansion base. If empty, it is derived
	// from the analyzed ELF path. Set it to the installed executable's
	// directory when admission analyzes a staged copy elsewhere.
	Origin string

	// TokenValues supplies expansions for dynamic string tokens other than
	// ORIGIN, commonly LIB and PLATFORM. Missing token values are reported as
	// unsafe rather than silently guessed.
	TokenValues map[string]string

	// Trust identifies non-root owners and groups that are nevertheless
	// trusted. See TrustPolicy.
	Trust TrustPolicy
}

// EntryKind distinguishes dependencies from preload entries.
type EntryKind string

const (
	NeededEntry  EntryKind = "needed"
	PreloadEntry EntryKind = "preload"
)

// SearchSource identifies the stage at which a candidate was considered.
type SearchSource string

const (
	SourceDirect  SearchSource = "direct"
	SourceRPATH   SearchSource = "rpath"
	SourceRUNPATH SearchSource = "runpath"
	SourceCache   SearchSource = "cache"
	SourceDefault SearchSource = "default"
)

// IssueCode is a stable machine-readable security or resolution finding.
type IssueCode string

const (
	IssueRelativePath      IssueCode = "relative-path"
	IssueUntrustedOwner    IssueCode = "untrusted-owner"
	IssueWorldWritable     IssueCode = "world-writable"
	IssueGroupWritable     IssueCode = "group-writable"
	IssueNotDirectory      IssueCode = "not-directory"
	IssueNotRegularFile    IssueCode = "not-regular-file"
	IssueUnresolvedToken   IssueCode = "unresolved-token"
	IssueMissingDependency IssueCode = "missing-dependency"
	IssueInvalidPath       IssueCode = "invalid-path"
)

// Issue describes one reason a path or dependency is not safe.
type Issue struct {
	Code   IssueCode
	Path   string
	Source SearchSource
	Detail string
}

// Candidate is one location considered while resolving an entry.
type Candidate struct {
	// Entry is the raw RPATH/RUNPATH/cache element or direct dependency.
	Entry string
	// Path is the full path that could be loaded.
	Path string
	// Directory is the directory searched for this candidate.
	Directory string
	Source    SearchSource
	Exists    bool
	Safe      bool
	Issues    []Issue
}

// Dependency is the resolution and security result for one DT_NEEDED or
// ld.so.preload entry. Search is in dynamic-loader order and stops at the
// first candidate whose file exists.
type Dependency struct {
	Name      string
	Kind      EntryKind
	Resolved  bool
	Path      string
	Directory string
	Source    SearchSource
	Search    []Candidate
	Safe      bool
	Issues    []Issue
}

// ELFMetadata exposes the dynamic tags used by the resolver.
type ELFMetadata struct {
	Needed        []string
	RPATH         []string
	RUNPATH       []string
	HasRPATH      bool
	HasRUNPATH    bool
	NoDefaultLibs bool
}

// Report is the result of checking one ELF executable.
type Report struct {
	ELFPath string
	Origin  string
	Needed  []Dependency
	Preload []Dependency
	Safe    bool
}

func dedupeIssues(in []Issue) []Issue {
	if len(in) < 2 {
		return in
	}
	out := make([]Issue, 0, len(in))
	seen := make(map[string]bool, len(in))
	for _, issue := range in {
		key := string(issue.Code) + "\x00" + string(issue.Source) + "\x00" + issue.Path + "\x00" + issue.Detail
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, issue)
	}
	return out
}

// ParsePreloadList splits /etc/ld.so.preload contents. Entries are separated
// by arbitrary ASCII whitespace.
func ParsePreloadList(data []byte) []string {
	return strings.Fields(string(data))
}

// ReadPreloadList reads and parses a preload list from path. The release
// service normally passes "/etc/ld.so.preload" after validating that the file
// itself comes from its trusted snapshot.
func ReadPreloadList(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParsePreloadList(data), nil
}
