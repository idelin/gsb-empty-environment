"""Incremental session: local edits, DAG-wide replacement, rule deletion.

- ``edit`` / ``replace_at`` are occurrence-local persistent edits: only the
  spine from the edited path to the root is rebuilt and recomputed, so a
  deep leaf edit touches only its ancestors along that path.
- ``replace_everywhere`` substitutes every structurally equal subtree and
  recomputes exactly the affected ancestor cone across all parents.
- ``delete_rules`` revokes every state that lost its (well-founded)
  support, cascading through dependent facts.
- Every mutation pushes an immutable ``RunResult`` snapshot; old snapshots
  remain queryable.
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Dict, List, Optional, Sequence, Set, Tuple

from .core import Automaton, Node, Path, Rule, postorder_unique
from .evaluation import (CancelToken, Derivations, EvalStats, Facts, Limits,
                         RunResult, check_constraints, compute)


@dataclass(frozen=True)
class EditReport:
    kind: str
    path: Optional[Path]
    recomputed_count: int
    pruned_count: int
    root_states_before: Tuple[str, ...]
    root_states_after: Tuple[str, ...]
    accepted_before: bool
    accepted_after: bool
    snapshot_index: int


@dataclass(frozen=True)
class RevokeReport:
    rule_ids: Tuple[str, ...]
    revoked: Tuple[Tuple[Node, str], ...]
    revoked_count: int
    snapshot_index: int


class Session:
    def __init__(self, automaton: Automaton, root: Node, *,
                 limits: Optional[Limits] = None,
                 token: Optional[CancelToken] = None) -> None:
        self.automaton = automaton
        self.limits = limits or Limits()
        self.token = token
        automaton.alphabet.validate_tree(root)
        self._root = root
        self._facts: Facts = {}
        self._derivations: Derivations = {}
        self._snapshots: List[RunResult] = []
        self._recompute_all()

    # ----------------------------------------------------------- queries
    @property
    def root(self) -> Node:
        return self._root

    @property
    def snapshots(self) -> Tuple[RunResult, ...]:
        return tuple(self._snapshots)

    def snapshot(self, index: int = -1) -> RunResult:
        return self._snapshots[index]

    def _current_accepted(self) -> bool:
        return self._snapshots[-1].accepted

    # --------------------------------------------------------- internals
    def _recompute_all(self) -> EvalStats:
        nodes = postorder_unique(self._root)
        facts, derivations, stats = compute(
            self.automaton, nodes, limits=self.limits, token=self.token)
        self._facts = facts
        self._derivations = derivations
        self._push_snapshot(stats)
        return stats

    def _push_snapshot(self, stats: Optional[EvalStats]) -> None:
        self._snapshots.append(RunResult(
            self.automaton, self._root, self._facts, self._derivations,
            check_constraints(self.automaton, self._facts), stats))

    def _gc(self) -> int:
        reachable = set(postorder_unique(self._root))
        pruned = 0
        for node in list(self._facts):
            if node not in reachable:
                del self._facts[node]
                pruned += 1
        for key in list(self._derivations):
            if key[0] not in reachable:
                del self._derivations[key]
        return pruned

    def _apply_dirty(self, dirty: Set[Node], kind: str,
                     path: Optional[Path]) -> EditReport:
        before = self._snapshots[-1]
        self.automaton.alphabet.validate_tree(self._root)
        nodes = postorder_unique(self._root)
        _, _, stats = compute(self.automaton, nodes,
                              facts=self._facts, derivations=self._derivations,
                              dirty=dirty, limits=self.limits,
                              token=self.token)
        pruned = self._gc()
        self._push_snapshot(stats)
        after = self._snapshots[-1]
        return EditReport(
            kind=kind, path=path,
            recomputed_count=len(dirty), pruned_count=pruned,
            root_states_before=tuple(sorted(before.root_states)),
            root_states_after=tuple(sorted(after.root_states)),
            accepted_before=before.accepted, accepted_after=after.accepted,
            snapshot_index=len(self._snapshots) - 1)

    # ----------------------------------------------------------- edits
    def edit(self, path: Path, *, label=None, attrs=None,
             children=None) -> EditReport:
        """Occurrence-local edit of the node at ``path``."""
        target = self._root
        for index in path:
            target = target.children[index]
        new_node = target.with_changes(label=label, children=children,
                                       attrs=attrs)
        return self.replace_at(path, new_node)

    def replace_at(self, path: Path, new_node: Node) -> EditReport:
        spine: List[Node] = []

        def rebuild(node: Node, rest: Path) -> Node:
            if not rest:
                spine.append(new_node)
                return new_node
            index = rest[0]
            kids = list(node.children)
            kids[index] = rebuild(kids[index], rest[1:])
            fresh = Node(node.label, tuple(kids), node.attrs)
            spine.append(fresh)
            return fresh

        self._root = rebuild(self._root, tuple(path))
        return self._apply_dirty(set(spine), "edit", tuple(path))

    def replace_everywhere(self, old: Node, new: Node) -> EditReport:
        """Replace every structurally-equal occurrence of ``old`` and
        recompute the whole ancestor cone (all DAG parents)."""
        memo: Dict[Node, Node] = {}
        changed: List[Node] = []

        def rebuild(node: Node) -> Node:
            if node in memo:
                return memo[node]
            if node == old:
                memo[node] = new
                changed.append(new)
                return new
            kids = tuple(rebuild(c) for c in node.children)
            if all(a is b for a, b in zip(kids, node.children)):
                out = node
            else:
                out = Node(node.label, kids, node.attrs)
                changed.append(out)
            memo[node] = out
            return out

        self._root = rebuild(self._root)
        return self._apply_dirty(set(changed), "replace_everywhere", None)

    # ----------------------------------------------------------- rules
    def delete_rules(self, rids: Sequence[str]) -> RevokeReport:
        """Delete rules and revoke every state that lost all support.

        Revocation recomputes the least fixpoint over the current DAG with
        the remaining rules, so states whose only support ran through the
        deleted rules (directly or through a cascade) disappear; cyclically
        self-supporting states cannot survive either.
        """
        rids = tuple(rids)
        old_facts = {(n, s) for n, states in self._facts.items()
                     for s in states}
        self.automaton = self.automaton.without_rules(rids)
        self._recompute_all()
        new_facts = {(n, s) for n, states in self._facts.items()
                     for s in states}
        revoked = tuple(sorted(
            old_facts - new_facts,
            key=lambda ns: (ns[0].label, ns[0].arity, repr(ns[0].attrs),
                            ns[1])))
        return RevokeReport(rule_ids=rids, revoked=revoked,
                            revoked_count=len(revoked),
                            snapshot_index=len(self._snapshots) - 1)

    def add_rules(self, rules: Sequence[Rule]) -> EditReport:
        before = self._snapshots[-1]
        self.automaton = self.automaton.with_rules(rules)
        stats = self._recompute_all()
        after = self._snapshots[-1]
        return EditReport(
            kind="add_rules", path=None,
            recomputed_count=len(postorder_unique(self._root)), pruned_count=0,
            root_states_before=tuple(sorted(before.root_states)),
            root_states_after=tuple(sorted(after.root_states)),
            accepted_before=before.accepted, accepted_after=after.accepted,
            snapshot_index=len(self._snapshots) - 1)
