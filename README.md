# merkle-evidence

Ordered evidence records committed into a Merkle tree, with membership
proofs, deterministic missing-leaf padding, compressed batch (shared-path)
proofs, and append-only consistency proofs. The verifier never accesses
the full record set and never trusts the prover for padding leaves or
internal counts. Pure Python 3.11 standard library; tests use `unittest`.

## Layout

- `merkle_evidence/hashing.py` — domain-separated, version-bound SHA-256
- `merkle_evidence/tree.py` — append-only ordered tree, padding rule
- `merkle_evidence/proofs.py` — proof objects (JSON round-trippable)
- `merkle_evidence/prover.py` — proof generation (holds full records)
- `merkle_evidence/verifier.py` — independent verifier (proofs + roots only)
- `merkle_evidence/evidence.py` — replayable evidence log
- `merkle_evidence/demo.py` — runnable synthetic sample
- `tests/test_merkle.py` — 28 unittests

## Rules (stable IDs, versioned)

| Rule ID | Meaning |
|---|---|
| `rule:leaf-hash@1` | `H("LEAF", version, index, len, data)` — binds content **and** position |
| `rule:pad-leaf@1` | `H("PADL", version, index)` — deterministic padding leaf |
| `rule:pad-subtree@1` | padding subtree root, pure function of `(version, level, start)` |
| `rule:node-hash@1` | `H("NODE", version, left, right)` |
| `rule:empty-root@1` | root of the empty tree |
| `rule:membership-proof@1` | ordered sibling list, `PAD` markers recomputed by verifier |
| `rule:batch-proof@1` | multi-member proof, shared path nodes sent once |
| `rule:consistency-proof@1` | canonical subtree decomposition proven under old **and** new root |
| `rule:evidence-replay@1` | deterministic log replay |

Facts and log steps carry stable IDs (`fact:000000@1`, `step:000000@1`, …)
plus the scheme version `merkle-evidence/1`.

## Security properties

- **Size binding**: the root is `H("ROOT", version, tree_size, top)`, so a
  proof made for one tree size can never verify under another.
- **Position identity**: leaf hashes mix in the index — duplicate contents
  at different positions get distinct leaves and non-swappable proofs.
- **Padding distrust**: padding is a pure function of
  `(version, level, position)`; the verifier recomputes it. A sender that
  hides a real node as `PAD`, or supplies a wrong padding hash, fails.
- **Version binding**: every digest mixes in the scheme version; proofs
  from different versions cannot be spliced into a false accept.
- **Anti-splice strictness**: every supplied node must be consumed exactly
  once; missing *or* leftover nodes fail.
- **Odd sizes**: leaves pad to the next power of two, so every level is
  even and handled uniformly.

## Run

```
python3 -m merkle_evidence.demo      # synthetic sample run
python3 -m unittest discover -s tests
```

## Sample output (synthetic data)

```
scheme version : merkle-evidence/1
tree size      : 7 leaves (padded to 8)
root @ size 5  : 81eac7c9866ffe44818ddad76bdfa6442b8b25dc532348b90ccd922efdef72cd
root @ size 7  : 3cb008cc0dada69ccba91f8a6864fbd2ab876b6d30782aee7286f84b1438a17c

membership leaf 3            : PASS
batch proof [1,3,4,5]        : PASS (3 nodes sent vs 12 without sharing)
consistency 5 -> 7           : PASS (subtrees=[(0, 2), (4, 0)])

tamper: tampered leaf content                 : FAIL (rejected)
tamper: tampered leaf position                : FAIL (rejected)
tamper: tampered tree size                    : FAIL (rejected)
tamper: tampered path order                   : FAIL (rejected)
tamper: duplicate content swapped position    : FAIL (rejected)
tamper: cross-version splice                  : FAIL (rejected)

evidence log steps           : 9
replay root matches          : PASS
tampered log replay          : FAIL (rejected: leaf hash mismatch at step:000002@1)
```

## Test result

```
Ran 28 tests in 0.008s
OK
```

Coverage highlights: all sizes 1–16 × all indices; all `2^5 − 1` non-empty
leaf subsets of a 5-leaf tree; all `(old, new)` size pairs up to 9 leaves
for consistency; tampering of content, position, size, path order, node
sets; cross-version splicing of all three proof types; replay tampering.
