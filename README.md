# Dynamic Build Kernel

A small dynamic dependency build kernel implemented with only the Python 3.11 standard library. It runs injectable task functions against a local synthetic workspace and does not invoke external build tools.

## Core guarantees

- Tasks record successful file reads, reads of missing paths, and directory enumerations.
- Each observed dependency includes a version:
  - files use a SHA-256 content digest;
  - missing paths use a sentinel version and invalidate when the path appears;
  - directories use a fingerprint over ordered `(name, entry-type)` pairs, so content changes inside a watched directory do not invalidate unless its declared name/type view changes.
- Outputs are staged as content-addressed objects and published through an atomic symlink replacement.
- Publication happens only when every recorded dependency still matches the current snapshot; otherwise the attempt is discarded and the node is rescheduled.
- Newly discovered graph edges and file dependencies become effective in the same graph/cache transaction. A newly discovered cycle returns an ordered cycle (`a -> b -> a`) rather than waiting for a dead dependency.
- Cache keys include content summaries, tool identifier, ordered arguments, environment whitelist values, the node identity, and a signature of its dependency edge set.
- Path aliases and node output paths are reduced to logical file identities.
- Multiple requests share an in-flight node. Canceling one subscriber removes only that request; the build continues while another subscriber exists.
- Per-node attempt generations prevent a delayed old attempt from replacing a newer result.

## Commit points and recovery

The local state directory is `.buildkernel/`:

- `wal.jsonl` contains the commit points:
  1. dependency graph update;
  2. cache record;
  3. artifact publication;
- `objects/` contains immutable content-addressed staged outputs;
- `tmp/` contains only unpublished temporary artifacts and is cleaned on startup;
- `events.jsonl` records nodes that truly started and finished.

Opening a `BuildKernel` replays the WAL. A cache entry is retained only if its object exists, the published symlink points at that object, and all dependency versions and cache identity inputs still match. A cache record without a published artifact is discarded and rebuilt; a published artifact after a crash before the publish log can be committed during recovery if the corresponding cache entry is valid.

## Controlled tester

Run:

```sh
python -m unittest -v tests.py
```

`dbk.tester.InterleaveTester` enumerates small interleavings around read, input change, dependency discovery, commit, and cancellation. The cases include:

- negative dependency invalidation after a missing path appears;
- dynamic dependency discovery and ordered dynamic cycle evidence;
- shared task cancellation that does not cancel another subscriber;
- crashes before and after graph/cache/publish commit points;
- unrelated file modifications that do not invalidate a node;
- old-generation attempts that cannot overwrite newer outputs.

`dbk.reference.ReferenceExecutor` copies the current snapshot, removes outputs, performs a full rebuild without using the old cache, and supplies reference outputs. Tests compare final outputs against that executor and inspect the durable execution event log to verify which nodes actually reran and that invalidation was scoped correctly.
