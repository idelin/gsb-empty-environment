# userfaultfd authorization for the Go sandbox gateway

`uffd` is a policy package for the gateway's `userfaultfd()` and `UFFDIO_*`
interception points. It does not issue syscalls; it decides whether a proposed
syscall/ioctl may proceed and keeps the registration state needed for later
decisions, including after a descriptor is passed with `SCM_RIGHTS`.

## Policy

- Without `CAP_SYS_PTRACE`, creation requires `UFFD_USER_MODE_ONLY`, whether
  or not `vm.unprivileged_userfaultfd` is enabled. A zero sysctl value still
  denies unprivileged creation.
- `O_CLOEXEC` and `O_NONBLOCK` are accepted but do not weaken the rule. Other
  unknown creation flags are rejected.
- `UFFDIO_REGISTER` ranges must be non-zero, page aligned, overflow-free and
  fully covered by the caller's own anonymous private mappings. Gaps are
  rejected; ranges crossing file-backed/shared mappings are rejected.
- `UFFDIO_REGISTER_MODE_WP` and `UFFDIO_REGISTER_MODE_MINOR` need an explicit
  extra grant (`PermWriteProtect` / `PermMinor`), at policy level or on the
  caller's `Capabilities`. `CAP_SYS_PTRACE` alone does not grant them.
- Partial overlap with a registered range is rejected. An exact duplicate
  with the same mode is idempotent.
- Registrations may not span an unregistered gap when copied to, continued,
  woken or unregistered.
- After `SCM_RIGHTS`, the receiver shares the exact registration state. It can
  service already registered ranges, but only the creator can register,
  extend or unregister ranges. A receiver needs its own WP grant before it can
  write-protect the creator's mappings.

## Example

```go
policy := uffd.NewAuthorizer(uffd.UserfaultfdEnabled)

creator := uffd.Caller{Process: 100}
fd, err := policy.Create(creator, uffd.UserModeOnly|uffd.CloseOnExec)

err = fd.Register(creator, uffd.RegisterRequest{
	Start:  0x10000,
	Length: 0x2000,
	Mode:   uffd.ModeMissing,
	Mappings: []uffd.Mapping{{
		Start: 0x10000, End: 0x12000,
		Kind: uffd.AnonymousPrivate, Owner: 100,
	}},
})
```

See `descriptor.go` for the complete ioctl surface and `uffd_test.go` for the
abuse cases.
