# chunked-upload-progress

TypeScript model for reliable chunked-upload progress.

## Model

`UploadProgressModel` (src/upload-progress.ts) separates two kinds of progress:

- **Confirmed bytes** come from server completion receipts (`confirmChunk`).
  They are the reliable progress and never roll back. Duplicate receipts for
  the same chunk are idempotent, and receipt bytes are clamped to the chunk's
  remaining size, so the confirmed total can never exceed `totalBytes`.
- **Tentative bytes** come from in-flight transfer reports (`reportProgress`).
  They are a temporary display value only: per chunk the model keeps the
  maximum report (out-of-order reports are safe), clamps it to the chunk
  size, and drops it once the chunk is confirmed.

Retries, staleness, and pause:

- `beginAttempt()` starts a new attempt and clears tentative bytes. A retry
  (`failAttempt` followed by `beginAttempt`) rolls back tentative progress
  but never confirmed bytes.
- Events carrying a stale `attemptId` are ignored, so late reports from an
  old attempt cannot corrupt the current one.
- `pause()` retains the confirmed set and clears tentative bytes; events are
  ignored while paused. `resume()` starts a fresh attempt.

`snapshot()` exposes `confirmedBytes`, `tentativeBytes`, and
`transferredBytes` (confirmed + tentative, capped at `totalBytes`), plus the
sorted `confirmedChunks` and a `completed` flag.

## Develop

```sh
npm install
npm test
```

The test suite (test/upload-progress.test.ts) drives out-of-order progress
reports and completion receipts, retries, stale attempts, and pause/resume,
asserting after every step that byte totals never exceed `totalBytes` and
that duplicate chunks are never double-counted.
