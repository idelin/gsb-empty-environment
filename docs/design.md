# Reassembly Semantics

## Inputs

`FragmentRecord` is immutable and carries:

- `id`, object `version`, `sender`;
- half-open byte `span` `[begin, end)`;
- a 32-byte content `digest`;
- the half-open `authority` interval the sender is allowed to write.

`TrustSnapshot` is also immutable and carries:

- a monotonic `generation`;
- ordered manifest leaves, each with a span and threshold;
- the trusted content root;
- host tables for revocation timestamps and sender-to-key resolution.

The host injects three primitives in `HostHooks`:

1. `verify_signature(record, snapshot)`;
2. `revocation_time(sender, snapshot)`;
3. `key_material(sender, snapshot)`.

## Eligibility

A record participates only when all hold:

1. its span is a valid half-open interval;
2. its span is contained in its authority interval;
3. it intersects at least one manifest leaf;
4. its signature verifies;
5. its sender is not revoked in the snapshot;
6. its sender resolves to non-empty key material.

Every rejection is returned in `QueryReport.rejected` with a typed reason and,
for revocations, the injected timestamp.

## Cells and quorum

Cells are produced by a sparse sweep over the sorted set of leaf and record
endpoints. Coordinates are plain 64-bit values; no array is sized by an
address and no maximum address is assumed.

For each object version and each cell inside a leaf, the engine collects the
eligible records of that version whose span contains the cell. Records are
reduced to distinct `KeyMaterial` values, so aliases sharing key material cast
a single vote. A leaf is satisfied only when every one of its cells reaches
the leaf threshold.

The digest attributed to a cell is taken from the tightest covering fragment
(shortest span, record id as tie breaker). Nested fragments therefore endorse
the same region without corrupting each other's digest while remaining
independent endorsements.

## Conflicts

Two eligible overlapping records produce retained evidence:

- same version, different digest, neither span contains the other: a hard
  content conflict that makes the version infeasible;
- different object versions: a version divergence, retained even if the byte
  digests are equal.

Nested same-version records with different digests are not conflicts because
the inner record is a strictly more specific endorsement.

## Root and feasibility

For a quorum-satisfying, conflict-free version, the content root is recomputed
across all manifest leaves using the canonical ordered serialization of leaf
spans, the version, and each cell span with its attributed digest. The version
is feasible only when this root equals the snapshot's trusted root.

## Minimum certificate

At most 22 candidate records are accepted, so every subset can be enumerated
exhaustively (`2^22` worst case). Subsets are evaluated by increasing
cardinality; within a cardinality the engine minimizes, in order:

1. total referenced bytes (saturating 64-bit sum of span lengths);
2. the lexicographically smallest ascending record-id tuple;
3. the smallest version.

The returned certificate embeds the issuing snapshot, the chosen version, the
minimum record set, the byte total, the content root and any conflict evidence
among cited records.

## Snapshot isolation and lease readback

The engine holds no trust state. A query takes one snapshot and never reads any
other. Because certificates carry their own snapshot copy, verification after a
revocation or threshold change re-judges the old certificate against the old
snapshot only; mixing new and old snapshot fragments is structurally
impossible.
