package netsec

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"sort"
	"strings"
	"sync"
)

// Modules is an immutable module-name-to-root mapping built from trusted
// configuration. Request paths are always interpreted relative to the
// selected module's Root; a module name is never joined into a filesystem
// path.
type Modules struct {
	roots map[string]*Root

	mu     sync.RWMutex
	closed bool
}

// ModuleSet is an alias for Modules.
type ModuleSet = Modules

// NewModules opens every configured module root. If any root is invalid, all
// previously opened roots are closed and an error is returned.
func NewModules(config map[string]string) (*Modules, error) {
	if len(config) == 0 {
		return nil, fmt.Errorf("netsec: module configuration is empty")
	}
	configured := make(map[string]string, len(config))
	for name, root := range config {
		configured[name] = root
	}
	names := make([]string, 0, len(config))
	for name := range configured {
		names = append(names, name)
	}
	sort.Strings(names)

	modules := &Modules{roots: make(map[string]*Root, len(config))}
	for _, name := range names {
		if err := validateModuleName(name); err != nil {
			modules.Close()
			return nil, err
		}
		root, err := NewRoot(configured[name])
		if err != nil {
			modules.Close()
			return nil, fmt.Errorf("netsec: module %q: %w", name, err)
		}
		for existingName, existing := range modules.roots {
			if withinRoot(existing.Name(), root.Name()) || withinRoot(root.Name(), existing.Name()) {
				_ = root.Close()
				modules.Close()
				return nil, fmt.Errorf(
					"netsec: module roots %q and %q overlap; modules must be isolated",
					existingName, name,
				)
			}
		}
		modules.roots[name] = root
	}
	return modules, nil
}

// NewModuleSet is an alias for NewModules.
func NewModuleSet(config map[string]string) (*Modules, error) {
	return NewModules(config)
}

func validateModuleName(name string) error {
	if name == "" {
		return fmt.Errorf("%w: empty module name", ErrInvalidPath)
	}
	if name == "." || name == ".." {
		return fmt.Errorf("%w: invalid module name %q", ErrInvalidPath, name)
	}
	if strings.ContainsAny(name, "/\\\x00") {
		return fmt.Errorf("%w: module name contains a path separator: %q", ErrInvalidPath, name)
	}
	if windowsAbsolute(name) {
		return fmt.Errorf("%w: module name looks absolute: %q", ErrInvalidPath, name)
	}
	return nil
}

// Names returns the configured module names in sorted order.
func (m *Modules) Names() []string {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed {
		return nil
	}
	names := make([]string, 0, len(m.roots))
	for name := range m.roots {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Root returns the configured root for an exact module name. It deliberately
// performs a map lookup and no path manipulation.
func (m *Modules) Root(module string) (*Root, error) {
	if m == nil {
		return nil, ErrUnknownModule
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed {
		return nil, ErrRootClosed
	}
	root, ok := m.roots[module]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownModule, module)
	}
	return root, nil
}

// Module returns the configured root for an exact module name.
func (m *Modules) Module(module string) (*Root, error) {
	return m.Root(module)
}

// ModuleRoot returns the configured root for an exact module name.
func (m *Modules) ModuleRoot(module string) (*Root, error) {
	return m.Root(module)
}

// Resolve resolves a request path within the selected module.
func (m *Modules) Resolve(module, name string) (string, error) {
	root, err := m.Root(module)
	if err != nil {
		return "", err
	}
	return root.Resolve(name)
}

// ReadFile reads a request path within the selected module.
func (m *Modules) ReadFile(module, name string) ([]byte, error) {
	root, err := m.Root(module)
	if err != nil {
		return nil, err
	}
	return root.ReadFile(name)
}

// Open opens a file for reading within the selected module.
func (m *Modules) Open(module, name string) (*os.File, error) {
	root, err := m.Root(module)
	if err != nil {
		return nil, err
	}
	return root.Open(name)
}

// Stat returns file information within the selected module.
func (m *Modules) Stat(module, name string) (fs.FileInfo, error) {
	root, err := m.Root(module)
	if err != nil {
		return nil, err
	}
	return root.Stat(name)
}

// WriteFile atomically writes a request path within the selected module.
func (m *Modules) WriteFile(module, name string, data []byte, perm fs.FileMode) error {
	root, err := m.Root(module)
	if err != nil {
		return err
	}
	return root.WriteFile(name, data, perm)
}

// WriteFrom atomically writes streamed content within the selected module.
func (m *Modules) WriteFrom(module, name string, src io.Reader, perm fs.FileMode) error {
	root, err := m.Root(module)
	if err != nil {
		return err
	}
	return root.WriteFrom(name, src, perm)
}

// Mkdir creates one directory within the selected module.
func (m *Modules) Mkdir(module, name string, perm fs.FileMode) error {
	root, err := m.Root(module)
	if err != nil {
		return err
	}
	return root.Mkdir(name, perm)
}

// MkdirAll creates a directory hierarchy within the selected module.
func (m *Modules) MkdirAll(module, name string, perm fs.FileMode) error {
	root, err := m.Root(module)
	if err != nil {
		return err
	}
	return root.MkdirAll(name, perm)
}

// Rename renames a request path within one module. It cannot move between
// modules.
func (m *Modules) Rename(module, oldName, newName string) error {
	root, err := m.Root(module)
	if err != nil {
		return err
	}
	return root.Rename(oldName, newName)
}

// Remove deletes a request path within the selected module.
func (m *Modules) Remove(module, name string) error {
	root, err := m.Root(module)
	if err != nil {
		return err
	}
	return root.Remove(name)
}

// RemoveAll deletes a file or directory tree within the selected module.
func (m *Modules) RemoveAll(module, name string) error {
	root, err := m.Root(module)
	if err != nil {
		return err
	}
	return root.RemoveAll(name)
}

// Close closes all module roots. It is safe to call more than once.
func (m *Modules) Close() error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil
	}
	m.closed = true

	var firstErr error
	for _, root := range m.roots {
		if err := root.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
