"""Proof DAG store with structural sharing and incremental revocation.

Nodes are interned by structural key, so a multiply-referenced subproof is
stored exactly once and carries complete back-references (its parent set).
Source identity is part of a fact's key, so equal values from different
sources stay independent. Commutative premises are normalized (sorted) at
interning time; ordered premises keep their significant order.

Edges can be removed incrementally; once a shared subgraph is no longer
reachable from any root, it is revoked (cascading garbage collection).
"""

from __future__ import annotations

from typing import Dict, List, Optional, Set, Tuple, Union

from .canonical import CanonicalForm, DigestFn, DigestRegistry, canonicalize, default_digest
from .errors import UnknownNodeError
from .model import Fact, Rule, Step

Key = Tuple


class ProofStore:
    def __init__(self, digest_fn: DigestFn = default_digest) -> None:
        self._digest_fn = digest_fn
        self._facts: Dict[Key, Fact] = {}
        self._rules: Dict[Key, Optional[Rule]] = {}
        self._children: Dict[Key, Tuple[Key, ...]] = {}
        self._parents: Dict[Key, Set[Key]] = {}
        self._roots: List[Key] = []
        # Stable external step names -> interned structural key.
        self.step_index: Dict[str, Key] = {}
        self.registry = DigestRegistry()

    # -- interning ------------------------------------------------------

    def add_fact(self, fact: Fact) -> Key:
        key = ("asserted", fact.structural_key())
        if key not in self._facts:
            self._facts[key] = fact
            self._rules[key] = None
            self._children[key] = ()
            self._parents[key] = set()
        return key

    def add_step(self, step: Step) -> Key:
        child_keys = tuple(self._intern(item) for item in step.premises)
        if step.rule.commutative:
            child_keys = tuple(sorted(child_keys))
        key = (
            "derived",
            step.rule.structural_key(),
            child_keys,
            step.conclusion.structural_key(),
        )
        if key not in self._facts:
            self._facts[key] = step.conclusion
            self._rules[key] = step.rule
            self._children[key] = child_keys
            self._parents[key] = set()
            for child in child_keys:
                self._parents[child].add(key)
        self.step_index[step.step_id] = key
        return key

    def _intern(self, item: Union[Fact, Step]) -> Key:
        if isinstance(item, Fact):
            return self.add_fact(item)
        if isinstance(item, Step):
            return self.add_step(item)
        raise TypeError(f"cannot intern {type(item)!r}")

    # -- roots ------------------------------------------------------------

    def mark_root(self, key: Key) -> None:
        if key not in self._facts:
            raise UnknownNodeError(f"unknown node: {key!r}")
        if key not in self._roots:
            self._roots.append(key)

    def remove_root(self, key: Key) -> None:
        if key in self._roots:
            self._roots.remove(key)
            self._gc_from(key)

    @property
    def roots(self) -> Tuple[Key, ...]:
        return tuple(self._roots)

    # -- incremental edge deletion ----------------------------------------

    def remove_edge(self, parent_key: Key, child_key: Key) -> None:
        """Remove the edge parent -> child and revoke what becomes unreachable."""
        if parent_key not in self._children:
            raise UnknownNodeError(f"unknown parent node: {parent_key!r}")
        children = self._children[parent_key]
        if child_key not in children:
            raise UnknownNodeError(
                f"no edge from {parent_key!r} to {child_key!r}"
            )
        self._children[parent_key] = tuple(c for c in children if c != child_key)
        self._parents[child_key].discard(parent_key)
        self._gc_from(child_key)

    def _gc_from(self, key: Key) -> None:
        stack = [key]
        while stack:
            current = stack.pop()
            if current in self._roots or self._parents.get(current):
                continue
            if current not in self._facts:
                continue
            for child in self._children[current]:
                self._parents[child].discard(current)
                stack.append(child)
            del self._facts[current]
            del self._rules[current]
            del self._children[current]
            del self._parents[current]
            self.step_index = {
                name: k for name, k in self.step_index.items() if k != current
            }

    # -- inspection ---------------------------------------------------------

    def has_node(self, key: Key) -> bool:
        return key in self._facts

    def node_count(self) -> int:
        return len(self._facts)

    def parents_of(self, key: Key) -> Set[Key]:
        return set(self._parents[key])

    def children_of(self, key: Key) -> Tuple[Key, ...]:
        return self._children[key]

    def _reachable(self) -> Set[Key]:
        seen: Set[Key] = set()
        stack = list(self._roots)
        while stack:
            key = stack.pop()
            if key in seen or key not in self._facts:
                continue
            seen.add(key)
            stack.extend(self._children[key])
        return seen

    # -- canonical form / evidence ------------------------------------------

    def canonical_form(self) -> CanonicalForm:
        graph = {
            key: (self._facts[key], self._rules[key], self._children[key])
            for key in self._reachable()
        }
        return canonicalize(graph, self._roots, self._digest_fn)

    def register_canonical(self) -> Tuple[CanonicalForm, bool]:
        """Canonicalize and register in the digest registry.

        Returns (stored_form, created_new_entry). Digest collisions are
        resolved by full structural comparison inside the registry.
        """
        return self.registry.register(self.canonical_form())

    def evidence(self) -> dict:
        return self.canonical_form().to_evidence()
