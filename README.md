# proofstitch

Shard proofs provide local conclusions plus boundary assumptions; the
stitcher accepts a conclusion only when every assumption behind it is
satisfied by another shard's valid, digest-matching evidence, and the
dependency chain bottoms out in root facts. Mutual assumptions with no root
support (A assumes B, B assumes A) are detected as cycles and rejected.

## Model

- `Fact` / `Rule` / `Step` / `Assumption` / `ShardProof` all carry a stable
  string ID and an integer version.
- Digests are SHA-256 over canonical JSON, so evidence is reproducible.
- Every `Step` is replayable: re-applying the referenced rule version to the
  referenced inputs must reproduce the claimed output digest.

## Stitching guarantees

- Each shard proof is replayed locally before anything is accepted.
- An assumption is satisfied only by *another* shard's export whose digest
  equals the expected digest; a digest mismatch is a hard `StitchError`.
- Grounding is a least fixed point from assumption-free shards; whatever
  remains rests on missing evidence or an unsupported cycle (reported via
  Tarjan SCC) and is rejected.
- Duplicate proofs (same `shard_id` + `version`) are ignored and never
  counted twice in `contributions`.

## Run

```sh
python3.11 sample.py                          # synthetic end-to-end demo
python3.11 -m unittest discover -s tests -v   # test suite
```

Standard library only; sample data is fully synthetic.
