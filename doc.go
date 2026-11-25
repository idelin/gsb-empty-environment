// Package uffd implements the authorization rules a sandbox gateway applies
// around userfaultfd creation and UFFDIO_* operations.
//
// The package is deliberately policy-level rather than a raw syscall wrapper.
// A gateway supplies the kernel sysctl value, the caller's credentials and
// its current virtual-memory map; the package decides whether an operation is
// allowed and, for a descriptor, keeps the registration state that makes
// later decisions possible.
//
// Caller values (process identity and capabilities) must come from the
// gateway's own credential accounting, never from the sandboxed process.
//
// The central invariants are:
//
//   - An unprivileged caller (no CAP_SYS_PTRACE) is refused a userfaultfd
//     unless it requested UFFD_USER_MODE_ONLY, even when
//     vm.unprivileged_userfaultfd is enabled.
//   - Registration is confined to the creator's own anonymous private
//     mappings, is page aligned, cannot overflow and cannot span an unmapped
//     hole.
//   - Write-protect and minor registrations need an explicit additional
//     grant. They are not implied by CAP_SYS_PTRACE.
//   - Partial overlaps with existing registrations are refused.
//   - Passing a descriptor with SCM_RIGHTS shares the same registration
//     state. The receiver may use ranges that were already registered, but
//     may not register, expand or unregister them, and needs its own grant
//     before it can write-protect the creator's mappings.
package uffd
