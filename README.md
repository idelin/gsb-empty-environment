# chunked-upload-progress

TypeScript model for chunked-upload progress with two kinds of values:

- **Confirmed bytes (reliable progress)** — advance only when a chunk's
  completion receipt arrives. Never roll back; each chunk counts at most once
  (duplicate receipts are ignored).
- **Temporary bytes (transfer progress)** — optimistic in-flight reports.
  A retry (`rollbackAttempt` / `beginAttempt`) rolls them back, never the
  confirmed values.

Every event is tagged with an `attemptId`. Events from old attempts are
ignored, so late arrivals from failed or paused attempts cannot corrupt
state. `pause()` preserves the confirmed set and discards temporary values;
`resume()` returns a fresh attempt id.

Invariants (verified by tests with out-of-order reports/receipts):

- `confirmedBytes <= totalBytes`
- `confirmedBytes + temporaryBytes <= totalBytes` (display ratio capped at 1)
- duplicate chunk confirmations never double-count

## API

```ts
const t = new ChunkedUploadProgress(totalBytes, chunkSize);
const attempt = t.beginAttempt();          // or t.resume()
t.reportTransfer(i, attempt, bytesSent);   // temporary, max-wins, capped
t.confirmChunk(i, attempt);                // reliable, idempotent
t.rollbackAttempt(attempt);                // drop temp values of attempt
t.pause();                                 // keep confirmed, drop temp
t.snapshot();                              // { confirmedBytes, temporaryBytes, ... }
```

## Scripts

- `npm run build` — compile to `dist/`
- `npm test` — compile and run the test suite
