# csvq

csvq is a Go CSV parsing and import library built around positional
column identity, row quarantine, verifiable recovery, and versioned
schema evolution.

## Parsing

- Quoted fields may contain commas and newlines; `""` is an escaped
  quote. Newlines inside quotes never split or repair a row.
- Header column names may repeat. Column identity is the position:
  field `i` of every record belongs to header column `i`.
- Records with fewer fields than the header are padded with *missing*
  fields (`Field.Present == false`), which are distinct from
  present-but-empty fields (`Field.Empty == true`).
- Records with more fields than the header, or with malformed quoting,
  are quarantined whole (`QuarantinedRow` keeps the raw source span).
  Quarantine never shifts the column positions of later records.
- Every record and quarantined row carries its original byte range
  (`Offset`, `EndOffset`).

## Verification

`Verify(src, table, chunkSize)` enumerates chunks over the parsed items,
checks that the chunk byte ranges tile the source contiguously,
independently re-parses every chunk, and independently re-parses sampled
records (all quarantined rows included) from their original byte ranges
to confirm both the ranges and the quarantine recovery.

## Import

- Column types are inferred per batch (`Int` when all present non-empty
  values parse as integers, else `Text`).
- `Store.ImportBatch` is atomic: on error the store is untouched.
- When a later batch widens an integer column to text, a new schema
  version is appended. `Store.View(version)` renders all rows under any
  historical version: old batches still render integers under the old
  version, and render their original raw text (leading zeros preserved)
  under the widened version, because cells always keep the raw text.

## Recovery manifest

`BuildManifest(src, store)` binds the SHA-256 source digest, the full
schema version history, every batch, and every quarantined row (with its
raw-span digest). `VerifyRecovery(src, manifest)` performs a full
import — re-parse plus batch replay — and requires the rebuilt manifest
to equal the given one.
