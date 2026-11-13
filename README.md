# netsec tensor container

Strict parser for the task's SAFETENSORS-style container:

- little-endian `uint64` metadata length in the first 8 bytes;
- one UTF-8 JSON object immediately after the header;
- binary data immediately after the metadata;
- tensor entries with `dtype`, `shape`, and `data_offsets`.

The parser rejects duplicate object keys, floating-point and exponent number
syntax, integers outside `uint64`, malformed shapes, overflowing element or
byte counts, out-of-file ranges, and every pair of overlapping non-empty tensor
ranges.

## Host API

```go
model, err := netsec.Open("/models/checkpoint.bin")
if err != nil {
    return err
}
defer model.Close()

tensor, ok := model.Lookup("weight")
if !ok {
    return errors.New("missing weight")
}

var header [16]byte
if _, err := tensor.ReadAt(header[:], 0); err != nil {
    return err
}
```

`Open` opens the path exactly once. Tensor views do not reopen it. Every read
checks the descriptor size/modification time and the path's file identity.
Replacing, truncating, or modifying the file after a successful parse causes
later reads to fail with `ErrFileChanged`. `Close` closes the descriptor and
later reads fail with `ErrClosed`.

For a sparse, in-memory, or otherwise custom `io.ReaderAt`, use:

```go
model, err := netsec.Parse(reader, size)
```

`Parse` automatically binds to `Generation() uint64`, `Generation() int64`, or
`Snapshot() uint64` when present, and checks `Size()` when present. The
`WithGeneration` and `WithGuard` options can supply equivalent checks for
other implementations.

## Manifest

Tensor names are sorted by their UTF-8 bytes. The metadata is re-encoded in
this exact compact form:

```json
{"a":{"dtype":"F32","shape":[2],"data_offsets":[0,8]},"z":{"dtype":"U8","shape":[3],"data_offsets":[8,11]}}
```

Field order inside each tensor is `dtype`, `shape`, `data_offsets`. The
manifest is SHA-256 over those canonical bytes and is available as
`Model.Manifest` or `Model.ManifestHex()`.

All validation finishes before any `Tensor.Data`/`io.ReaderAt` view is
returned. A view is limited to its tensor range and is read-only; it never
exposes the backing file or a writable byte slice.
