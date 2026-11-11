# safezip

`safezip` reads individual ZIP members from object storage through ranged
`ReadAt` calls, without downloading the whole object, and only hands member
bytes to upper layers after full verification.

## Security guarantees

- **Header binding.** Every central-directory entry is rebound to its local
  file header. Name, compression method, flags and the exact payload range
  must agree; ZIP64 extra fields on both sides must match.
- **ZIP64.** 64-bit sizes and offsets are parsed explicitly from both the
  ZIP64 end-of-central-directory chain and the per-header extra fields.
- **Data descriptors.** Streaming members (`general purpose bit 3`) have
  their trailing descriptor read as a separate range and cross-checked
  against the central directory, including the 64-bit form.
- **Region disjointness.** Local headers, payloads and data descriptors are
  claimed as byte regions that must be mutually disjoint and stay clear of
  the central directory.
- **Pseudo-directory and overlong claims.** Entries that look like
  directories but declare bytes are refused, as are lengths past the object
  end or beyond configured `Limits`.
- **Commit-after-verify.** Decompressed bytes are buffered per read and only
  committed to a `Sink`/cache after the whole member is consumed and its
  CRC-32 matches. A failed, truncated or corrupt read never reaches the
  cache. `FileCache` writes to a temp file and renames atomically.

## Usage

```go
type Store interface {
    io.ReaderAt
    Size() int64
}

a, err := safezip.Open(store, safezip.Config{
    ArchiveID: "bucket/object-key",
    Limits:    safezip.DefaultLimits(),
})
if err != nil { /* ErrFormat, ErrBinding, ... */ }

m, ok := a.Lookup("path/to/member.txt")
if !ok { /* missing member */ }

cache := &safezip.FileCache{Dir: "/var/cache/zip"}
if _, err := a.CacheMember(m, cache); err != nil {
    // ErrCRC means the bytes were verified bad and nothing was committed.
}
```

For streaming use, `Archive.OpenMember` returns an `io.ReadCloser` whose
`Close` returns `nil` only when the complete member was read and the CRC
matches; bytes read before a successful `Close` are untrusted.

Only the store (method 0) and deflate (method 8) compression methods are
accepted. Spanned/multi-disk archives and unknown critical extra fields are
rejected.
