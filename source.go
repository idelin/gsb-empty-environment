package netsec

import (
	"io"
	"os"
	"path"
	"path/filepath"
)

// OpenSource opens a virtual .tcl path using the same descriptor-relative
// checks as Open and requires the final object to be a regular file.
func (t *Translator) OpenSource(virtualPath string) (*os.File, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	index, rel, err := t.parseToken(virtualPath)
	if err != nil {
		return nil, err
	}
	if path.Ext(rel) != ".tcl" {
		return nil, accessError("source", tokenFor(index, ""), ErrNotTclFile)
	}
	return t.openLocked("source", virtualPath, true)
}

// Source reads a virtual .tcl file for evaluation by the master interpreter.
//
// The extension check and all root, subdirectory, symlink, and race checks
// are identical to OpenSource. Source never resolves the path through an
// unchecked string.
func (t *Translator) Source(virtualPath string) ([]byte, error) {
	file, err := t.OpenSource(virtualPath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	content, err := io.ReadAll(file)
	if err != nil {
		return nil, accessError("source", "", classifyIOError(err))
	}
	return content, nil
}

// openLocked is the body shared with Open. The caller must hold t.mu.
func (t *Translator) openLocked(operation, virtualPath string, requireRegular bool) (*os.File, error) {
	index, rel, err := t.parseToken(virtualPath)
	if err != nil {
		return nil, err
	}
	root := &t.roots[index]
	route, err := routeFor(root, rel)
	if err != nil {
		return nil, accessError(operation, tokenFor(index, ""), err)
	}

	name := route.rel
	if name == "" {
		name = "."
	}
	file, err := route.root.Open(filepath.FromSlash(name))
	if err != nil {
		return nil, accessError(operation, tokenFor(index, ""), classifyIOError(err))
	}

	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, accessError(operation, tokenFor(index, ""), classifyIOError(err))
	}
	if requireRegular && !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, accessError(operation, tokenFor(index, ""), ErrNotRegularFile)
	}
	return file, nil
}
