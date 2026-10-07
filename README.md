# vfs

In-memory virtual directory tree with Linux-`namei`-style path resolution.

## Resolution rules

- Separator is `/`; paths are resolved against a single virtual root (a jail).
- Dot segments are eliminated during the walk: `.` is skipped, `..` climbs to
  the physical parent inode. Any `..` that would cross the root is rejected
  with `ErrEscapesRoot` — including ones hidden inside symlink targets.
- Symlinks are expanded step by step, keyed on directory **inode identity**,
  never on string prefixes. Absolute targets restart at the virtual root, so
  links cannot escape the jail. At most `MaxSymlinkHops` (40) expansions per
  resolution; more yields `ErrTooManyLinks` (covers link loops).
- Each component lookup re-reads the parent directory's child mapping under a
  lock, so a concurrent `Replace` (rename/unlink race) only affects lookups
  that have not happened yet; a walk stays pinned to inodes it already holds.

## API sketch

```go
fs := vfs.New()
fs.MkdirAll("/a/b")
fs.AddFile("/a/b/f")
fs.AddSymlink("/link", "/a/b")   // absolute target, jailed
n, err := fs.Resolve("/link/f")  // -> inode of /a/b/f
```

`Replace(path, kind, target)` atomically swaps a node and exists to model
replacement races. `FS.Path(n)` reconstructs a node's absolute path from
parent pointers.

## Tests

`go test -race ./...` covers dot-segment handling, escape rejection, in-root
symlink jumps, link loops, the 40-hop limit, and deterministic + concurrent
replacement races.
