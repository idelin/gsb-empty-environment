"""Stitch shard proofs into a grounded, replayable evidence graph.

Guarantees enforced here:
- Every shard proof is replayed; tampered steps are rejected (ReplayError).
- An assumption is only satisfied by another shard's export whose digest
  equals the assumption's expected digest; a digest mismatch is a hard error.
- Conclusions must be grounded: shards whose assumptions bottom out only in
  a cycle of mutual assumptions (A assumes B, B assumes A) are rejected.
- Duplicate shard proofs (same shard_id + version) are ignored, so their
  contributions are never counted twice.
"""
from __future__ import annotations

from dataclasses import dataclass, field

from .core import Fact, RuleRegistry, replay_steps
from .shards import ShardProof


class StitchError(Exception):
    """Raised for hard stitching failures (digest mismatch, bad proof)."""


@dataclass
class StitchResult:
    accepted_conclusions: dict[str, Fact]
    contributions: dict[str, int]
    grounded_shards: set[str]
    ungrounded_shards: set[str]
    cycles: list[list[str]]
    duplicates_ignored: list[tuple[str, int]] = field(default_factory=list)


class Stitcher:
    def __init__(self, registry: RuleRegistry) -> None:
        self._registry = registry
        self._proofs: dict[tuple[str, int], ShardProof] = {}
        self._duplicates: list[tuple[str, int]] = []

    def add_proof(self, proof: ShardProof) -> bool:
        """Add a shard proof. Returns False (and ignores) exact duplicates."""
        if proof.key in self._proofs:
            self._duplicates.append(proof.key)
            return False
        self._proofs[proof.key] = proof
        return True

    def stitch(self) -> StitchResult:
        verified = self._replay_all()
        deps, unsatisfied = self._match_assumptions(verified)
        grounded = self._ground(deps, unsatisfied)
        ungrounded = set(self._proofs) - grounded
        cycles = _find_cycles(deps, ungrounded)

        accepted: dict[str, Fact] = {}
        contributions: dict[str, int] = {}
        for key in sorted(grounded):
            proof = self._proofs[key]
            for fact_id, fact in verified[key].items():
                accepted.setdefault(fact_id, fact)
            contributions[proof.shard_id] = contributions.get(proof.shard_id, 0) + len(
                proof.exports
            )

        return StitchResult(
            accepted_conclusions=accepted,
            contributions=contributions,
            grounded_shards={k[0] for k in grounded},
            ungrounded_shards={k[0] for k in ungrounded},
            cycles=cycles,
            duplicates_ignored=list(self._duplicates),
        )

    def _replay_all(self) -> dict[tuple[str, int], dict[str, Fact]]:
        verified: dict[tuple[str, int], dict[str, Fact]] = {}
        for key, proof in self._proofs.items():
            boundary = {f.fact_id: f for f in proof.boundary_inputs}
            for assumption in proof.assumptions:
                declared = boundary.get(assumption.fact_id)
                if declared is None or declared.digest != assumption.expected_digest:
                    raise StitchError(
                        f"{proof.shard_id}: assumption {assumption.assumption_id} "
                        f"does not match declared boundary input"
                    )
            derived = replay_steps(
                self._registry,
                list(proof.root_facts) + list(proof.boundary_inputs),
                list(proof.steps),
            )
            exports = {}
            for fact_id in proof.exports:
                if fact_id not in derived:
                    raise StitchError(
                        f"{proof.shard_id}: export {fact_id} is not derived"
                    )
                exports[fact_id] = derived[fact_id]
            verified[key] = exports
        return verified

    def _match_assumptions(self, verified):
        deps: dict[tuple[str, int], set[tuple[str, int]]] = {
            k: set() for k in self._proofs
        }
        unsatisfied: dict[tuple[str, int], list[str]] = {}
        for key, proof in self._proofs.items():
            for assumption in proof.assumptions:
                match = None
                mismatch = None
                for other_key, exports in verified.items():
                    if other_key == key:
                        continue  # a shard cannot vouch for itself
                    fact = exports.get(assumption.fact_id)
                    if fact is None:
                        continue
                    if fact.digest == assumption.expected_digest:
                        match = other_key
                        break
                    mismatch = (other_key, fact.digest)
                if match is not None:
                    deps[key].add(match)
                elif mismatch is not None:
                    other_key, actual = mismatch
                    raise StitchError(
                        f"{proof.shard_id}: boundary evidence digest mismatch for "
                        f"{assumption.fact_id}: expected "
                        f"{assumption.expected_digest[:12]}..., "
                        f"{other_key[0]} provides {actual[:12]}..."
                    )
                else:
                    unsatisfied.setdefault(key, []).append(assumption.assumption_id)
        return deps, unsatisfied

    def _ground(self, deps, unsatisfied) -> set[tuple[str, int]]:
        """Least fixed point: shards whose assumptions all resolve to grounded
        shards. Anything left over rests on an unsupported cycle (or on
        missing evidence) and is rejected."""
        grounded: set[tuple[str, int]] = set()
        changed = True
        while changed:
            changed = False
            for key in self._proofs:
                if key in grounded or key in unsatisfied:
                    continue
                if all(d in grounded for d in deps[key]):
                    grounded.add(key)
                    changed = True
        return grounded


def _find_cycles(deps, nodes) -> list[list[str]]:
    """Tarjan SCC over the ungrounded subgraph; report SCCs of size > 1."""
    index: dict = {}
    low: dict = {}
    on_stack: set = set()
    stack: list = []
    cycles: list[list[str]] = []
    counter = [0]

    def visit(v):
        index[v] = low[v] = counter[0]
        counter[0] += 1
        stack.append(v)
        on_stack.add(v)
        for w in deps[v] & nodes:
            if w not in index:
                visit(w)
                low[v] = min(low[v], low[w])
            elif w in on_stack:
                low[v] = min(low[v], index[w])
        if low[v] == index[v]:
            scc = []
            while True:
                w = stack.pop()
                on_stack.discard(w)
                scc.append(w)
                if w == v:
                    break
            if len(scc) > 1:
                cycles.append(sorted(k[0] for k in scc))

    for v in sorted(nodes):
        if v not in index:
            visit(v)
    return cycles
