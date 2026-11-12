// Package netsec implements the supervisor side of a seccomp user-notification
// handler for openat(2).
//
// The security model is deliberately narrow:
//
//   - The pathname is copied out of the traced process exactly once. After
//     that copy, the supervisor never consults the process's memory again.
//   - SECCOMP_IOCTL_NOTIF_ID_VALID is checked immediately before and
//     immediately after the memory copy.
//   - Relative paths are resolved against the traced process's cwd; absolute
//     paths are resolved inside the traced process's root. Both are evaluated
//     with the process's mount-namespace view, not the supervisor's view.
//   - An allowed open is performed by the supervisor and the resulting file
//     descriptor is injected with SECCOMP_IOCTL_NOTIF_ADDFD. The kernel
//     response contains that descriptor. The supervisor never asks the kernel
//     to continue the original openat with SECCOMP_USER_NOTIF_FLAG_CONTINUE,
//     because the process could have changed the pathname since the copy.
//
// Use LinuxKernel on Linux and a fake Kernel implementation in tests.
package netsec
