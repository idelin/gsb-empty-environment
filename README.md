# dictmerge

Merge record batches that were dictionary-encoded with **different local
dictionaries**, remapping codes while preserving null bitmaps.  Pure
Python 3.11 standard library; tests use `unittest`.

## Layout

- `dictmerge/dictionary.py` — versioned local dictionary: stable term
  identity (`term_id`), retraction, aliases (same term, different codes),
  pluggable hash so collisions can be forced.
- `dictmerge/batch.py` — typed batches (`int` exact, `float` with NaN as
  a value, `str` dictionary-encoded) with explicit null bitmaps.  Empty
  string is a term; null lives only in the bitmap.
- `dictmerge/merger.py` — resumable merge engine: memory-budget-limited
  term collection with sorted-run spilling, deterministic (sorted) global
  dictionary, per-batch remap tables (unreferenced terms reclaimed as
  `-1`), row-buffer flushing, cancellation via journal, atomic publish.
- `dictmerge/store.py` — versioned on-disk store; a version becomes
  visible only through an atomic `CURRENT` flip, so old batches keep
  decoding against old dictionary meanings.
- `demo.py` — runnable sample (includes cancel/resume and accounting).

## Guarantees

- Codes from different local dictionaries are never conflated
  (`[a,b]` vs `[b,a]`: code 0 differs).
- Merge-then-decode equals decode-then-concatenate, under random
  chunking, cancellation/resume, and spilling.
- Invalid codes raise `MergeError` carrying batch index and row number.
- Publishing a dictionary and its re-encoded batches is atomic
  (write version dir, fsync, atomically replace `CURRENT`).
- Byte accounting for the physical dictionary, remap tables, spill
  files, and output batches is exposed in `ACCOUNTING.json`.

## Run

```sh
python3.11 demo.py
python3.11 -m unittest discover -s tests -v
```
