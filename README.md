# streammux

A minimal byte-stream multiplexer in Go (standard library only). One ordered
byte channel carries many independent logical streams, each with its own flow
control window, close state, and error state.

## Wire format

```
+-----------------------------------+---
|  Length (24 bits)                 |   payload length, header excluded
+------+--------+-------------------+---
| Type | Flags  | Stream ID (32)    |   1 + 1 + 4 bytes
+------+--------+-------------------+---
|  Payload ...
+---------------------------------------
```

- `DATA` (flags: `END_STREAM`) — stream payload; consumes stream + connection windows.
- `WINDOW_UPDATE` — 8-byte **cumulative consumed-bytes watermark** (stream 0 =
  connection level). Absolute watermarks make replays idempotent: a duplicate
  or stale update is ignored and can never extend the send window twice.
- `RST_STREAM` — 4-byte error code; tears down one stream, connection survives.

## Design notes

- **Parsing**: `Parser` accepts arbitrarily chunked input. `ErrNeedMore`
  (incomplete) is strictly separated from `*ProtocolError` (invalid). The
  payload-length quota is enforced right after the 9-byte header, *before*
  any payload allocation.
- **Scheduling**: a background writer emits at most `WriteBudget` bytes per
  cycle; control frames drain first within that budget, then DATA frames
  round-robin over streams that hold both stream-level and connection-level
  credit. One stream's exhausted window never blocks another stream.
- **Half-close / late data**: `CloseWrite` sends `END_STREAM` after queued
  data drains. DATA received after remote half-close is a stream error
  (`RST_STREAM(STREAM_CLOSED)`); DATA for a fully closed stream is reported
  via `OnLateData` and likewise rejected. Writes after `CloseWrite` fail with
  `ErrStreamClosed`.

## Layout

- `mux/` — the library (`frame.go`, `parser.go`, `conn.go`, `stream.go`, `errors.go`)
- `mux/mux_test.go` — `go test` suite (unit + `net.Pipe` integration, race-clean)
- `cmd/demo/` — runnable walkthrough of the protocol behaviours

## Run

```sh
go test ./...          # or: go test ./mux/ -race -v
go run ./cmd/demo
```
