# strided_tensor

Pure-stdlib (Python 3.11) strided tensor views over a shared flat `list`
storage. No tensor/autograd dependencies.

## Features

- Views as `(shape, strides, offset)` triples: `transpose`/`permute`/`T`,
  negative-stride slices (`t[::-1]`), non-contiguous slices (`t[1:9:3]`),
  zero-stride `broadcast_to`, empty dims.
- Bounds checking uses the view's real address extent, never `numel` alone.
- Overlap analysis (`internal_overlap`, `views_overlap`) returns a
  tri-state `Overlap.NO / YES / UNKNOWN`:
  1. fast sufficient conditions (disjoint offset intervals, gcd lattice
     test, nested-stride injectivity test);
  2. exact finite solver: address-set enumeration for views within
     `EXACT_ENUM_LIMIT` elements;
  3. otherwise conservative `UNKNOWN`, treated as unsafe.
- Alias-safe in-place ops: `add_` raises `OverlapError` on possible
  aliasing (no indeterminate overwrite); `copy_` uses snapshot semantics
  on overlap; `borrowed()` locks a storage against writes for the borrow
  lifetime (`BorrowError`).
- `reshape` requires contiguity and raises `NonContiguousError` otherwise;
  use `.contiguous().reshape(...)` to copy explicitly.

## Files

- `strided_tensor.py` — library
- `test_strided_tensor.py` — 29 unittest cases, incl. brute-force
  address-set verification of every overlap predicate on 300+ random
  small views, and independent numerical references
- `demo.py` — runnable tour

## Run

```sh
python3.11 -m unittest test_strided_tensor -v
python3.11 demo.py
```
