# Gateway Object Reassembly (`gsb::reassembly`)

A C++20 importable reassembly component for a gateway that receives immutable
fragment records from multiple authorized senders. It decides whether an
object version is deliverable, retains evidence for every signed-fragment
inconsistency, and returns the minimum certificate sufficient to verify the
decision.

## Layout

- `include/gsb/reassembly.hpp` — full implementation, usable as a normal header.
- `src/gsb/reassembly.cppm` — named module `gsb.reassembly` that re-exports
  the header as a C++20 header unit, so consumers can `import gsb.reassembly;`.
- `tests/` — scenario tests plus an independent brute-force subset oracle.
- `scripts/build_module.sh` — Apple Clang module build (the available
  Makefile generator does not scan module dependencies).

## Core rules

- Each manifest leaf has its own threshold of **distinct signing principals**.
  Senders whose public-key material is equal are aliases and count once.
- A sender's authority interval may cover only part of a leaf. Fragments whose
  span is not fully inside their authority are rejected with evidence.
- Endorsement is evaluated on sparse endpoint cells built only from explicit
  event coordinates (64-bit, half-open). Nothing is allocated by maximum
  address.
- Every byte cell must be covered by enough distinct principals endorsing the
  same object version, and the cross-leaf content root must recompute to the
  trusted root in the snapshot.
- Same-version partial overlaps with different digests are hard conflicts and
  taint the version; nested intervals remain compatible. Different versions
  over the same bytes are always retained as conflict evidence, even when the
  byte digest is identical. There is no first-come-first-wins behaviour.
- A query accepts at most `gsb::max_candidate_records` (22). The minimum
  certificate is chosen by fewest records, then fewest total referenced bytes,
  then lexicographic record-id tuple, then version.

## Trust snapshots

`ReassemblyEngine` is stateless. Each `query` is judged entirely against the
single supplied `TrustSnapshot`. Revoking a principal or changing a threshold
produces a new snapshot generation; a fresh query cannot mix records across
snapshots. An issued `Certificate` embeds an immutable copy of its issuing
snapshot, and `verify_certificate` always re-judges against that embedded
snapshot, so old leases remain verifiable after the current snapshot changes.

Signature verification, revocation timestamps and key resolution are host
injected through `HostHooks`. Default implementations read the tables embedded
in the snapshot.

## Build and test

```sh
cmake -S . -B build/cmake
cmake --build build/cmake
ctest --test-dir build/cmake --output-on-failure

# Named-module import path (Apple Clang):
./scripts/build_module.sh
```

The test suite covers sparse endpoint sweeps, cross-leaf partial authority,
identical bytes under different versions, alias duplicate votes, nested
intervals, retained hard conflicts, snapshot revocation/threshold changes with
old-lease readback, the 22-record cap, and an exhaustive all-subset oracle.
