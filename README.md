# Rolling-Code Access Receiver

Go firmware service for a rolling-code door receiver. Guarantees that every
valid code triggers the unlock callback **at most once**, even across power
loss.

## Design

- **Sliding window** (`receiver/receiver.go`): per device, counters
  `lastAccepted+1 .. lastAccepted+Window` are accepted and slide the
  window; anything at or below `lastAccepted` is a replay and rejected.
- **Dual-code resync**: a counter beyond the window but inside
  `ResyncWindow` only arms a resync; the next counter must be exactly
  consecutive to unlock. The armed state is WAL-persisted, so power loss
  between the two codes is safe.
- **Write-ahead log** (`receiver/wal.go`): every state change is appended
  with a CRC and fsynced before it takes effect. The `accept` record is
  durable *before* the unlock callback runs; a `done` record follows a
  successful callback. Corrupt tails from torn writes are truncated on
  recovery.
- **Idempotent recovery**: on open, the WAL is replayed; any `accept`
  without a matching `done` is re-issued to the door controller with the
  same operation number (`<deviceID>:<counter>`), so the controller
  deduplicates and the door physically actuates once.

## Layout

- `receiver/` — library: frames (HMAC-SHA256 tags), sliding window, WAL, recovery.
- `cmd/rcvd/` — service: hex frames on stdin, verdicts on stdout; keys via
  `RCVD_KEYS="<deviceIDhex>=<keyhex>,..."`.

## Tests

```
go test -race ./...
```

Covers crash after accept / before done, replay across restarts,
cross-window dual-code resync (including power loss mid-resync), and
two-reader races on the same code.
