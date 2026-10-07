# graphsum

Canonical summarization for rooted, directed, labeled multigraphs with at
most 8 nodes.

## Guarantees

- **Canonical form**: isomorphic graphs (same root/labels/direction/
  parallel-edge multiplicities, any node numbering and edge insertion
  order) produce the identical `Summary.Encoding`; non-isomorphic graphs
  produce different encodings.
- **Exact enumeration, no hashing**: the canonical encoding is the
  lexicographically minimal serialization over all n! node numberings
  (n ≤ 8 ⇒ ≤ 40320). Nothing is truncated at cycles — see
  `TestCyclesNotTruncated`, which uses C6 vs 2×C3 and C4 vs 2×C2
  (degree-regular pairs that defeat recursive/degree hashing).
- **Parallel edges** are preserved as multiplicities (`labelxcount` cells);
  edge insertion order is irrelevant by construction.
- **Independent verification**: `Verify` re-checks every summary through a
  separate code path — an independent implementation of the canonical
  format, an independent permutation enumeration (Heap's algorithm vs. the
  producer's iterative next-permutation), and a direct automorphism test of
  every permutation for the orbit computation.

## API

- `Canonical(g) (*Summary, error)` — exact canonical summary:
  `Encoding`, `Numbering` (canonical position → stable node ID), and
  `Orbits` (automorphism orbits over stable node IDs).
- `Verify(g, s) error` — independent permutation checker for a claimed
  summary. Recomputes the minimum encoding and the automorphism orbits
  from scratch and rejects any mismatch.
- `Service` — versioned incremental wrapper:
  - every edit (`AddNode`, `AddEdge`, `SetLabel`, `SetRoot`, …) bumps
    `Version()`; node IDs are stable identities across versions;
  - repeated `Summary()` calls on one version hit the cache;
  - a structurally no-op local edit reuses the cached summary, but only
    after the independent checker re-verifies all symmetry-equivalent
    numberings (`Stats.LastVerifiedNumberings` == |Aut(g)|);
  - structural edits recompute by exact enumeration and are independently
    verified before caching.

## Layout

- `graph.go` — graph type (root, labels, adjacency with multiplicities)
- `canon.go` — canonical encoding + exact enumeration (producer)
- `verify.go` — independent permutation checker (verifier)
- `service.go` — versioned, caching, re-verifying summary service
