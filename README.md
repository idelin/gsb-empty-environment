# gsbdiff

Baseline-summary based document diff/patch in Go (standard library only).

## Supported serialization scope

The document model is JSON data: `null`, `bool`, `float64` numbers
(NaN/Inf rejected), `string`, arrays and objects with string keys.
The document root must be an object or an array. Go integer kinds are
normalized to `float64`; anything else (funcs, channels, structs, ...)
is rejected.

## Design

- **Baseline summary**: each diff carries `len + SHA-256` of the canonical
  baseline encoding. `Apply` refuses a missing baseline
  (`ErrBaselineRequired`) instead of guessing a default object, and refuses
  a baseline whose length matches but content differs
  (`ErrBaselineMismatch`).
- **Ops**: field changes (`OpSet` / `OpDel`) and array edits
  (keep/delete/insert script from common prefix/suffix). Deleting a key is
  distinct from setting it to `null`.
- **Atomic apply**: `Apply` never mutates the caller's baseline; it works
  on a deep copy and returns all-or-nothing.
- **Inverse diff**: `Apply` returns an inverse diff whose baseline summary
  is the result document; forward + inverse restores the original document
  byte-identically (identity convention: canonical encodings equal).
- **Safe decoding**: the binary wire format is plain length-prefixed data;
  decoding never executes host code, and every declared count/length is
  validated against the remaining input before any allocation.

## Usage

```go
d, _ := gsbdiff.Compute(oldDoc, newDoc)
wire, _ := gsbdiff.Encode(d)
d2, _ := gsbdiff.Decode(wire)
next, inv, err := gsbdiff.Apply(oldDoc, d2) // atomic; inv undoes it
restored, _, err := gsbdiff.Apply(next, inv) // == oldDoc
```

## Run

```sh
go test ./...   # tests
go run ./cmd/demo
```
