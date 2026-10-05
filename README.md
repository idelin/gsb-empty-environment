# dircopy

Directory copy tool (C++17) that preserves hard-link relationships *inside*
the copied tree without ever adopting same-inode objects that live outside
the source root.

## Build & test

```sh
make          # builds ./dircopy and ./test_dircopy
make check    # runs the test suite
```

## Usage

```sh
dircopy [--on-error=skip|abort] [--report FILE] SRC DST
```

`DST` must not exist; it is created. Exit status: `0` clean copy,
`1` fatal error, `2` finished with rejections (see the report).

## How it works

1. **Scan phase** (`scan_tree`): the source root is walked without
   following symlinks. Every entry's `(st_dev, st_ino)` identity is
   recorded, and every regular-file inode found *inside* the root is
   registered once. Hard links pointing outside the root are never
   followed, so outside objects never enter the inode set.
2. **Copy phase** (`copy_tree`): before each item is copied, its current
   `lstat` identity is compared against the scan record. A regular file
   whose inode was already copied becomes a `link(2)` hard link in the
   destination, reproducing the sharing relationship (verified by inode
   equality, not byte comparison). Content is compared by no one — only
   identities are.
3. **Symlinks** are copied as symlinks with their target string verbatim;
   dangling and absolute links are preserved, targets are never pulled in.
4. **TOCTOU guard**: if an object's identity changed between scan and copy
   (replaced file, swapped symlink, hard link to an outside inode moved
   in), the item is *rejected* and recorded in the report. Rejected
   directories reject their whole subtree. A file is also re-validated via
   `fstat` after `open(..., O_NOFOLLOW)` to close the lstat→open race.
5. **Failure handling**: `--on-error=skip` (default) rejects the affected
   item and continues; `--on-error=abort` stops at the first failure and
   removes the entire partially created destination tree. A file that
   fails mid-copy always has its partial destination unlinked. Directory
   permission bits are applied only after the whole tree is written, so
   cleanup never fights read-only directories.

## Tests (`tests/test_dircopy.cpp`)

- internal hard links stay linked in the destination (inode equality,
  `nlink == 3`, fresh inode distinct from the source's)
- same content in different inodes is *not* merged
- an inode that also exists outside the root is not adopted: the
  destination copy gets `nlink == 1` and the outside tree is not copied
- identity change between scan and copy (new file, or hard link to an
  outside object swapped in) → item rejected, listed in the report
- symlinks copied verbatim (relative, dangling, absolute-outside);
  symlink swapped after scan → rejected
- `--on-error=abort`: partial destination tree fully removed
- `--on-error=skip`: rest of the tree survives, no partial file left
- file and directory permission bits preserved
- cross-device copy: internal links still reproduced in the destination.
  Needs a second device; set `DIRCOPY_TEST_DEV2_DIR` to a directory on
  another filesystem, or let the test try to create a RAM disk
  (`hdiutil` on macOS, `tmpfs` on Linux). Skips gracefully otherwise.
