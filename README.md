# snaprecv

Receives versioned snapshots delivered as unordered, hash-verified chunks.
Nothing becomes visible until every byte of a version is staged and verified;
installation atomically swaps the visible snapshot.

## Protocol

- Each `Chunk` carries a global `Version`, `Offset`, `TotalSize`, payload and
  the SHA-256 of the payload. Chunks may arrive in any order and may overlap;
  overlapping bytes must agree with already verified bytes (`ErrConflict`).
- Chunks with a bad hash (`ErrHashMismatch`), empty or out-of-bounds payload
  (`ErrInvalidChunk`), or a version that is not newer than the installed or
  staged snapshot (`ErrStaleVersion`) are rejected without touching state.
  Stale responses can therefore never overwrite a newer snapshot.
- A chunk for a newer version abandons the currently staged one.

## On-disk layout

```
staging/data             staged bytes for the in-progress version
staging/manifest.json    fsynced record of verified byte intervals
snapshots/v<N>/data      fully verified snapshots
current -> snapshots/v<N>   atomically swapped visible state
```

Write protocol per chunk: pwrite payload, fsync the data file, then update
the manifest via tmp-file + rename + fsync. Install renames the staging dir
into `snapshots/v<N>` and swaps the `current` symlink with a rename.

## Recovery

`Open` replays durable state after a crash:

- verified staged chunks are reused; only missing ranges need redelivery;
- a fully verified staging area (crash before install) is installed;
- a renamed-but-not-yet-linked snapshot (crash mid-install) is linked in;
- staging for versions that can no longer be installed is discarded.

## Fault injection

`Options.Hook` fires at cut points (`HookPoint`): `chunk.data.written`,
`chunk.data.synced`, `manifest.synced`, `install.renamed`,
`install.symlinked`, `install.cleaned`. Tests crash at each point and assert
the visible state is always either the intact old or the complete new
snapshot. `TestChunkLevelModel` differentially checks the receiver against a
byte-level reference model over randomized chunk streams (overlaps,
conflicts, corrupt hashes, tail chunks, stale versions, crashes).

## Test

```
go test ./...
```
