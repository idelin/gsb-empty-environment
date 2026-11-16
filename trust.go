package netsec

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

var (
	// ErrUntrustedRepository is returned when a repository may not be used
	// because its ownership is not trusted.
	ErrUntrustedRepository = errors.New("netsec: repository is not trusted")

	// ErrUntrustedParent is returned when repository discovery would have to
	// cross a directory owned by another user.
	ErrUntrustedParent = errors.New("netsec: refusing to cross an untrusted parent directory")

	// ErrRepositoryNotFound is returned when no repository is found.
	ErrRepositoryNotFound = errors.New("netsec: repository not found")
)

// TrustReason explains why a repository was accepted.
type TrustReason string

const (
	// TrustReasonOwner means that the repository and its Git directory are
	// owned by the principal user.
	TrustReasonOwner TrustReason = "owner"

	// TrustReasonSafeDirectory means that the repository matched an entry in
	// the system or global configuration.
	TrustReasonSafeDirectory TrustReason = "safe-directory"
)

// Repository describes a repository and its trust decision.
//
// Inspect and Discover return a Repository even when Trusted is false. Callers
// must not read the repository's local configuration unless Trusted is true.
type Repository struct {
	WorkTree       string
	GitDir         string
	OwnerUID       uint32
	GitDirOwnerUID uint32
	CurrentUID     uint32
	Trusted        bool
	Reason         TrustReason
	SafeDirectory  string
	checker        *Checker
}

// ConfigPath returns the repository-local Git configuration path.
func (r *Repository) ConfigPath() string {
	if r == nil || r.GitDir == "" {
		return ""
	}
	return filepath.Join(r.GitDir, "config")
}

// ReadConfig reads the repository-local configuration only after checking the
// repository's trust again. It returns ErrUntrustedRepository when the
// repository is not trusted.
func (r *Repository) ReadConfig() ([]byte, error) {
	if r == nil {
		return nil, ErrRepositoryNotFound
	}

	path := r.WorkTree
	if path == "" {
		path = r.GitDir
	}

	checker := r.checker
	if checker == nil {
		checker = NewChecker()
	}
	current, err := checker.Inspect(path)
	if err != nil {
		return nil, err
	}
	if !current.Trusted {
		return nil, current.untrustedError()
	}

	data, err := os.ReadFile(current.ConfigPath())
	if err != nil {
		return nil, fmt.Errorf("read repository config: %w", err)
	}
	return data, nil
}

func (r *Repository) untrustedError() error {
	return &UntrustedRepositoryError{
		Path:           r.WorkTree,
		GitDir:         r.GitDir,
		OwnerUID:       r.OwnerUID,
		GitDirOwnerUID: r.GitDirOwnerUID,
		CurrentUID:     r.CurrentUID,
	}
}

// UntrustedRepositoryError contains the ownership information behind an
// ErrUntrustedRepository decision.
type UntrustedRepositoryError struct {
	Path           string
	GitDir         string
	OwnerUID       uint32
	GitDirOwnerUID uint32
	CurrentUID     uint32
}

func (e *UntrustedRepositoryError) Error() string {
	return fmt.Sprintf(
		"%v: %s (working tree uid %d, git dir uid %d, current uid %d)",
		ErrUntrustedRepository, e.Path, e.OwnerUID, e.GitDirOwnerUID, e.CurrentUID,
	)
}

// Is lets errors.Is match ErrUntrustedRepository.
func (e *UntrustedRepositoryError) Is(target error) bool {
	return target == ErrUntrustedRepository
}

// UntrustedParentError describes the ownership boundary that stopped upward
// repository discovery.
type UntrustedParentError struct {
	Path       string
	OwnerUID   uint32
	CurrentUID uint32
}

func (e *UntrustedParentError) Error() string {
	return fmt.Sprintf(
		"%v: %s is owned by uid %d, current uid is %d",
		ErrUntrustedParent, e.Path, e.OwnerUID, e.CurrentUID,
	)
}

// Is lets errors.Is match ErrUntrustedParent.
func (e *UntrustedParentError) Is(target error) bool {
	return target == ErrUntrustedParent
}

// RepositoryNotFoundError identifies the starting point of a failed discovery.
type RepositoryNotFoundError struct {
	Start string
}

func (e *RepositoryNotFoundError) Error() string {
	return fmt.Sprintf("%v from %s", ErrRepositoryNotFound, e.Start)
}

// Is lets errors.Is match ErrRepositoryNotFound.
func (e *RepositoryNotFoundError) Is(target error) bool {
	return target == ErrRepositoryNotFound
}

// Checker performs repository trust checks. A zero-value Checker is not valid;
// use NewChecker.
type Checker struct {
	currentUID       func() uint32
	ownerUID         func(string) (uint32, error)
	getenv           func(string) (string, bool)
	homeDir          string
	systemConfig     string
	systemConfigSet  bool
	globalConfigs    []string
	globalConfigsSet bool
}

// Option configures a Checker.
type Option func(*Checker)

// NewChecker returns a Checker using the current process, OS ownership
// metadata, and the normal Git system/global configuration locations.
func NewChecker(opts ...Option) *Checker {
	c := &Checker{
		currentUID: func() uint32 { return uint32(os.Geteuid()) },
		ownerUID:   fileOwnerUID,
		getenv:     os.LookupEnv,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(c)
		}
	}
	return c
}

// WithCurrentUID overrides the process UID used when SUDO_UID is absent. It is
// useful for tests and for callers that already know the principal identity.
func WithCurrentUID(uid uint32) Option {
	return func(c *Checker) {
		c.currentUID = func() uint32 { return uid }
	}
}

// WithOwner overrides the ownership lookup. The function must return the UID
// of the named path. It is useful for tests and non-Unix integrations.
func WithOwner(owner func(string) (uint32, error)) Option {
	return func(c *Checker) {
		if owner != nil {
			c.ownerUID = owner
		}
	}
}

// WithEnvironment overrides environment lookup.
func WithEnvironment(getenv func(string) (string, bool)) Option {
	return func(c *Checker) {
		if getenv != nil {
			c.getenv = getenv
		}
	}
}

// WithHomeDir overrides the home directory used for ~ expansion and default
// global configuration locations.
func WithHomeDir(home string) Option {
	return func(c *Checker) {
		c.homeDir = home
	}
}

// WithSystemConfig overrides the system Git config path. An empty path
// disables the system config.
func WithSystemConfig(path string) Option {
	return func(c *Checker) {
		c.systemConfig = path
		c.systemConfigSet = true
	}
}

// WithGlobalConfigs overrides the global Git config paths.
func WithGlobalConfigs(paths ...string) Option {
	return func(c *Checker) {
		c.globalConfigs = append([]string(nil), paths...)
		c.globalConfigsSet = true
	}
}

// PrincipalUID returns the UID against which repository ownership is compared.
// If SUDO_UID is set, its value is used; otherwise the process effective UID is
// used.
func (c *Checker) PrincipalUID() (uint32, error) {
	if value, ok := c.getenv("SUDO_UID"); ok && strings.TrimSpace(value) != "" {
		uid, err := strconv.ParseUint(strings.TrimSpace(value), 10, 32)
		if err != nil {
			return 0, fmt.Errorf("netsec: invalid SUDO_UID %q: %w", value, err)
		}
		return uint32(uid), nil
	}
	return c.currentUID(), nil
}

// PrincipalUID is the package-level form of Checker.PrincipalUID.
func PrincipalUID() (uint32, error) {
	return NewChecker().PrincipalUID()
}

// Inspect checks exactly the repository rooted at path. It does not search
// parent directories.
func (c *Checker) Inspect(path string) (*Repository, error) {
	return c.inspectPath(path)
}

// Inspect checks exactly the repository rooted at path.
func Inspect(path string) (*Repository, error) {
	return NewChecker().Inspect(path)
}

// Discover searches start and then its parents for a .git entry. Upward
// traversal stops at a directory owned by another user unless that directory
// is allowed by a protected safe.directory entry.
func (c *Checker) Discover(start string) (*Repository, error) {
	dir, err := existingDirectory(start, true)
	if err != nil {
		return nil, err
	}

	currentUID, err := c.PrincipalUID()
	if err != nil {
		return nil, err
	}

	first := true
	for {
		ownerUID, err := c.ownerUID(dir)
		if err != nil {
			return nil, fmt.Errorf("stat owner of %s: %w", dir, err)
		}
		owned := ownerUID == currentUID

		safe := false
		if !owned {
			safe, err = c.pathIsSafeDirectory(dir)
			if err != nil {
				return nil, err
			}
			if !safe && !first {
				return nil, &UntrustedParentError{
					Path:       dir,
					OwnerUID:   ownerUID,
					CurrentUID: currentUID,
				}
			}
		}

		repo, found, err := c.inspectAt(dir)
		if err != nil {
			return nil, err
		}
		if found {
			return c.classify(repo)
		}

		if !owned && !safe {
			return nil, &UntrustedParentError{
				Path:       dir,
				OwnerUID:   ownerUID,
				CurrentUID: currentUID,
			}
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return nil, &RepositoryNotFoundError{Start: start}
		}
		dir = parent
		first = false
	}
}

// Discover searches start and its parents for a repository.
func Discover(start string) (*Repository, error) {
	return NewChecker().Discover(start)
}

// IsTrusted discovers a repository from path and reports whether it is trusted.
// An untrusted repository produces (false, nil), not an ownership error.
func (c *Checker) IsTrusted(path string) (bool, error) {
	repo, err := c.Discover(path)
	if err != nil {
		return false, err
	}
	return repo.Trusted, nil
}

// IsTrusted discovers a repository from path and reports whether it is trusted.
func IsTrusted(path string) (bool, error) {
	return NewChecker().IsTrusted(path)
}

// RequireTrusted discovers a repository and returns ErrUntrustedRepository if
// it cannot be trusted.
func (c *Checker) RequireTrusted(path string) (*Repository, error) {
	repo, err := c.Discover(path)
	if err != nil {
		return nil, err
	}
	if !repo.Trusted {
		return nil, repo.untrustedError()
	}
	return repo, nil
}

// RequireTrusted discovers a repository and rejects it if it is untrusted.
func RequireTrusted(path string) (*Repository, error) {
	return NewChecker().RequireTrusted(path)
}

// ReadConfig reads a repository's local config only if the repository is
// trusted. The trust check is repeated immediately before the read.
func ReadConfig(path string) ([]byte, error) {
	repo, err := RequireTrusted(path)
	if err != nil {
		return nil, err
	}
	return repo.ReadConfig()
}

// ReadConfig reads a repository's local config only if it is trusted.
func (c *Checker) ReadConfig(path string) ([]byte, error) {
	repo, err := c.RequireTrusted(path)
	if err != nil {
		return nil, err
	}
	return repo.ReadConfig()
}

// IsSafeDirectory reports whether path itself matches safe.directory in the
// system or global Git configuration. Repository-local configuration is never
// consulted.
func (c *Checker) IsSafeDirectory(path string) (bool, error) {
	dir, err := existingDirectory(path, false)
	if err != nil {
		return false, err
	}
	return c.pathIsSafeDirectory(dir)
}

// IsSafeDirectory reports whether path itself matches safe.directory in
// protected Git configuration.
func IsSafeDirectory(path string) (bool, error) {
	return NewChecker().IsSafeDirectory(path)
}

func (c *Checker) inspectPath(path string) (*Repository, error) {
	dir, err := existingDirectory(path, false)
	if err != nil {
		return nil, err
	}

	if filepath.Base(dir) == ".git" && looksLikeGitDir(dir) {
		workTree := filepath.Dir(dir)
		ownerUID, err := c.ownerUID(workTree)
		if err != nil {
			return nil, fmt.Errorf("stat owner of %s: %w", workTree, err)
		}
		gitOwnerUID, err := c.ownerUID(dir)
		if err != nil {
			return nil, fmt.Errorf("stat owner of %s: %w", dir, err)
		}
		return c.classify(&Repository{
			WorkTree:       workTree,
			GitDir:         dir,
			OwnerUID:       ownerUID,
			GitDirOwnerUID: gitOwnerUID,
		})
	}

	repo, found, err := c.inspectAt(dir)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, &RepositoryNotFoundError{Start: path}
	}
	return c.classify(repo)
}

func (c *Checker) inspectAt(dir string) (*Repository, bool, error) {
	gitPath := filepath.Join(dir, ".git")
	info, err := os.Lstat(gitPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("inspect %s: %w", gitPath, err)
	}

	gitDir := ""
	switch {
	case info.IsDir():
		gitDir = gitPath
	case info.Mode().IsRegular():
		target, err := readGitFile(gitPath, dir)
		if err != nil {
			return nil, false, err
		}
		gitDir = target
	default:
		return nil, false, fmt.Errorf("netsec: unsupported .git entry at %s", gitPath)
	}

	gitDir, err = canonicalExisting(gitDir)
	if err != nil {
		return nil, false, fmt.Errorf("resolve git directory %s: %w", gitDir, err)
	}
	gitInfo, err := os.Stat(gitDir)
	if err != nil {
		return nil, false, fmt.Errorf("stat git directory %s: %w", gitDir, err)
	}
	if !gitInfo.IsDir() {
		return nil, false, fmt.Errorf("netsec: git directory %s is not a directory", gitDir)
	}

	ownerUID, err := c.ownerUID(dir)
	if err != nil {
		return nil, false, fmt.Errorf("stat owner of %s: %w", dir, err)
	}
	gitOwnerUID, err := c.ownerUID(gitDir)
	if err != nil {
		return nil, false, fmt.Errorf("stat owner of %s: %w", gitDir, err)
	}

	return &Repository{
		WorkTree:       dir,
		GitDir:         gitDir,
		OwnerUID:       ownerUID,
		GitDirOwnerUID: gitOwnerUID,
	}, true, nil
}

func (c *Checker) classify(repo *Repository) (*Repository, error) {
	currentUID, err := c.PrincipalUID()
	if err != nil {
		return nil, err
	}
	repo.CurrentUID = currentUID
	repo.checker = c

	if repo.OwnerUID == currentUID && repo.GitDirOwnerUID == currentUID {
		repo.Trusted = true
		repo.Reason = TrustReasonOwner
		return repo, nil
	}

	entries, err := c.loadSafeDirectories(repo.ConfigPath())
	if err != nil {
		return nil, err
	}
	if value, ok := matchSafeDirectory(entries, repo.WorkTree, repo.GitDir); ok {
		repo.Trusted = true
		repo.Reason = TrustReasonSafeDirectory
		repo.SafeDirectory = value
	}
	return repo, nil
}

func (c *Checker) pathIsSafeDirectory(path string) (bool, error) {
	localConfig := filepath.Join(path, ".git", "config")
	entries, err := c.loadSafeDirectories(localConfig)
	if err != nil {
		return false, err
	}
	_, ok := matchSafeDirectory(entries, path)
	return ok, nil
}

func matchSafeDirectory(entries []safeDirectoryEntry, paths ...string) (string, bool) {
	for _, entry := range entries {
		for _, path := range paths {
			if entry.matches(path) {
				return entry.value, true
			}
		}
	}
	return "", false
}

func readGitFile(path, workTree string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open %s: %w", path, err)
	}
	defer file.Close()

	const maxGitFileSize = 64 * 1024
	data, err := io.ReadAll(io.LimitReader(file, maxGitFileSize+1))
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	if len(data) > maxGitFileSize {
		return "", fmt.Errorf("netsec: .git file at %s is too large", path)
	}
	const prefix = "gitdir:"
	line := strings.TrimSpace(string(data))
	if before, _, ok := strings.Cut(line, "\n"); ok {
		line = strings.TrimSpace(before)
	}
	if len(line) < len(prefix) || !strings.EqualFold(line[:len(prefix)], prefix) {
		return "", fmt.Errorf("netsec: invalid .git file at %s", path)
	}
	target := strings.TrimSpace(line[len(prefix):])
	if target == "" {
		return "", fmt.Errorf("netsec: empty gitdir in %s", path)
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(workTree, target)
	}
	return target, nil
}

func looksLikeGitDir(path string) bool {
	for _, name := range []string{"HEAD", "objects"} {
		if _, err := os.Lstat(filepath.Join(path, name)); err != nil {
			return false
		}
	}
	return true
}

func existingDirectory(path string, allowFile bool) (string, error) {
	canonical, err := canonicalExisting(path)
	if err != nil {
		return "", fmt.Errorf("netsec: inspect %s: %w", path, err)
	}
	info, err := os.Stat(canonical)
	if err != nil {
		return "", fmt.Errorf("netsec: inspect %s: %w", path, err)
	}
	if !info.IsDir() {
		if !allowFile || !info.Mode().IsRegular() {
			return "", fmt.Errorf("netsec: %s is not a directory", path)
		}
		canonical = filepath.Dir(canonical)
	}
	return canonical, nil
}

func canonicalExisting(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	return filepath.Clean(resolved), nil
}
