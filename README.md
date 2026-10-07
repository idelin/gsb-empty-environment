# archrestore

Go model of archive restoration with staged hard-link resolution.

## Semantics

- **Forward references**: a hard-link entry may cite a target path that only
  appears later in the same batch. References are staged and resolved at
  finalize time.
- **Shared inodes**: resolved hard links to regular files share one `Inode`
  (with a link count); chains of links (`a -> b -> file`) collapse onto it.
- **Symlinks stay independent**: a hard link to a symlink materializes as an
  independent symlink node carrying a copy of the link text — symlinks never
  share inodes.
- **Rejection**: hard-link cycles (including self-links), links to
  directories (explicit or implicit), dangling targets, duplicate paths, and
  parent-not-a-directory conflicts all fail the batch.
- **Atomic publish**: `Restorer.Apply` builds on a copy-on-write staged
  tree; any failure discards it wholesale, so a half-built tree is never
  published and previously committed state (including inode link counts) is
  untouched.

## Layout

- `restore/restore.go` — entries, nodes, inodes, committed tree, `Restorer`
- `restore/stage.go` — copy-on-write staging area and rollback boundary
- `restore/graph.go` — inode reference graph: three-color DFS over staged
  link edges detects cycles and resolves terminals
- `restore/restore_test.go` — forward refs, conflicts, rejection, rollback

## Test

```sh
go test ./...
```
