# merkle-evidence

Ordered evidence records anchored in a Merkle tree. Python 3.11, standard
library only (`hashlib`, `json`, `dataclasses`, `unittest`). All sample
data is synthetic; all evidence is deterministically replayable.

## Trust model

The independent verifier **never accesses the full record list**. It holds:

- a trusted `Checkpoint(log_version, tree_size, root)` (e.g. signed),
- a proof object,
- the digest(s) of only the record(s) under inspection.

The verifier recomputes the entire tree shape from the checkpoint's size
alone. It never trusts prover-supplied padding leaves, internal node
counts, or the size/version fields inside a proof — those must equal the
checkpoint before any hash is recomputed.

## Rules (stable IDs, versioned)

| Rule ID            | v  | Definition |
|--------------------|----|------------|
| `RULE-HASH-LEAF`   | 1  | `SHA256(0x00 \| u64be(index) \| ustr(log_version) \| record_digest)` |
| `RULE-HASH-NODE`   | 1  | `SHA256(0x01 \| left \| right)` |
| `RULE-HASH-RECORD` | 1  | `SHA256(0x02 \| canonical_json(record))` |
| `RULE-HASH-EMPTY`  | 1  | `SHA256("")` (root of the empty tree) |
| `RULE-SPLIT`       | 1  | subtree of `n>1` leaves splits at `k`, largest power of two `< n` |
| `RULE-PAD-PROMOTE` | 1  | missing-leaf handling: an odd node is **promoted unchanged** to the next level; no padding leaf is ever transmitted or accepted |

Consequences:

- **Positional identity**: the leaf hash binds `index`, so duplicate record
  content at different positions yields different leaves; proofs cannot be
  swapped between positions.
- **Version binding**: the leaf hash binds `log_version`, and proofs carry
  `log_version`/`tree_size`/`leaf_index`; proofs from different versions
  cannot be spliced into a false acceptance.
- **Size binding**: size is pinned by the trusted checkpoint; a crafted
  path under a different claimed size is rejected.

## Proof types

- `MembershipProof` — single leaf; sibling hashes ordered leaf-to-root.
- `MultiProof` — batch proof for several members; shared path hashes are
  emitted **once** (compression), in exact verifier-consumption order
  (depth-first, left before right).
- `ConsistencyProof` — append-only proof that the tree of `old_size` is a
  prefix of the tree of `new_size` (RFC 6962-style subproofs).

Facts, rules and steps carry stable IDs and versions
(`Fact(fact_id, version, …)`, `Rule(rule_id, version, …)`,
`Step(step_id, version, …)`); records embed the exact referenced
(id, version) pairs, so digests are self-contained and replayable.

## Layout

```
merkle_evidence/
  hashing.py   # domain-separated hashing rules
  model.py     # Fact / Rule / Step / EvidenceRecord / EvidenceLog
  tree.py      # MerkleTree: split + padding rules, proof generation
  proofs.py    # Checkpoint + proof objects + independent verifiers
  replay.py    # deterministic replay of a log against a root
  demo.py      # synthetic end-to-end demo
tests/         # unittest suite (exhaustive small-tree enumeration)
```

## Run

```
python3.11 -m merkle_evidence.demo
python3.11 -m unittest discover -s tests -t . -v
```

## Sample run (synthetic data)

```
log=synthetic-evidence-log version=v2026.10 size=7
root=b0b76a6e02d8a72249ba72a8e42aa4efc962bec31c72a7f9963bc8f43c7d39ba

[replay] size=7 root_matches=True

[membership] index=4 path_len=3 verified=True

[multi] indices=(1, 2, 5) hashes=4 (individual paths would need 9) verified=True

[consistency] 3 -> 7 hashes=4 verified=True

[tamper checks]
  tampered leaf content       -> False
  tampered leaf position      -> False
  tampered tree size (7->8)   -> False
  reversed path order         -> False
  spliced other-version proof -> False
```

## Test results

```
Ran 48 tests in 0.791s
OK
```

Coverage includes: exhaustive membership proofs for all positions of trees
of size 1–20; exhaustive multi proofs for **every non-empty leaf subset**
of trees of size 1–8; exhaustive consistency proofs for all `0 ≤ m ≤ n ≤
17`; tampering of leaf content, leaf position, tree size, path order,
truncated/extended hash lists (fake padding leaves); duplicate-content
position swapping; cross-version proof splicing; odd-level padding-rule
consistency; replay tamper detection.
