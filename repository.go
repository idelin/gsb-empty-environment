package rpki

import (
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path"
	"sort"
	"strings"
)

var (
	ErrRepositoryPath = errors.New("rpki: unsafe repository path")
	ErrNotFound       = errors.New("rpki: repository object not found")
)

// Repository is the read-only offline view used by validation.
type Repository interface {
	Open(name string) ([]byte, error)
	List(dir string) ([]string, error)
}

// MapRepository is an in-memory repository whose keys use slash paths.
type MapRepository struct {
	Files map[string][]byte
}

func NewMapRepository(files map[string][]byte) *MapRepository {
	copied := make(map[string][]byte, len(files))
	for name, content := range files {
		normalized, err := NormalizeRepositoryPath(name)
		if err != nil {
			panic(err)
		}
		copied[normalized] = append([]byte(nil), content...)
	}
	return &MapRepository{Files: copied}
}

func (r *MapRepository) Open(name string) ([]byte, error) {
	normalized, err := NormalizeRepositoryPath(name)
	if err != nil {
		return nil, err
	}
	content, ok := r.Files[normalized]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, normalized)
	}
	return append([]byte(nil), content...), nil
}

func (r *MapRepository) List(dir string) ([]string, error) {
	normalized, err := NormalizeRepositoryPath(dir)
	if err != nil {
		return nil, err
	}
	if normalized == "." {
		normalized = ""
	}
	prefix := ""
	if normalized != "" {
		prefix = normalized + "/"
	}
	seen := map[string]struct{}{}
	for name := range r.Files {
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		rest := strings.TrimPrefix(name, prefix)
		if rest == "" || strings.Contains(rest, "/") {
			continue
		}
		seen[name] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out, nil
}

// OSRepository exposes a synchronized local directory without network access.
type OSRepository struct {
	root string
	fsys fs.FS
}

func NewOSRepository(root string) (*OSRepository, error) {
	if root == "" {
		return nil, fmt.Errorf("%w: empty root", ErrRepositoryPath)
	}
	return &OSRepository{root: root, fsys: os.DirFS(root)}, nil
}

func (r *OSRepository) Open(name string) ([]byte, error) {
	normalized, err := NormalizeRepositoryPath(name)
	if err != nil {
		return nil, err
	}
	return fs.ReadFile(r.fsys, normalized)
}

func (r *OSRepository) List(dir string) ([]string, error) {
	normalized, err := NormalizeRepositoryPath(dir)
	if err != nil {
		return nil, err
	}
	entries, err := fs.ReadDir(r.fsys, normalized)
	if err != nil {
		return nil, err
	}
	prefix := ""
	if normalized != "." {
		prefix = normalized + "/"
	}
	var out []string
	for _, entry := range entries {
		if entry.IsDir() || !entry.Type().IsRegular() {
			continue
		}
		out = append(out, prefix+entry.Name())
	}
	sort.Strings(out)
	return out, nil
}

// NormalizeRepositoryPath enforces rooted slash paths without parent traversal.
func NormalizeRepositoryPath(name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("%w: empty path", ErrRepositoryPath)
	}
	if strings.ContainsRune(name, '\\') || strings.Contains(name, "\x00") {
		return "", fmt.Errorf("%w: invalid path %q", ErrRepositoryPath, name)
	}
	cleaned := path.Clean("/" + strings.TrimPrefix(name, "/"))
	cleaned = strings.TrimPrefix(cleaned, "/")
	if cleaned == "" {
		return ".", nil
	}
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("%w: traversal %q", ErrRepositoryPath, name)
	}
	return cleaned, nil
}

// JoinRepositoryPath joins and normalizes repository path components.
func JoinRepositoryPath(elem ...string) (string, error) {
	return NormalizeRepositoryPath(path.Join(elem...))
}

// MapRSyncURI maps rsync://host/path/dir/ to repository path "path/dir".
func MapRSyncURI(uri string) (string, error) {
	trimmed := strings.TrimSpace(uri)
	if trimmed == "" {
		return "", fmt.Errorf("%w: empty SIA URI", ErrRepositoryPath)
	}
	withoutScheme := trimmed
	if idx := strings.Index(withoutScheme, "://"); idx >= 0 {
		withoutScheme = withoutScheme[idx+3:]
	}
	if idx := strings.Index(withoutScheme, "/"); idx >= 0 {
		withoutScheme = withoutScheme[idx+1:]
	} else {
		withoutScheme = ""
	}
	withoutScheme, err := url.PathUnescape(withoutScheme)
	if err != nil {
		return "", fmt.Errorf("%w: bad SIA escaping: %v", ErrRepositoryPath, err)
	}
	normalized, err := NormalizeRepositoryPath(withoutScheme)
	if err != nil {
		return "", err
	}
	if normalized == "." {
		return "", fmt.Errorf("%w: SIA URI has no directory: %s", ErrRepositoryPath, uri)
	}
	return normalized, nil
}
