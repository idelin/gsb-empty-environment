# tlv

Streaming Type-Length-Value codec in Go (standard library only).

Wire format per field: 2-byte big-endian type, 4-byte big-endian length,
then the value. Types with bit `0x8000` set are containers whose value is
a nested TLV sequence.

## Guarantees

- **Unknown-type passthrough**: unknown fields keep raw value bytes;
  decode + re-encode is byte-identical.
- **Nesting & empty containers**: containers nest arbitrarily; a
  zero-length container is legal.
- **Length coverage**: a child may not claim bytes past its parent's
  boundary; violations report the child's absolute stream offset.
- **Quota before allocation**: any single value length is capped
  (`Parser.MaxValueLen`, default 16 MiB) and checked as soon as the
  header is complete, before any length-driven allocation.
- **Chunked input**: `Parser.Feed` accepts arbitrary chunking; truncated
  input is reported as `ErrNeedMoreData` (via `errors.Is`), distinct
  from malformed data.
- **Duplicate order**: field order, including duplicate types, is
  preserved on decode; `EncodeCanonical` sorts by wire type with a
  stable sort, recursively inside containers.

## Usage

```go
fields, err := tlv.Parse(wire)        // one-shot
p := tlv.NewParser()                  // streaming
err = p.Feed(chunk)                   // any chunk size
fields, err = p.Finalize()            // ErrNeedMoreData if truncated
out := tlv.Encode(fields)             // preserves order
canon := tlv.EncodeCanonical(fields)  // canonical form
```

## Run

```sh
go test ./...
go run ./cmd/tlvdemo
```
