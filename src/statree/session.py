"""Incremental automaton session over a mutable shared-subtree DAG."""
from __future__ import annotations

from collections.abc import Iterable, Mapping, Sequence
from dataclasses import dataclass
from typing import Any

from .dag import Dag
from .engine import (
    CancelToken,
    Evidence,
    Limits,
    Proof,
    RuleIndex,
    Stats,
    check_budget,
    explain,
    least_fixpoint,
)
from .model import Rule, Signature


@dataclass(frozen=True, slots=True)
class EditReport:
    path: tuple[int, ...]
    old_leaf: int
    new_leaf: int
    rebuilt: tuple[int, ...]
    changed: bool
    accepting_before: frozenset[str]
    accepting_after: frozenset[str]


@dataclass(slots=True)
class Snapshot:
    """Immutable, queryable view of a committed session state."""

    version: int
    dag: Dag
    roots: Mapping[str, int]
    table: Mapping[int, tuple[frozenset[str], tuple[Proof, ...]]]
    accepting: Mapping[str, frozenset[str]]
    rule_names: frozenset[str]

    def states(self, node_id: int) -> frozenset[str]:
        return self.table[node_id][0]

    def accepting_states(self, root: str) -> frozenset[str]:
        return self.accepting[root]

    def explain(
        self,
        node_id: int,
        state: str,
        *,
        path: tuple[int | str, ...] = (),
        cap: int = 1024,
    ) -> tuple[Evidence, ...]:
        return explain(self.dag, node_id, state, self.table, path=path, cap=cap)


def _supported(proof: Proof, states: Mapping[int, frozenset[str]]) -> bool:
    _, _, _, kids = proof
    return all(cs in states.get(cid, frozenset()) for cid, cs in kids)


class Automaton:
    """State-constrained ordered tree automaton with incremental maintenance."""

    def __init__(
        self,
        alphabet: Sequence[str],
        raw_nodes: Iterable[Any],
        rules: Sequence[Rule | Mapping[str, Any]] = (),
        *,
        roots: Mapping[str, int] | Sequence[int] | None = None,
        signatures: Sequence[Signature | Mapping[str, Any] | str] = (),
        limits: Limits | None = None,
    ) -> None:
        self.dag = Dag.from_adjacency(alphabet, raw_nodes)
        self.rules = RuleIndex(tuple(Rule.parse(r) for r in rules))
        self.limits = limits or Limits()
        self.stats = Stats()
        self.signatures = tuple(Signature.parse(s) for s in signatures)
        if roots is None:
            self.roots: dict[str, int] = {
                f"root{i}": rid for i, rid in enumerate(self.dag.roots())
            }
        elif isinstance(roots, Mapping):
            self.roots = dict(roots)
        else:
            self.roots = {f"root{i}": rid for i, rid in enumerate(roots)}
        self._states: dict[int, frozenset[str]] = {}
        self._proofs: dict[int, list[Proof]] = {}
        self._snaps: dict[int, Snapshot] = {}
        self.version = 0
        self.evaluate_all()
        self.commit()

    # ----------------------------------------------------------------- utility
    def _budget(self, token: CancelToken | None = None) -> None:
        check_budget(self.limits, token, self.stats)

    def accepting_set(self, root_id: int) -> frozenset[str]:
        node = self.dag.node(root_id)
        states_set = self._states.get(root_id, frozenset())
        return frozenset(
            s
            for s in states_set
            if any(
                sig.matches(node.label, node.arity) and s in sig.states
                for sig in self.signatures
            )
        )

    def accepting(self) -> dict[str, frozenset[str]]:
        return {name: self.accepting_set(rid) for name, rid in self.roots.items()}

    def states(self, node_id: int) -> frozenset[str]:
        return self._states.get(node_id, frozenset())

    def proofs(self, node_id: int) -> tuple[Proof, ...]:
        return tuple(self._proofs.get(node_id, ()))

    def _fire_node(self, nid: int, token: CancelToken | None = None):
        return self.rules.fire(
            nid,
            self.dag.node(nid),
            self._states,
            self.stats,
            self.limits,
            token,
        )

    # ----------------------------------------------------------- full rebuild
    def evaluate_all(
        self, *, token: CancelToken | None = None
    ) -> dict[int, frozenset[str]]:
        """Bottom-up pass in topological order; each node fires once."""
        self.stats.reset()
        states: dict[int, frozenset[str]] = {}
        proofs: dict[int, list[Proof]] = {}
        for nid in self.dag.topo_order():
            self._budget(token)
            derived, got = self.rules.fire(
                nid,
                self.dag.node(nid),
                states,
                self.stats,
                self.limits,
                token,
            )
            states[nid] = derived
            proofs[nid] = list(got)
        self._states = states
        self._proofs = proofs
        return dict(states)

    def fixpoint_evaluate(
        self, *, token: CancelToken | None = None
    ) -> dict[int, frozenset[str]]:
        """Reference evaluation by explicit least-fixpoint rounds."""
        self.stats.reset()
        result = least_fixpoint(
            self.dag,
            self.rules,
            limits=self.limits,
            token=token,
            stats=self.stats,
        )
        self._states = {i: v[0] for i, v in result.items()}
        self._proofs = {i: list(v[1]) for i, v in result.items()}
        return dict(self._states)

    # ------------------------------------------------------------ rule edits
    def add_rule(
        self, rule: Rule | Mapping[str, Any], *, token: CancelToken | None = None
    ) -> bool:
        """Add a rule and propagate newly derivable states ancestor-wise."""
        parsed = Rule.parse(rule)
        if self.rules.get(parsed.name) is not None:
            return False
        self.stats.reset()
        self.rules.add(parsed)
        work: list[int] = list(self.dag.topo_order())
        queued = set(work)
        while work:
            self._budget(token)
            nid = work.pop(0)
            queued.discard(nid)
            before = self._states[nid]
            derived, got = self._fire_node(nid, token)
            merged = before | derived
            known_rules = {p[2] for p in self._proofs[nid]}
            for proof in got:
                if proof[2] not in known_rules:
                    self._proofs[nid].append(proof)
                    known_rules.add(proof[2])
            if merged != before:
                self.stats.derived += len(merged - before)
                self._states[nid] = merged
                for pid in sorted(self.dag.parents(nid)):
                    if pid not in queued:
                        queued.add(pid)
                        work.append(pid)
        return True

    def delete_rule(
        self, name: str, *, token: CancelToken | None = None
    ) -> Rule | None:
        """Delete a rule and retract every state that loses all support.

        The rule itself is removed first.  We then repeatedly drop proofs that
        are either instances of the deleted rule or rely on a retracted child
        state.  Whenever the *last* proof of a state disappears, the state is
        retracted and the check propagates to its parents (fixpoint).
        """
        rule = self.rules.remove(name)
        if rule is None:
            return None
        self.stats.reset()

        dirty: set[int] = {
            nid
            for nid, plist in self._proofs.items()
            if any(p[2] == name for p in plist)
        }
        while dirty:
            self._budget(token)
            nid = dirty.pop()
            changed = False
            kept: list[Proof] = []
            for proof in self._proofs[nid]:
                _, head, rname, kids = proof
                if rname == name or not _supported(proof, self._states):
                    changed = True
                    continue
                kept.append(proof)
            self._proofs[nid] = kept
            live_states = {p[1] for p in kept}
            for lost in self._states[nid] - live_states:
                self.stats.retracted += 1
                changed = True
            self._states[nid] = frozenset(live_states)
            if changed:
                for pid in self.dag.parents(nid):
                    dirty.add(pid)
        return rule

    # ------------------------------------------------------------- leaf edit
    def edit_leaf(
        self,
        root: str | int,
        child_path: Sequence[int],
        *,
        label: str | None = None,
        attrs: Mapping[str, Any] | None = None,
        token: CancelToken | None = None,
    ) -> EditReport:
        """Replace a deep leaf; only the leaf and its ancestors are rebuilt.

        Shared subtrees off the ancestor spine keep their cached states.
        """
        self.stats.reset()
        root_id = self.roots[root] if isinstance(root, str) else root
        path = tuple(child_path)

        old_ids = [root_id]
        cur = root_id
        for pos in path:
            node = self.dag.node(cur)
            if not (0 <= pos < node.arity):
                raise KeyError(f"path {path} leaves tree at {cur}")
            cur = node.children[pos]
            old_ids.append(cur)
        leaf_id = cur
        leaf = self.dag.node(leaf_id)
        if leaf.arity != 0:
            raise ValueError(f"node {leaf_id} at {path} is not a leaf")
        new_label = label if label is not None else leaf.label
        if self.dag.alphabet and new_label not in self.dag.alphabet:
            raise KeyError(f"label {new_label!r} not in alphabet")
        accept_before = self.accepting_set(root_id)

        new_ids: list[int | None] = [None] * len(old_ids)
        new_ids[-1] = self.dag.intern(
            new_label, (), leaf.attr_map() if attrs is None else attrs
        )
        for k in range(len(old_ids) - 2, -1, -1):
            parent = self.dag.node(old_ids[k])
            kids = list(parent.children)
            kids[path[k]] = new_ids[k + 1]  # type: ignore[index]
            new_ids[k] = self.dag.intern(parent.label, kids, parent.attr_map())
        new_root = int(new_ids[0])
        spine = [int(x) for x in new_ids]

        for nid in reversed(spine):
            self._budget(token)
            derived, got = self.rules.fire(
                nid,
                self.dag.node(nid),
                self._states,
                self.stats,
                self.limits,
                token,
            )
            self._states[nid] = derived
            self._proofs[nid] = list(got)

        if isinstance(root, str):
            self.roots[root] = new_root
        return EditReport(
            path=path,
            old_leaf=leaf_id,
            new_leaf=spine[-1],
            rebuilt=tuple(spine),
            changed=new_root != root_id,
            accepting_before=accept_before,
            accepting_after=self.accepting_set(new_root),
        )

    # -------------------------------------------------------------- snapshots
    def commit(self) -> Snapshot:
        self.version += 1
        snap = Snapshot(
            version=self.version,
            dag=self.dag.clone(),
            roots=dict(self.roots),
            table={
                i: (self._states[i], tuple(self._proofs.get(i, ())))
                for i in self.dag.ids()
            },
            accepting={
                name: self.accepting_set(rid) for name, rid in self.roots.items()
            },
            rule_names=frozenset(self.rules.names()),
        )
        self._snaps[self.version] = snap
        return snap

    def snapshot(self, version: int | None = None) -> Snapshot:
        if version is None:
            return self.commit()
        return self._snaps[version]

    def explain(
        self,
        node_id: int,
        state: str,
        *,
        path: tuple[int | str, ...] = (),
        cap: int = 1024,
    ) -> tuple[Evidence, ...]:
        table = {
            i: (self._states[i], tuple(self._proofs.get(i, ())))
            for i in self.dag.ids()
        }
        return explain(self.dag, node_id, state, table, path=path, cap=cap)

