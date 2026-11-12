package netsec

import (
	"errors"
	"fmt"
	"strings"
	"syscall"
)

// Handler processes openat notifications using a Kernel backend.
//
// A Handler is safe for concurrent use as long as the Kernel is. It never
// sends SECCOMP_USER_NOTIF_FLAG_CONTINUE.
type Handler struct {
	Kernel Kernel
}

// NewHandler returns a Handler backed by k.
func NewHandler(k Kernel) *Handler {
	return &Handler{Kernel: k}
}

// Result records what the handler did with one notification.
//
// Errno is zero for an allowed open. For a denied open Errno is the positive
// errno sent to the target, Path and ResolvedPath contain the one-time
// snapshot that was validated (when available), and TargetFD is -1.
type Result struct {
	NotificationID uint64
	PID            uint32
	Path           string
	ResolvedPath   string
	View           View
	TargetFD       int
	Errno          syscall.Errno
}

// Allowed reports whether the open was performed and its fd was injected.
func (r *Result) Allowed() bool {
	return r != nil && r.Errno == 0 && r.TargetFD >= 0
}

// DenialError is returned after the handler has sent an error response to an
// openat notification. Errno is the value the target received.
type DenialError struct {
	Errno syscall.Errno
	Cause error
}

func (e *DenialError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("openat denied with errno %d: %v", e.Errno, e.Cause)
	}
	return fmt.Sprintf("openat denied with errno %d", e.Errno)
}

// Unwrap exposes the underlying reason for the denial.
func (e *DenialError) Unwrap() error {
	return e.Cause
}

// Handle decodes an openat notification and processes it. Notifications for
// other syscalls are rejected without touching the target.
func (h *Handler) Handle(n Notification) (*Result, error) {
	req, err := n.OpenatRequest()
	if err != nil {
		return nil, err
	}
	return h.HandleOpen(n, req)
}

// HandleOpen processes one openat notification.
//
// Sequence:
//
//  1. Validate the notification ID before reading.
//  2. Read the pathname exactly once into supervisor memory.
//  3. Validate the notification ID again, even if the read failed.
//  4. Resolve and open the copied pathname in the target's view.
//  5. Atomically inject the resulting fd and send the success response.
//
// If the ID is invalid at either check, no response is sent and
// ErrNotificationGone is returned. Any later failure is reported to the
// target as an errno response via Kernel.Respond.
func (h *Handler) HandleOpen(n Notification, req OpenRequest) (*Result, error) {
	if h == nil || h.Kernel == nil {
		return nil, ErrNoKernel
	}

	if err := h.Kernel.NotifValid(n.ID); err != nil {
		return nil, notificationGone("validate before path read", err)
	}

	// This is the only access to the traced process's memory in this package.
	rawPath, readErr := h.Kernel.ReadCString(n.PID, req.PathAddr, MaxPathBytes)

	// The target may have exited or another thread may have caused the
	// notification to be invalidated while the copy was in progress. Verify
	// again before acting on the copy, even when the copy itself failed.
	if err := h.Kernel.NotifValid(n.ID); err != nil {
		return nil, notificationGone("validate after path read", err)
	}

	res := &Result{
		NotificationID: n.ID,
		PID:            n.PID,
		TargetFD:       -1,
	}
	if readErr != nil {
		errno := errnoForRead(readErr)
		res.Errno = errno
		return res, h.deny(n.ID, errno, readErr)
	}

	path := string(rawPath)
	res.Path = path
	if errno, cause := validatePath(path); cause != nil {
		res.Errno = errno
		return res, h.deny(n.ID, errno, cause)
	}

	view, err := h.Kernel.ProcessView(n.PID)
	if err != nil {
		errno := errnoOrDefault(err, syscall.EACCES)
		res.Errno = errno
		return res, h.deny(n.ID, errno, err)
	}
	res.View = view

	base := view.Cwd
	if req.DirFD != AT_FDCWD {
		if req.DirFD < 0 {
			errno := syscall.EBADF
			cause := fmt.Errorf("invalid dirfd %d: %w", req.DirFD, errno)
			res.Errno = errno
			return res, h.deny(n.ID, errno, cause)
		}
		base, err = h.Kernel.DirFDBase(n.PID, req.DirFD)
		if err != nil {
			errno := errnoOrDefault(err, syscall.EACCES)
			res.Errno = errno
			return res, h.deny(n.ID, errno, err)
		}
	}

	resolved, err := ResolvePath(base, path)
	if err != nil {
		errno := errnoOrDefault(err, syscall.EINVAL)
		res.Errno = errno
		return res, h.deny(n.ID, errno, err)
	}
	res.ResolvedPath = resolved

	src, err := h.Kernel.OpenInView(view, resolved, req.Flags, req.Mode)
	if err != nil {
		errno := errnoOrDefault(err, syscall.EIO)
		res.Errno = errno
		return res, h.deny(n.ID, errno, err)
	}
	if src == nil {
		errno := syscall.EIO
		cause := errors.New("netsec: OpenInView returned a nil file")
		res.Errno = errno
		return res, h.deny(n.ID, errno, cause)
	}
	defer src.Close()

	// ADDFD with SECCOMP_ADDFD_FLAG_SEND both installs the descriptor and
	// answers the notification. The kernel checks the notification ID as part
	// of that atomic operation, so there is no window in which we could answer
	// a stale notification.
	targetFD, err := h.Kernel.SendFD(n.ID, src)
	if err != nil {
		if isNotificationGone(err) {
			return res, notificationGone("inject path result", err)
		}
		errno := errnoOrDefault(err, syscall.EIO)
		res.Errno = errno
		return res, h.deny(n.ID, errno, err)
	}
	res.TargetFD = targetFD
	return res, nil
}

// deny sends a negative response to the target. If the notification expired
// before the response, it returns ErrNotificationGone and does not pretend
// that the denial was delivered.
func (h *Handler) deny(id uint64, errno syscall.Errno, cause error) error {
	if errno == 0 {
		errno = syscall.EIO
	}
	if err := h.Kernel.Respond(id, 0, errno); err != nil {
		if isNotificationGone(err) {
			return notificationGone("send denial", err)
		}
		return fmt.Errorf("send denial for notification %d: %w", id, err)
	}
	return &DenialError{Errno: errno, Cause: cause}
}

func validatePath(path string) (syscall.Errno, error) {
	switch {
	case path == "":
		return syscall.ENOENT, fmt.Errorf("empty pathname: %w", syscall.ENOENT)
	case strings.IndexByte(path, 0) >= 0:
		return syscall.EINVAL, fmt.Errorf("pathname contains NUL: %w", syscall.EINVAL)
	case len(path) > MaxPathBytes:
		return syscall.ENAMETOOLONG, fmt.Errorf("pathname is %d bytes: %w", len(path), syscall.ENAMETOOLONG)
	default:
		return 0, nil
	}
}

func notificationGone(op string, err error) error {
	if err == nil {
		return fmt.Errorf("%s: %w", op, ErrNotificationGone)
	}
	return fmt.Errorf("%s: %w: %v", op, ErrNotificationGone, err)
}

func isNotificationGone(err error) bool {
	return err != nil && (errors.Is(err, ErrNotificationGone) ||
		errors.Is(err, syscall.ENOENT) ||
		errors.Is(err, syscall.ESRCH))
}

func errnoForRead(err error) syscall.Errno {
	var errno syscall.Errno
	if errors.As(err, &errno) && errno != 0 {
		return errno
	}
	return syscall.EFAULT
}

func errnoOrDefault(err error, fallback syscall.Errno) syscall.Errno {
	var errno syscall.Errno
	if errors.As(err, &errno) && errno != 0 {
		return errno
	}
	return fallback
}
