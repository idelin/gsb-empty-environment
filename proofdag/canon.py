"""Canonicalization of proof DAGs.

Produces a traversal-order-independent canonical form:

* structurally identical subproofs are deduplicated (hash-consed);
* canonical nodes are sorted by structural key and numbered;
* commutative (conjunctive) premise sets are sorted, ordered operations
  keep their premise order, so order-sensitive proofs never merge;
* facts keep their source identity, so equal values from different
  sources stay distinct;
* the structural summary hash never includes internal node ids, so
  renaming internal nodes or permuting inputs yields the same summary,
  while substituting a source changes it;
* if two summaries collide (e.g. a forced/weak hash), equality is
  decided by structural comparison of the canonical forms.
"""

from __future__ import annotations

import hashlib
import json
from dataclasses import dataclass

from .dag import CycleError, ProofDAG, UnknownNodeError


def default_hash(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


@dataclass(frozen=True)
class CanonicalNode:
    index: int
    kind: str  # "F" (fact) or "S" (step)
    key: tuple
    children: tuple  # canonical indices of premises, in canonical order
    parents: tuple  # canonical indices of referrers (back-references)
    step_ids: tuple  # (stable_id, version) of merged steps; empty for facts
    is_goal: bool


@dataclass(frozen=True)
class CanonicalProof:
    nodes: tuple
    goals: tuple
    canonical_form: str
    summary: str

    def structurally_equal(self, other: "CanonicalProof") -> bool:
        return self.canonical_form == other.canonical_form


def summaries_equal(a: CanonicalProof, b: CanonicalProof) -> bool:
    """Summary comparison that is safe under forced hash collisions:
    equal hashes are always confirmed by structural verification."""
    if a.summary != b.summary:
        return False
    return a.canonical_form == b.canonical_form


def _post_order(dag: ProofDAG, goal_ids: list[str]) -> list[str]:
    WHITE, GRAY, BLACK = 0, 1, 2
    color: dict[str, int] = {}
    post: list[str] = []
    for root in goal_ids:
        if root not in dag.steps and root not in dag.facts:
            raise UnknownNodeError(f"unknown goal node {root!r}")
        if color.get(root, WHITE) != WHITE:
            continue
        stack = [root]
        while stack:
            nid = stack[-1]
            state = color.get(nid, WHITE)
            if state == WHITE:
                color[nid] = GRAY
                step = dag.steps.get(nid)
                if step is None:
                    if nid not in dag.facts:
                        raise UnknownNodeError(f"unknown premise node {nid!r}")
                    color[nid] = BLACK
                    post.append(nid)
                    stack.pop()
                    continue
                for pid in step.premises:
                    pstate = color.get(pid, WHITE)
                    if pstate == GRAY:
                        raise CycleError(
                            f"cyclic proof rejected at node {pid!r}"
                        )
                    if pstate == WHITE:
                        stack.append(pid)
            else:
                if state == GRAY:
                    color[nid] = BLACK
                    post.append(nid)
                stack.pop()
    return post


def canonicalize(
    dag: ProofDAG,
    goals: list[str] | None = None,
    hash_fn=default_hash,
) -> CanonicalProof:
    goal_ids = list(dag.goals) if goals is None else list(goals)
    post = _post_order(dag, goal_ids)

    # Bottom-up structural keys (hash-consing identity).
    key_of: dict[str, tuple] = {}
    for nid in post:
        fact = dag.facts.get(nid)
        if fact is not None:
            key_of[nid] = fact.key()
            continue
        step = dag.steps[nid]
        child_keys = [key_of[pid] for pid in step.premises]
        if step.rule.commutative:
            child_keys.sort()
        key_of[nid] = (
            "S",
            step.rule.stable_id,
            str(step.rule.version),
            tuple(child_keys),
        )

    # Deduplicate and number: canonical index assigned by sorted key order,
    # which is independent of insertion/traversal order.
    unique_keys = sorted(set(key_of[nid] for nid in post))
    index_of_key = {key: i for i, key in enumerate(unique_keys)}

    children_of: dict[tuple, tuple] = {}
    step_ids_of: dict[tuple, set] = {key: set() for key in unique_keys}
    parents_of: dict[tuple, set] = {key: set() for key in unique_keys}
    goal_key_set = set()
    for nid in post:
        key = key_of[nid]
        if nid in goal_ids:
            goal_key_set.add(key)
        fact = dag.facts.get(nid)
        if fact is not None:
            children_of.setdefault(key, ())
            continue
        step = dag.steps[nid]
        child_keys = [key_of[pid] for pid in step.premises]
        if step.rule.commutative:
            child_keys.sort()
        children_of[key] = tuple(index_of_key[ck] for ck in child_keys)
        step_ids_of[key].add((step.stable_id, step.version))
        parent_index = index_of_key[key]
        for pid in step.premises:
            parents_of[key_of[pid]].add(parent_index)

    nodes = tuple(
        CanonicalNode(
            index=index_of_key[key],
            kind=key[0],
            key=key,
            children=children_of[key],
            parents=tuple(sorted(parents_of[key])),
            step_ids=tuple(sorted(step_ids_of[key])),
            is_goal=key in goal_key_set,
        )
        for key in unique_keys
    )
    goal_indices = tuple(sorted(index_of_key[key] for key in goal_key_set))

    records = [{"goals": list(goal_indices)}]
    for node in nodes:
        records.append(
            [
                node.index,
                node.kind,
                node.key,
                list(node.children),
                list(node.parents),
                [list(s) for s in node.step_ids],
                node.is_goal,
            ]
        )
    canonical_form = json.dumps(records, sort_keys=True, separators=(",", ":"))
    return CanonicalProof(
        nodes=nodes,
        goals=goal_indices,
        canonical_form=canonical_form,
        summary=hash_fn(canonical_form.encode("utf-8")),
    )
