// Package rootedpublish publishes files into a fixed root directory using a
// directory file descriptor as the trust anchor.
//
// A naive upload handler validates a target path and only afterwards opens it.
// An attacker who can write to any directory along the path can win the
// check-then-use race (TOCTOU) by swapping in a symlink in between, redirecting
// the write outside the intended root. This package removes the window:
//
//   - the root is opened once as an O_DIRECTORY file descriptor;
//   - every path component is walked with openat(2) relative to the previous
//     descriptor using O_NOFOLLOW, so a symlink can never redirect a lookup;
//   - the payload is streamed into a private O_NOFOLLOW temporary file living
//     in the final parent directory (same filesystem, same volume);
//   - the temporary file is fsynced and atomically renamed over the target;
//   - on any failure the temporary file is unlinked through the parent
//     descriptor, leaving the previous version and the rest of the tree
//     untouched.
package rootedpublish

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"syscall"
)

// HookPoint identifies a controllable synchronization point inside Publish.
// Hooks exist for tests that need to mutate the directory tree while Publish is
// blocked to prove the operation cannot be diverted.
type HookPoint string

const (
	// HookResolvedParent fires after the parent directory has been resolved
	// and opened as a descriptor but before the temporary file is created.
	HookResolvedParent HookPoint = "resolved_parent"
	// HookTempCreated fires after the temporary file has been created and
	// before any payload byte is written.
	HookTempCreated HookPoint = "temp_created"
	// HookAfterWrite fires after the whole payload has been copied and fsynced
	// but before the atomic rename.
	HookAfterWrite HookPoint = "after_write"
	// HookAfterRename fires after the rename. The new content is already
	// visible at the target path and the commit cannot be rolled back, so the
	// callback is observation-only: its returned error is ignored.
	HookAfterRename HookPoint = "after_rename"
)

// Sentinel errors. Callers can use errors.Is to classify failures.
var (
	// ErrInvalidPath is returned for empty paths, absolute paths, paths that
	// escape the root via "..", or paths containing NUL bytes.
	ErrInvalidPath = errors.New("rootedpublish: invalid or escaping path")
	// ErrSymlink is returned when a path component (including the final target
	// name) is a symbolic link. Symlinks are never followed by the publisher.
	ErrSymlink = errors.New("rootedpublish: symbolic link rejected")
	// ErrNotDirectory is returned when a path component that must be a
	// directory is something else.
	ErrNotDirectory = errors.New("rootedpublish: component is not a directory")
)

// Hook is invoked at the named synchronization point with the logical
// relative path being published. Returning an error aborts Publish; the error
// is wrapped and returned after the temporary file has been cleaned up.
// The hook may block; Publish honors ctx cancellation while blocked.
type Hook func(ctx context.Context, point HookPoint, relPath string) error

// Options controls a Publish call.
type Options struct {
	// Mode is the permission mode applied to the published file. Zero means
	// 0o644. The temporary file is always created 0o600 and chmod-ed only
	// after the payload is complete.
	Mode os.FileMode
	// CreateDirs, when true, creates missing parent directories (0o755) during
	// the descriptor walk. Existing symlinks are still rejected.
	CreateDirs bool
	// Hook optionally receives synchronization callbacks (see HookPoint).
	Hook Hook
}

// Publisher publishes files relative to a single root directory. It is safe
// for concurrent use: each Publish opens its own per-component descriptors.
type Publisher struct {
	rootFD int
	root   string
}

// Open opens rootPath and retains an O_DIRECTORY descriptor for it. The
// descriptor is the trust anchor; later renames of rootPath itself do not
// affect publishes because every lookup is relative to this descriptor.
func Open(rootPath string) (*Publisher, error) {
	fd, err := syscall.Open(rootPath,
		syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("rootedpublish: open root %q: %w", rootPath, err)
	}
	return &Publisher{rootFD: fd, root: rootPath}, nil
}

// RootFD returns the file descriptor of the root directory.
func (p *Publisher) RootFD() int { return p.rootFD }

// Close releases the root descriptor.
func (p *Publisher) Close() error {
	if p.rootFD < 0 {
		return nil
	}
	err := syscall.Close(p.rootFD)
	p.rootFD = -1
	return err
}

// splitRel validates relPath and returns its intermediate components plus the
// final file name. Only canonical relative paths are accepted: no absolute
// paths, no "." or ".." components, no empty components (double slashes) and
// no NUL bytes. Rejecting ".." outright means the descriptor walk never has to
// interpret a component that could leave the root.
func splitRel(relPath string) (dirs []string, name string, err error) {
	if relPath == "" || strings.ContainsRune(relPath, 0) {
		return nil, "", ErrInvalidPath
	}
	if path.IsAbs(relPath) {
		return nil, "", ErrInvalidPath
	}
	parts := strings.Split(relPath, "/")
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return nil, "", ErrInvalidPath
		}
	}
	if len(parts) == 0 {
		return nil, "", fmt.Errorf("%w: target must name a file", ErrInvalidPath)
	}
	return parts[:len(parts)-1], parts[len(parts)-1], nil
}

// isSymlink reports whether the directory entry name under dirFD is a symbolic
// link, without following it.
func isSymlink(dirFD int, name string) (bool, error) {
	var st syscall.Stat_t
	if err := fstatAT(dirFD, name, &st, atSYMLINKNOFOLLOW); err != nil {
		return false, err
	}
	return st.Mode&syscall.S_IFMT == syscall.S_IFLNK, nil
}

// openChildDir opens the directory name under dirFD, refusing symlinks. When
// create is true and the entry is missing it is created first. The returned
// descriptor must be closed by the caller.
func openChildDir(parentFD int, name string, create bool) (int, error) {
	if link, err := isSymlink(parentFD, name); err == nil && link {
		return -1, fmt.Errorf("component %q: %w", name, ErrSymlink)
	}
	fd, err := openAT(parentFD, name,
		syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		if !create || !errors.Is(err, syscall.ENOENT) {
			if errors.Is(err, syscall.ENOTDIR) {
				return -1, fmt.Errorf("component %q: %w", name, ErrNotDirectory)
			}
			return -1, err
		}
		if err := mkdirAT(parentFD, name, 0o755); err != nil {
			return -1, err
		}
		fd, err = openAT(parentFD, name,
			syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
		if err != nil {
			return -1, err
		}
	}
	return fd, nil
}

// resolveParent walks dirs from the root descriptor, opening one descriptor per
// component and closing each parent as it descends. The returned descriptor
// owns the final parent directory and must be closed by the caller.
func (p *Publisher) resolveParent(dirs []string, create bool) (int, error) {
	parentFD, err := syscall.Dup(p.rootFD)
	if err != nil {
		return -1, err
	}
	for _, component := range dirs {
		childFD, err := openChildDir(parentFD, component, create)
		_ = syscall.Close(parentFD)
		if err != nil {
			return -1, err
		}
		parentFD = childFD
	}
	return parentFD, nil
}

// Publish streams r to relPath under the root and atomically replaces any
// existing file. On success the new content is durable (fsynced) and visible;
// on failure the previous content is preserved and no temporary file remains.
func (p *Publisher) Publish(ctx context.Context, relPath string, r io.Reader, opts Options) error {
	if p.rootFD < 0 {
		return errors.New("rootedpublish: publisher is closed")
	}
	mode := opts.Mode.Perm()
	if mode == 0 {
		mode = 0o644
	}
	dirs, name, err := splitRel(relPath)
	if err != nil {
		return err
	}

	parentFD, err := p.resolveParent(dirs, opts.CreateDirs)
	if err != nil {
		return err
	}
	defer syscall.Close(parentFD)

	// A symlink at the final name would redirect the temporary-file open.
	// Reject it explicitly for a typed error; the kernel-level O_NOFOLLOW in
	// createTemp is the actual race-proof guard.
	if link, lerr := isSymlink(parentFD, name); lerr == nil && link {
		return fmt.Errorf("target %q: %w", name, ErrSymlink)
	}

	if opts.Hook != nil {
		if err := runHook(ctx, opts.Hook, HookResolvedParent, relPath); err != nil {
			return err
		}
	}

	tmpName, tmpFD, err := createTemp(parentFD)
	if err != nil {
		return err
	}
	// From here on every error path closes the descriptor and removes the
	// temporary directory entry. The fd is never wrapped in an *os.File, so no
	// runtime finalizer can close it out-of-band and let another goroutine
	// reuse the number.
	cleaned := false
	cleanup := func() {
		if cleaned {
			return
		}
		cleaned = true
		_ = syscall.Close(tmpFD)
		_ = unlinkAT(parentFD, tmpName, 0)
	}
	defer cleanup()

	if opts.Hook != nil {
		if err := runHook(ctx, opts.Hook, HookTempCreated, relPath); err != nil {
			return err
		}
	}

	if _, err := copyWithContext(ctx, tmpFD, r); err != nil {
		return err
	}
	if err := syscall.Fsync(tmpFD); err != nil {
		return fmt.Errorf("fsync temp file: %w", err)
	}
	if err := syscall.Fchmod(tmpFD, uint32(mode)); err != nil {
		return fmt.Errorf("fchmod temp file: %w", err)
	}

	if opts.Hook != nil {
		if err := runHook(ctx, opts.Hook, HookAfterWrite, relPath); err != nil {
			return err
		}
	}

	// renameat within one directory is atomic: either the old content or the
	// new content is visible at name, never a partial file.
	if err := renameAT(parentFD, tmpName, parentFD, name); err != nil {
		return fmt.Errorf("atomic rename: %w", err)
	}
	cleaned = true // the temporary entry no longer exists under tmpName.
	_ = syscall.Close(tmpFD)

	if opts.Hook != nil {
		_ = runHook(ctx, opts.Hook, HookAfterRename, relPath)
	}
	return nil
}

// createTemp makes a uniquely named 0o600 regular file in parentFD with
// O_NOFOLLOW|O_EXCL so the call fails rather than following or clobbering an
// attacker-planted entry.
func createTemp(parentFD int) (string, int, error) {
	for attempt := 0; ; attempt++ {
		var nonce [8]byte
		if _, err := rand.Read(nonce[:]); err != nil {
			return "", -1, fmt.Errorf("generate temp name: %w", err)
		}
		tmpName := fmt.Sprintf(".rootedpublish.tmp.%d.%x", pid(), nonce)
		fd, err := openAT(parentFD, tmpName,
			syscall.O_WRONLY|syscall.O_CREAT|syscall.O_EXCL|syscall.O_NOFOLLOW|syscall.O_CLOEXEC,
			0o600)
		if err == nil {
			return tmpName, fd, nil
		}
		if errors.Is(err, syscall.EEXIST) && attempt < 100 {
			continue
		}
		return "", -1, fmt.Errorf("create temp file: %w", err)
	}
}

// runHook invokes hook and maps context cancellation through.
func runHook(ctx context.Context, hook Hook, point HookPoint, relPath string) error {
	done := make(chan error, 1)
	go func() { done <- hook(ctx, point, relPath) }()
	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("hook %s: %w", point, err)
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// copyWithContext streams src into the raw descriptor dst while honoring ctx
// cancellation. The descriptor is owned and closed by the caller.
func copyWithContext(ctx context.Context, dst int, src io.Reader) (int64, error) {
	buf := make([]byte, 32*1024)
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		nr, readErr := src.Read(buf)
		if nr > 0 {
			if err := writeFD(dst, buf[:nr]); err != nil {
				return total, err
			}
			total += int64(nr)
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return total, nil
			}
			return total, readErr
		}
	}
}
