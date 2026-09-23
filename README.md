# dictcomp

Dictionary compression over a finite sample set, Go standard library only.
No third-party compression libraries are used; the encoder, decoder, and
dictionary selection are all implemented here.

`dictcomp` selects a fixed dictionary of repeated substrings from a training
set, subject to a byte budget that **includes the serialized dictionary
overhead**, then encodes streams as literal runs and dictionary references.
Selection is deterministic and does not depend on the order samples are
supplied.

## Build and test

```sh
go build ./...
go test ./...
```

## Usage

```sh
# Select a dictionary from training samples; report train/valid ratios
# (dictionary overhead included in every reported number).
dictcomp train -mode heuristic -budget 512 -o dict.bin \
    -valid valid0.txt -valid valid1.txt train0.txt train1.txt

# Small inputs: exact mode enumerates every candidate subset and returns the
# provably optimal dictionary and encoding (within the candidate model).
dictcomp train -mode exact -budget 256 -minlen 3 -maxlen 6 tiny1.txt tiny2.txt

# Compress / decompress a single file. Decoder output is bounded by -maxout.
dictcomp encode -dict dict.bin -o log.dcs log.txt
dictcomp decode -dict dict.bin -maxout 1000000 -o log.out log.dcs

# Report compression of files against an existing dictionary.
dictcomp eval -dict dict.bin file...
```

## How gains are accounted

- A candidate phrase's standalone gain is `count*(len-2) - (len+1)`, where
  `count` is the maximum number of **non-overlapping** occurrences (leftmost
  greedy is optimal for equal-length intervals), so a phrase can never be
  credited for overlapping copies of itself.
- Dictionary selection and encoding are scored jointly: every candidate
  subset (exact mode) or the greedy set (heuristic mode) is evaluated by
  actually parsing the training set with that dictionary. Overlapping
  candidates therefore cannot double-count savings — the parser only ever
  selects compatible (non-overlapping) matches, and the reported train cost
  is the real encoded size.
- Exact mode enumerates all `2^n` candidate subsets (n bounded by `-maxexact`)
  and returns the minimum-cost dictionary; ties resolve deterministically.
- Heuristic mode adds candidates by standalone gain, prunes entries that do
  not pay for themselves in the joint parse, and falls back to an empty
  dictionary when compression loses. It reports a feasible result plus an
  **upper bound** on achievable savings (fractional-knapsack relaxation over
  per-phrase use limits, plus literal-run header slack). It never promises
  optimality.
- Train and validation sets are reported separately; every ratio includes
  the serialized dictionary size. If the dictionary is too large to pay for
  itself, the report says so plainly instead of hiding the overhead.

## Determinism

Samples are sorted by content before training, candidates are iterated in
sorted order, and all tie-breaks are fixed. Reordering the input files does
not change the selected dictionary (covered by tests).

## File formats (version 1)

All lengths are unsigned varints. Both formats carry explicit magic/version
bytes, lengths, and CRC-32 (IEEE) checksums.

Dictionary file (`DCD1`):

```
"DCD1" | varint numEntries | (varint len | entry bytes)... | crc32(all preceding)
```

Stream file (`DCS1`):

```
"DCS1" | dictCRC(4, BE) | varint origLen | varint numTokens | tokens... | crc32(decoded output)
```

Token: tag `0` = literal run (`varint length` 1..127 + raw bytes);
tag `1` = dictionary match (`varint entry index`).

The decoder is independent of the encoder and enforces: magic/version check,
dictionary binding via `dictCRC`, declared `origLen` and token count,
per-step output bounded by `-maxout`, final length equality, checksum
equality, and no trailing bytes.

## Cost model note

The exact-mode parser models the stream format precisely (literal runs are
capped at 127 bytes). The heuristic fast parser treats a literal run of any
length as one run; the encoder splits longer runs at 2 bytes per extra run,
and all user-reported sizes always come from the real encoder output, never
from the model.

## Layout

- `format.go` — dictionary/stream serialization, bounded decoder
- `parse.go` — exact and fast DP parsers (token cost model)
- `candidates.go` — repeated-substring candidate generation, non-overlap counts
- `select.go` — exact enumeration, greedy+prune heuristic, savings upper bound
- `main.go` — CLI (`train`, `encode`, `decode`, `eval`)
- `*_test.go` — unit tests, independent enumeration reference, end-to-end CLI tests
- `examples/` — sample training/validation logs used in the demo below
