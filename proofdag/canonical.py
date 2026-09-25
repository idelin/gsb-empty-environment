"""Canonicalization of equivalence-proof DAGs.

Canonicalization is a pure function of the *structure* of the proof graph:
user-supplied node names and insertion/traversal order never influence the
result, while source identities of facts always do.

The canonical form deduplicates structurally identical subproofs, sorts
nodes by their structural key and numbers them deterministically. The
digest is a fingerprint of that canonical form only; lookups by digest
always fall back to full structural comparison, so a (forced or
accidental) digest collision never merges two different proofs.
"""

from __future__ import annotations

import hashlib
import json
from dataclasses import dataclass
from typing import Callable, Dict, Hashable, List, Mapping, Optional, Tuple

from .errors import CycleError, UnknownNodeError
from .model import Fact, Rule

NodeId = Hashable
# A graph descriptor maps a temporary node id to
# (conclusion_fact, rule_or_None, child_node_ids).
Graph = Mapping[NodeId, Tuple[Fact, Optional[Rule], Tuple[NodeId, ...]]]
DigestFn = Callable[[str], str]

FORMAT = "proofdag-canonical/1"


def default_digest(text: str) -> str:
    return hashlib.sha256(text.encode("utf-8")).hexdigest()


@dataclass(frozen=True)
class CanonicalNode:
    number: int
    fact: Fact
    rule: Optional[Rule]  # None => asserted leaf
    premises: Tuple[int, ...]  # canonical premise order

    def to_json(self) -> dict:
        return {
            "number": self.number,
            "kind": "asserted" if self.rule is None else "derived",
            "fact": {
                "fact_id": self.fact.fact_id,
                "version": self.fact.version,
                "source_id": self.fact.source_id,
                "payload": self.fact.payload,
            },
            "rule": None
            if self.rule is None
            else {
                "rule_id": self.rule.rule_id,
                "version": self.rule.version,
                "commutative": self.rule.commutative,
            },
            "premises": list(self.premises),
        }


@dataclass(frozen=True)
class CanonicalForm:
    nodes: Tuple[CanonicalNode, ...]
    roots: Tuple[int, ...]
    digest: str

    def structural_tuple(self) -> tuple:
        """Exact structural identity, used to verify digest collisions."""
        return (
            tuple(
                (
                    node.number,
                    node.fact.structural_key(),
                    None if node.rule is None else node.rule.structural_key(),
                    node.premises,
                )
                for node in self.nodes
            ),
            self.roots,
        )

    def to_evidence(self) -> dict:
        return {
            "format": "proofdag-evidence/1",
            "digest": self.digest,
            "nodes": [node.to_json() for node in self.nodes],
            "roots": list(self.roots),
        }


def _check_references(graph: Graph, roots: Tuple[NodeId, ...]) -> None:
    for root in roots:
        if root not in graph:
            raise UnknownNodeError(f"unknown root node: {root!r}")
    for nid, (_, _, children) in graph.items():
        for child in children:
            if child not in graph:
                raise UnknownNodeError(
                    f"node {nid!r} references unknown node {child!r}"
                )


def _check_acyclic(graph: Graph) -> None:
    WHITE, GRAY, BLACK = 0, 1, 2
    color = {nid: WHITE for nid in graph}
    for start in graph:
        if color[start] != WHITE:
            continue
        color[start] = GRAY
        stack = [(start, iter(graph[start][2]))]
        while stack:
            nid, it = stack[-1]
            descended = False
            for child in it:
                if color[child] == GRAY:
                    raise CycleError(
                        f"cyclic proof rejected: edge {nid!r} -> {child!r} closes a cycle"
                    )
                if color[child] == WHITE:
                    color[child] = GRAY
                    stack.append((child, iter(graph[child][2])))
                    descended = True
                    break
            if not descended:
                color[nid] = BLACK
                stack.pop()


def _canonical_child_order(rule, children, keys) -> Tuple[NodeId, ...]:
    if rule.commutative:
        return tuple(sorted(children, key=lambda c: keys[c]))
    return tuple(children)


def _structural_keys(graph: Graph) -> Dict[NodeId, tuple]:
    keys: Dict[NodeId, tuple] = {}
    for start in graph:
        if start in keys:
            continue
        stack = [(start, False)]
        while stack:
            nid, expanded = stack.pop()
            if nid in keys:
                continue
            fact, rule, children = graph[nid]
            if rule is None:
                keys[nid] = ("asserted", fact.structural_key())
                continue
            if not expanded:
                stack.append((nid, True))
                for child in children:
                    if child not in keys:
                        stack.append((child, False))
                continue
            ordered = _canonical_child_order(rule, children, keys)
            child_keys = tuple(keys[c] for c in ordered)
            keys[nid] = (
                "derived",
                rule.structural_key(),
                child_keys,
                fact.structural_key(),
            )
    return keys


def _reachable_from(graph: Graph, roots: Tuple[NodeId, ...]) -> set:
    seen = set()
    stack = list(roots)
    while stack:
        nid = stack.pop()
        if nid in seen:
            continue
        seen.add(nid)
        stack.extend(graph[nid][2])
    return seen


def _serialize(nodes: List[CanonicalNode], roots: Tuple[int, ...]) -> str:
    return json.dumps(
        {
            "format": FORMAT,
            "nodes": [node.to_json() for node in nodes],
            "roots": list(roots),
        },
        sort_keys=True,
        separators=(",", ":"),
    )


def canonicalize(
    graph: Graph,
    roots,
    digest_fn: DigestFn = default_digest,
) -> CanonicalForm:
    """Build the canonical form of a proof DAG.

    Raises CycleError for cyclic graphs and UnknownNodeError for dangling
    references. The result is independent of the order in which nodes and
    roots are enumerated.
    """
    roots = tuple(roots)
    _check_references(graph, roots)
    _check_acyclic(graph)
    keys = _structural_keys(graph)
    reachable = _reachable_from(graph, roots)
    ordered = sorted(reachable, key=lambda nid: keys[nid])
    number = {nid: idx for idx, nid in enumerate(ordered)}
    nodes: List[CanonicalNode] = []
    for nid in ordered:
        fact, rule, children = graph[nid]
        premises: Tuple[int, ...] = ()
        if rule is not None:
            ordered_children = _canonical_child_order(rule, children, keys)
            premises = tuple(number[c] for c in ordered_children)
        nodes.append(CanonicalNode(number[nid], fact, rule, premises))
    root_numbers = tuple(sorted(number[r] for r in roots))
    body = _serialize(nodes, root_numbers)
    return CanonicalForm(tuple(nodes), root_numbers, digest_fn(body))


class DigestRegistry:
    """Indexes canonical forms by digest.

    A digest is only a fingerprint: registration and lookup always verify
    the full structure, so a forced digest collision keeps both proofs as
    independent entries instead of merging them.
    """

    def __init__(self) -> None:
        self._buckets: Dict[str, List[CanonicalForm]] = {}

    def register(self, form: CanonicalForm) -> Tuple[CanonicalForm, bool]:
        """Register ``form``; returns (stored_form, created_new_entry)."""
        bucket = self._buckets.setdefault(form.digest, [])
        for existing in bucket:
            if existing.structural_tuple() == form.structural_tuple():
                return existing, False
        bucket.append(form)
        return form, True

    def resolve(self, form: CanonicalForm) -> Optional[CanonicalForm]:
        """Return the stored form structurally equal to ``form``, if any."""
        for existing in self._buckets.get(form.digest, []):
            if existing.structural_tuple() == form.structural_tuple():
                return existing
        return None

    def bucket_size(self, digest: str) -> int:
        return len(self._buckets.get(digest, []))
