// Package gsbsecure is an importable in-memory model and verification toolkit
// for host-side container runtime path handling.
//
// The packages compose as follows:
//
//   - vfs: concurrency-safe directory graph with files, directories, static
//     absolute/relative symlinks and procfs magic links, plus atomic
//     rename/renameat2-exchange semantics.
//   - userns: user-namespace hierarchy with nested UID/GID id-mappings,
//     credentials, capability gates and DAC checks.
//   - mount: mount-namespace trees carrying root inodes, mount points,
//     MS_RDONLY, MNT_LOCKED, MS_SHARED/MS_SLAVE/MS_PRIVATE propagation and
//     peer groups, with the security adjudication around unmount and
//     read-only flag changes.
//   - resolve: an openat2(RESOLVE_IN_ROOT | RESOLVE_NO_MAGICLINKS) style
//     component walker (40 symlink hop budget, absolute symlink
//     re-anchoring, root clamping, magic-link refusal, final containment)
//     alongside a deliberately vulnerable string-prefix control resolver
//     and resumable slow walks for TOCTOU interleaving.
//   - adversarial: a bounded six-step exhaustive searcher that enumerates
//     symlink/rename/exchange/open sequences and victim/attacker
//     interleavings, demonstrating escapes on the vulnerable resolver while
//     asserting zero escapes from the secure resolver.
package gsbsecure
