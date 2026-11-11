package depseal

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strings"
)

// LockfileVersion is the only lock file schema version understood by this
// package.
const LockfileVersion = 1

// testScheme is reserved for in-process test sources that never touch the
// network. Production lock files must use http or https.
const testScheme = "depseal-test"

// Lockfile is the parsed representation of a depseal lock file.
type Lockfile struct {
	// Version is the schema version and must equal LockfileVersion.
	Version int `json:"version"`
	// Sources maps a source identifier referenced by Package.Source to its
	// trusted definition.
	Sources map[string]Source `json:"sources"`
	// Packages is the fully resolved, ordered set of locked packages.
	Packages []Package `json:"packages"`

	// index is populated during parsing for O(1) package lookup.
	index map[string]*Package
}

// Source binds a source identifier to a concrete origin and the set of hosts
// its responses may legitimately come from.
type Source struct {
	// ID is the key under which the source is declared in Lockfile.Sources.
	ID string `json:"id"`
	// BaseURL is the canonical origin of the source, for example
	// "https://registry.internal.example". Plain HTTP is accepted only for
	// loopback hosts, which keeps local test sources usable.
	BaseURL string `json:"base_url"`
	// AllowedHosts is the inclusive allowlist of hosts the source may
	// redirect to. The host of BaseURL is always implied. A redirect whose
	// target host is not on this list is treated as a cross-source escape.
	AllowedHosts []string `json:"allowed_hosts,omitempty"`

	base  *url.URL
	hosts map[string]struct{}
}

// Package is a single locked dependency.
type Package struct {
	// Name is the package name, unique within a lock file together with
	// Version.
	Name string `json:"name"`
	// Version is the locked version.
	Version string `json:"version"`
	// Source must reference a key in Lockfile.Sources and is the only source
	// the package may be fetched from.
	Source string `json:"source"`
	// Digest is the expected digest of the package blob, in the form
	// "sha256:<hex>".
	Digest Digest `json:"digest"`
	// Parent is the identity (name@version) of the dependency that required
	// this package. The empty string marks a direct (root) dependency. Every
	// named parent must itself be present in the lock file, so the
	// provenance chain is verifiable.
	Parent string `json:"parent,omitempty"`
}

// Identity returns the name@version coordinate identifying the package.
func (p Package) Identity() string {
	return p.Name + "@" + p.Version
}

// DependencyPath reconstructs the parent chain for a locked package, from the
// package itself back to its root dependency.
func (l *Lockfile) DependencyPath(pkg Package) ([]Package, error) {
	seen := make(map[string]struct{})
	path := []Package{pkg}
	seen[pkg.Identity()] = struct{}{}

	current := pkg
	for current.Parent != "" {
		parent, ok := l.index[current.Parent]
		if !ok {
			return nil, fmt.Errorf("depseal: package %s references unknown parent %q", current.Identity(), current.Parent)
		}
		if _, cycle := seen[parent.Identity()]; cycle {
			return nil, fmt.Errorf("depseal: parent cycle detected at %s", parent.Identity())
		}
		seen[parent.Identity()] = struct{}{}
		path = append(path, *parent)
		current = *parent
	}
	return path, nil
}

// Lookup returns the locked package matching name and version.
func (l *Lockfile) Lookup(name, version string) (Package, bool) {
	if l.index == nil {
		return Package{}, false
	}
	pkg, ok := l.index[name+"@"+version]
	if !ok {
		return Package{}, false
	}
	return *pkg, true
}

// ParseLockfile reads and validates a lock file. Validation fails on unknown
// sources, malformed digests, duplicate coordinates, dangling parents and
// untrusted source origins.
func ParseLockfile(r io.Reader) (*Lockfile, error) {
	dec := json.NewDecoder(r)
	var l Lockfile
	if err := dec.Decode(&l); err != nil {
		return nil, fmt.Errorf("depseal: parse lock file: %w", err)
	}
	if err := l.validate(); err != nil {
		return nil, err
	}
	return &l, nil
}

func (l *Lockfile) validate() error {
	if l.Version != LockfileVersion {
		return fmt.Errorf("depseal: unsupported lock file version %d, want %d", l.Version, LockfileVersion)
	}
	if len(l.Sources) == 0 {
		return fmt.Errorf("depseal: lock file declares no sources")
	}
	if len(l.Packages) == 0 {
		return fmt.Errorf("depseal: lock file declares no packages")
	}

	for id, src := range l.Sources {
		if err := (&src).validate(id); err != nil {
			return err
		}
		l.Sources[id] = src
	}

	l.index = make(map[string]*Package, len(l.Packages))
	for i := range l.Packages {
		pkg := &l.Packages[i]
		if pkg.Name == "" {
			return fmt.Errorf("depseal: package at index %d has no name", i)
		}
		if pkg.Version == "" {
			return fmt.Errorf("depseal: package %s has no version", pkg.Name)
		}
		if _, ok := l.Sources[pkg.Source]; !ok {
			return fmt.Errorf("depseal: package %s references unknown source %q", pkg.Identity(), pkg.Source)
		}
		if err := pkg.Digest.validate(); err != nil {
			return fmt.Errorf("depseal: package %s: %w", pkg.Identity(), err)
		}
		identity := pkg.Identity()
		if _, dup := l.index[identity]; dup {
			return fmt.Errorf("depseal: duplicate locked package %s", identity)
		}
		l.index[identity] = pkg
	}

	for i := range l.Packages {
		pkg := &l.Packages[i]
		if pkg.Parent == "" {
			continue
		}
		if _, ok := l.index[pkg.Parent]; !ok {
			return fmt.Errorf("depseal: package %s references unknown parent %q", pkg.Identity(), pkg.Parent)
		}
		if pkg.Parent == pkg.Identity() {
			return fmt.Errorf("depseal: package %s lists itself as parent", pkg.Identity())
		}
	}
	return nil
}

func (s *Source) validate(id string) error {
	if s.ID == "" {
		return fmt.Errorf("depseal: source %q has no id", id)
	}
	if s.ID != id {
		return fmt.Errorf("depseal: source declared under %q has mismatching id %q", id, s.ID)
	}
	base, err := url.Parse(s.BaseURL)
	if err != nil {
		return fmt.Errorf("depseal: source %s has invalid base_url: %w", id, err)
	}
	if base.Scheme != "https" && base.Scheme != "http" && base.Scheme != testScheme {
		return fmt.Errorf("depseal: source %s base_url must use http(s), got %q", id, base.Scheme)
	}
	if base.Host == "" {
		return fmt.Errorf("depseal: source %s base_url has no host", id)
	}
	if base.Scheme == "http" && !isLoopbackHost(base.Hostname()) {
		return fmt.Errorf("depseal: source %s uses http on non-loopback host %q", id, base.Hostname())
	}

	hosts := make(map[string]struct{}, len(s.AllowedHosts)+1)
	hosts[strings.ToLower(base.Host)] = struct{}{}
	for _, host := range s.AllowedHosts {
		h := strings.ToLower(strings.TrimSpace(host))
		if h == "" {
			return fmt.Errorf("depseal: source %s has empty allowed host", id)
		}
		hosts[h] = struct{}{}
	}

	s.base = base
	s.hosts = hosts
	return nil
}

func isLoopbackHost(host string) bool {
	host = strings.ToLower(host)
	switch host {
	case "localhost", "127.0.0.1", "[::1]", "::1":
		return true
	}
	return strings.HasPrefix(host, "127.")
}

// packageURL builds the canonical blob URL for a package on this source.
func (s Source) packageURL(pkg Package) (string, error) {
	if s.base == nil {
		return "", fmt.Errorf("depseal: source %s was not validated", s.ID)
	}
	ref := url.URL{
		Scheme: s.base.Scheme,
		Host:   s.base.Host,
		Path: strings.TrimSuffix(s.base.Path, "/") +
			"/packages/" + url.PathEscape(pkg.Name) +
			"/versions/" + url.PathEscape(pkg.Version) + "/blob",
	}
	return ref.String(), nil
}

// allowsHost reports whether host is inside the source's trusted host set.
func (s Source) allowsHost(host string) bool {
	_, ok := s.hosts[strings.ToLower(host)]
	return ok
}
