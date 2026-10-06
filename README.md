# keydir

Go prototype of an append-only log store with an in-memory key directory
(bitcask-style), focused on crash consistency.

## Design

- **Data log is the source of truth.** `Put` appends a record
  (`crc32 | version | keyLen | valLen | key | value`) and syncs it
  *before* updating the in-memory directory. A crash may complete only
  one of the two steps; recovery always replays the log.
- **Rebuild ignores incomplete tails.** Replay stops at the first record
  that fails length or CRC validation and truncates the active file's
  torn tail so later appends stay reachable.
- **Directory hits are validated.** `Get` resolves the offset from the
  directory, then checks the record's CRC, key and version. A stale
  entry (e.g. pointing into a file reclaimed by compaction) returns
  `ErrStaleDir` instead of misreading.
- **Directory checkpoint is only a hint.** `Checkpoint` persists the
  directory atomically (tmp + rename) with a log watermark. A corrupt or
  missing checkpoint falls back to a full log scan.
- **Compaction copies live records verbatim** (versions preserved) into
  a new file, switches the directory in one shot, then reclaims old
  files. A crash mid-switch is safe: replay merges old and new files by
  version.

## Injectable file model

`FS`/`File` (`fs.go`) abstract all I/O. `osFS` is the production
backend; the tests use `memFS` (`memfs_test.go`), which models durable
vs. unflushed bytes and supports `Crash()`, `Corrupt()` and raw torn
appends. Scenarios covered in `store_test.go`:

- incomplete tail and bit-flipped record ignored on rebuild
- crash between data append and directory checkpoint
- corrupt directory checkpoint falls back to log replay
- compaction switch invalidates old offsets (no misreads)
- directory-hit key/version validation
- crash during compaction switch

## Run

```sh
go test -v ./...
```
