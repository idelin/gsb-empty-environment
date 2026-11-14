package netsec

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// Sentinel errors returned by the spool package. Callers can use errors.Is
// to distinguish an attacker-controlled, rejected lookup from an ordinary
// I/O failure.
var (
	// ErrQueueNotAllowed is returned when a queue name is not registered
	// in the whitelist.
	ErrQueueNotAllowed = errors.New("netsec: queue is not in the whitelist")
	// ErrControlNotAllowed is returned when a control file name has not
	// been explicitly admitted for the target queue.
	ErrControlNotAllowed = errors.New("netsec: control file is not in the whitelist")
	// ErrInvalidName is returned for names that are empty, absolute,
	// contain path separators or NUL bytes, or escape their directory
	// after canonicalization (e.g. "..", "a/../../etc").
	ErrInvalidName = errors.New("netsec: invalid file or queue name")
	// ErrAbsolute is returned when a supplied name is an absolute path.
	ErrAbsolute = fmt.Errorf("%w: absolute paths are rejected", ErrInvalidName)
	// ErrSymlink is returned when any path component that is opened is a
	// symbolic link.
	ErrSymlink = errors.New("netsec: symbolic links are rejected")
	// ErrNotRegular is returned when an opened entry is not a regular file.
	ErrNotRegular = errors.New("netsec: spool entry is not a regular file")
	// ErrOutsideSpool is returned when the canonical path would resolve
	// outside the owning queue spool directory.
	ErrOutsideSpool = errors.New("netsec: path escapes the queue spool directory")
	// ErrInsecurePermission is returned when a spool directory or file
	// grants write access to group or world, or carries a setuid/setgid
	// or sticky bit.
	ErrInsecurePermission = errors.New("netsec: insecure file or directory permissions")
	// ErrTooLarge is returned when a file or the aggregate job exceeds
	// the configured size limits.
	ErrTooLarge = errors.New("netsec: job or file exceeds size limit")
	// ErrTooManyFiles is returned when a job references more data files
	// than the configured limit.
	ErrTooManyFiles = errors.New("netsec: job exceeds file count limit")
)

var (
	queueNameRE = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,63}$`)
	controlRE   = regexp.MustCompile(`^cf[A-Za-z0-9]{1,8}[A-Za-z0-9_.-]{0,24}$`)
	dataRE      = regexp.MustCompile(`^df[A-Za-z0-9]{1,8}[A-Za-z0-9_.-]{0,24}$`)
)

// Limits bounds the resources a single print job may consume. A zero field
// means "no limit"; callers normally obtain a usable value from
// DefaultLimits.
type Limits struct {
	// MaxJobBytes bounds the sum of the sizes of every file in a job.
	MaxJobBytes int64
	// MaxFileBytes bounds the size of any individual control or data
	// file and also caps how much data may be read through File.Read.
	MaxFileBytes int64
	// MaxFiles bounds the number of distinct data files in a job. The
	// control file is not counted.
	MaxFiles int
}

// DefaultLimits is a conservative starting point for ordinary print queues.
func DefaultLimits() Limits {
	return Limits{
		MaxJobBytes:  16 << 20, // 16 MiB total per job
		MaxFileBytes: 4 << 20,  // 4 MiB per file / per read
		MaxFiles:     32,
	}
}

// Option customizes a Queue.
type Option func(*Queue)

// WithInsecurePermissionMask overrides the permission bits that must be
// clear on spool directories and job files. The default mask rejects any
// group/world write access as well as setuid, setgid and sticky bits.
func WithInsecurePermissionMask(mask os.FileMode) Option {
	return func(q *Queue) { q.insecureMask = mask }
}

// defaultInsecureMask rejects group/world-writable entries and any special
// mode bits.
const defaultInsecureMask = os.ModeSetuid | os.ModeSetgid | os.ModeSticky |
	os.FileMode(0o022)

// Queue is a single whitelisted print queue backed by a dedicated spool
// directory. The zero value is not usable; construct one with NewQueue.
type Queue struct {
	name         string
	spoolDir     string
	limits       Limits
	insecureMask os.FileMode
	mu           sync.RWMutex
	controls     map[string]struct{}
}

// NewQueue registers a queue after validating that its spool directory is a
// real, local directory (never a symlink) with acceptable permissions.
func NewQueue(name, spoolDir string, limits Limits, opts ...Option) (*Queue, error) {
	if !validComponent(name) || !queueNameRE.MatchString(name) {
		return nil, fmt.Errorf("%w: queue %q", ErrInvalidName, name)
	}
	cleaned, err := prepareSpoolDir(spoolDir)
	if err != nil {
		return nil, err
	}
	q := &Queue{
		name:         name,
		spoolDir:     cleaned,
		limits:       limits,
		insecureMask: defaultInsecureMask,
		controls:     make(map[string]struct{}),
	}
	for _, opt := range opts {
		opt(q)
	}
	return q, nil
}

func prepareSpoolDir(spoolDir string) (string, error) {
	if strings.ContainsRune(spoolDir, 0) {
		return "", fmt.Errorf("%w: NUL byte in spool path", ErrInvalidName)
	}
	cleaned := filepath.Clean(spoolDir)
	if !filepath.IsAbs(cleaned) {
		return "", fmt.Errorf("%w: spool directory must be absolute", ErrInvalidName)
	}
	info, err := os.Lstat(cleaned)
	if err != nil {
		return "", fmt.Errorf("netsec: cannot stat spool directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("%w: spool directory", ErrSymlink)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%w: spool path is not a directory", ErrNotRegular)
	}
	if info.Mode()&defaultInsecureMask != 0 {
		return "", fmt.Errorf("%w: spool directory mode %v",
			ErrInsecurePermission, info.Mode().Perm())
	}
	return cleaned, nil
}

// Name returns the whitelisted queue name.
func (q *Queue) Name() string { return q.name }

// SpoolDir returns the absolute, cleaned spool directory.
func (q *Queue) SpoolDir() string { return q.spoolDir }

// Limits returns the queue's resource limits.
func (q *Queue) Limits() Limits { return q.limits }

// AllowControl adds a control file name to the queue's whitelist. The name
// must be a single safe path component and match the LPR control file
// convention (cf...). Unknown control names are never opened.
func (q *Queue) AllowControl(controlName string) error {
	if !validComponent(controlName) || !controlRE.MatchString(controlName) {
		return fmt.Errorf("%w: control file %q", ErrControlNotAllowed, controlName)
	}
	q.mu.Lock()
	q.controls[controlName] = struct{}{}
	q.mu.Unlock()
	return nil
}

func (q *Queue) controlAllowed(name string) bool {
	q.mu.RLock()
	_, ok := q.controls[name]
	q.mu.RUnlock()
	return ok
}

// OpenControl opens an admitted control file. The name must be whitelisted
// via AllowControl and must remain inside the queue spool directory.
func (q *Queue) OpenControl(ctx context.Context, controlName string) (*File, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !validComponent(controlName) || !controlRE.MatchString(controlName) {
		return nil, fmt.Errorf("%w: control file %q", ErrControlNotAllowed, controlName)
	}
	if !q.controlAllowed(controlName) {
		return nil, fmt.Errorf("%w: %q", ErrControlNotAllowed, controlName)
	}
	return q.openFile(ctx, controlName)
}

// OpenDataFile opens a data file whose name follows the LPR data file
// convention (df...). Data files are not individually whitelisted; their
// names must still be single safe components confined to the spool.
func (q *Queue) OpenDataFile(ctx context.Context, dataName string) (*File, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if dataName != "" && !strings.ContainsRune(dataName, 0) && filepath.IsAbs(dataName) {
		return nil, fmt.Errorf("%w: data file %q", ErrAbsolute, dataName)
	}
	if !validComponent(dataName) || !dataRE.MatchString(dataName) {
		return nil, fmt.Errorf("%w: data file %q", ErrInvalidName, dataName)
	}
	return q.openFile(ctx, dataName)
}

// Job is a validated print job consisting of one control file and zero or
// more distinct data files, all opened through the same queue.
type Job struct {
	Queue     *Queue
	Control   *File
	Data      []*File
	TotalSize int64
}

// Close releases every file handle held by the job.
func (j *Job) Close() error {
	var first error
	for _, f := range j.Data {
		if err := f.Close(); err != nil && first == nil {
			first = err
		}
	}
	if j.Control != nil {
		if err := j.Control.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// OpenJob validates and opens a complete job, enforcing data-file count and
// aggregate size limits. On success every returned handle is owned by the
// caller and must be released through Job.Close.
func (q *Queue) OpenJob(ctx context.Context, controlName string, dataNames []string) (*Job, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if q.limits.MaxFiles > 0 && len(dataNames) > q.limits.MaxFiles {
		return nil, fmt.Errorf("%w: %d data files (limit %d)",
			ErrTooManyFiles, len(dataNames), q.limits.MaxFiles)
	}

	job := &Job{Queue: q}
	ok := false
	defer func() {
		if !ok {
			job.Close()
		}
	}()

	seen := make(map[string]struct{}, len(dataNames))
	var total int64
	for _, dataName := range dataNames {
		if _, dup := seen[dataName]; dup {
			return nil, fmt.Errorf("%w: duplicate data file %q", ErrInvalidName, dataName)
		}
		seen[dataName] = struct{}{}
		f, err := q.OpenDataFile(ctx, dataName)
		if err != nil {
			return nil, err
		}
		job.Data = append(job.Data, f)
		total += f.size
	}

	control, err := q.OpenControl(ctx, controlName)
	if err != nil {
		return nil, err
	}
	job.Control = control
	total += control.size

	if q.limits.MaxJobBytes > 0 && total > q.limits.MaxJobBytes {
		return nil, fmt.Errorf("%w: %d bytes (limit %d)",
			ErrTooLarge, total, q.limits.MaxJobBytes)
	}
	job.TotalSize = total
	ok = true
	return job, nil
}

func (q *Queue) openFile(ctx context.Context, name string) (*File, error) {
	if err := q.checkSpool(); err != nil {
		return nil, err
	}
	if !validComponent(name) {
		return nil, classifyName(name)
	}

	// Defense in depth: even though validComponent rejects separators and
	// "..", recompute the canonical join and confirm containment before
	// touching the filesystem.
	target := filepath.Join(q.spoolDir, name)
	cleaned := filepath.Clean(target)
	rel, err := filepath.Rel(q.spoolDir, cleaned)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return nil, fmt.Errorf("%w: %q", ErrOutsideSpool, name)
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	// O_NOFOLLOW guarantees the final component is never a symlink. The
	// only directory component is the re-validated spool directory.
	f, err := openNoFollow(cleaned)
	if err != nil {
		return nil, err
	}

	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("netsec: cannot stat opened file: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		f.Close()
		return nil, fmt.Errorf("%w: %q", ErrSymlink, name)
	}
	if !info.Mode().IsRegular() {
		f.Close()
		return nil, fmt.Errorf("%w: %q", ErrNotRegular, name)
	}
	if info.Mode()&q.insecureMask != 0 {
		f.Close()
		return nil, fmt.Errorf("%w: file %q mode %v",
			ErrInsecurePermission, name, info.Mode().Perm())
	}
	if q.limits.MaxFileBytes > 0 && info.Size() > q.limits.MaxFileBytes {
		f.Close()
		return nil, fmt.Errorf("%w: file %q is %d bytes (limit %d)",
			ErrTooLarge, name, info.Size(), q.limits.MaxFileBytes)
	}
	return &File{
		queue: q,
		name:  name,
		file:  f,
		size:  info.Size(),
	}, nil
}

func (q *Queue) checkSpool() error {
	info, err := os.Lstat(q.spoolDir)
	if err != nil {
		return fmt.Errorf("netsec: cannot stat spool directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: spool directory", ErrSymlink)
	}
	if !info.IsDir() {
		return fmt.Errorf("%w: spool path is not a directory", ErrNotRegular)
	}
	if info.Mode()&q.insecureMask != 0 {
		return fmt.Errorf("%w: spool directory mode %v",
			ErrInsecurePermission, info.Mode().Perm())
	}
	return nil
}

// validComponent enforces the "single safe name component" rule: no NUL
// bytes, no absolute paths, no separators, and no form that canonicalizes
// outside its parent directory.
func validComponent(name string) bool {
	if name == "" || strings.ContainsRune(name, 0) {
		return false
	}
	if filepath.IsAbs(name) {
		return false
	}
	if strings.ContainsAny(name, `/\`) {
		return false
	}
	if filepath.Clean(name) != name {
		return false
	}
	if name == "." || name == ".." {
		return false
	}
	return true
}

// classifyName returns the most precise sentinel error for a rejected name.
func classifyName(name string) error {
	if name != "" && !strings.ContainsRune(name, 0) && filepath.IsAbs(name) {
		return fmt.Errorf("%w: %q", ErrAbsolute, name)
	}
	return fmt.Errorf("%w: %q", ErrInvalidName, name)
}

// File is a safely opened spool entry. It wraps the underlying descriptor so
// reads cannot exceed the configured per-file limit.
type File struct {
	queue *Queue
	name  string
	file  *os.File
	size  int64
	read  int64
}

// Name returns the validated spool-relative file name.
func (f *File) Name() string { return f.name }

// Size returns the file's recorded size in bytes.
func (f *File) Size() int64 { return f.size }

// Read implements io.Reader, additionally bounding the number of bytes that
// can be consumed to the queue's per-file limit.
func (f *File) Read(p []byte) (int, error) {
	limit := f.queue.limits.MaxFileBytes
	if limit > 0 {
		remaining := limit - f.read
		if remaining <= 0 {
			return 0, io.EOF
		}
		if int64(len(p)) > remaining {
			p = p[:remaining]
		}
	}
	n, err := f.file.Read(p)
	f.read += int64(n)
	return n, err
}

// Reader returns the file itself as an io.Reader; the same read cap applies.
func (f *File) Reader() io.Reader { return f }

// Close releases the underlying descriptor.
func (f *File) Close() error {
	if f == nil || f.file == nil {
		return nil
	}
	err := f.file.Close()
	f.file = nil
	return err
}
