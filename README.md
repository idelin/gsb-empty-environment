# User Namespace & Capability Security Audit Library

Embeddable, zero-dependency C++17 library that validates nested Linux
user-namespace ID maps and process capability sets for a multi-tenant
container host. It rejects the configurations that let an unprivileged
container escalate to host root:

- tenant IDs (including transitively nested ones) mapping to host UID/GID 0
  or a protected system ID range in `init_user_ns`;
- 32-bit wrap-around, overlapping, or out-of-parent-scope `uid_map` /
  `gid_map` entries;
- unprivileged `gid_map` writes without `setgroups=deny` (CVE-2014-8989
  supplementary-group stripping);
- capability-set invariant violations, `CAP_SYS_ADMIN` retained in
  `init_user_ns`, missing `PR_SET_NO_NEW_PRIVS`, and illegal `execve`
  capability transitions (SUID / file-capability escalation).

## Layout

- `include/userns_security.hpp` — public API, zero includes beyond the
  standard library.
- `src/userns_security.cpp` — audit engine (can be compiled directly into a
  host binary; a static library target is also provided).
- `tests/test_userns_security.cpp` — 17 self-contained test groups, 54
  assertions, no test framework dependency.
- `examples/embed_demo.cpp` — minimal embedding example.

## Build & Test

Strict flags are always on: `-Wall -Wextra -Wconversion -Werror` (the build
also adds `-Wpedantic -Wshadow -Wsign-conversion`).

```sh
make test      # build and run the test suite
make           # static object + embedding demo in build/
```

Or with CMake:

```sh
cmake -S . -B build
cmake --build build
ctest --test-dir build --output-on-failure
```

## Model

- `UserNsAuditor` owns a tree of `UserNsNode`; node `0` is the implicit
  `init_user_ns`. Each node stores its parent, whether it was created by an
  unprivileged `clone(CLONE_NEWUSER)`, its `setgroups` state, and its
  `uid_map` / `gid_map` entries `(ns_id, lower_id, count)` (max 340 lines).
- All interval arithmetic is 64-bit half-open (`IdRange{first, last}`), so a
  range ending exactly at `UINT32_MAX + 1` is representable and true
  wrap-around is detected precisely.
- `translate_to_parent` / `translate_to_init` walk the ancestor chain and
  map a tenant ID level by level into `init_user_ns`.

## Audit rules

1. **Maps (`audit_maps`)** — per `uid_map`/`gid_map`:
   - reject more than 340 entries and any `count == 0`;
   - reject 32-bit wrap-around of either the `ns_id` or `lower_id` range;
   - reject overlap among in-namespace (`ns_id`) ranges and among parent
     (`lower_id`) ranges independently;
   - every `lower_id` range must be fully contained in the parent
     namespace's already-mapped ranges (any range is valid under
     `init_user_ns`);
   - every mapped tenant ID is translated up the full ancestor chain and
     rejected if its host ID hits `0` or any configured protected range
     (`SecurityPolicy::protected_uid_ranges` / `protected_gid_ranges`,
     default `[0, 1000)`).
2. **setgroups gate (`audit_setgroups`)** — an unprivileged, non-init
   namespace that wrote a `gid_map` while `setgroups != deny` is reported as
   `SetgroupsRequired` (CVE-2014-8989). Privileged creators and empty maps
   are exempt, matching the kernel rule.
3. **Credentials & transitions**
   - `audit_credentials` / `audit_enter_namespace` check
     `CapEff ⊆ CapPrm`, `CapPrm ⊆ CapBnd`, `CapAmb ⊆ CapPrm ∩ CapInh`,
     reject unknown capability bits, and for unprivileged tenants reject
     `CAP_SYS_ADMIN` in `cap_eff_in_init` and missing `no_new_privs`.
     An unprivileged ancestor taints every nested descendant.
   - `audit_execve` enforces that the bounding set never grows, that
     ambient/inheritable/file caps stay inside `CapBnd`, and recomputes the
     post-`execve` permitted/effective sets from the
     `(P_inh & X_inh) | (X_perm & P_bnd) | P_amb` rule (or, without file
     capabilities, `CapPrm == CapEff == CapAmb`), flagging any SUID-style
     privilege gain as `ExecResultMismatch`.

Every check returns an `AuditResult` containing typed `Violation` codes with
human-readable details; `audit_all()` runs the map and setgroups checks over
the whole tree. See `examples/embed_demo.cpp` for end-to-end usage.
