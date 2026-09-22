"""Bottom-up evaluation engine.

Two semantics are provided, sharing the exact same rule-firing code:

* :meth:`RuleIndex.fire` performs one derivation step at one node.
* :func:`least_fixpoint` iterates synchronous rounds from the empty
  assignment.  Because the start value is the empty set of states, only states
  with a finite derivation tree exist in the result: a rule cycle with no
  nullary ground state cannot create states out of nothing.
"""
from __future__ import annotations

import threading
import time
from dataclasses import dataclass, field
from typing import Callable, Iterable, Mapping, Sequence

from .dag import Dag, Node
from .model import Rule

# (node_id, head_state, rule_name, ((child_id, child_state), ...))
Proof = tuple[int, str, str, tuple[tuple[int, str], ...]]


class AutomatonError(RuntimeError):
    pass


class LimitExceeded(AutomatonError):
    """Evaluation exceeded a configured resource budget."""


class CancellationError(AutomatonError):
    """Evaluation was cancelled through a :class:`CancelToken`."""


@dataclass(slots=True)
class Limits:
    max_firings: int | None = None
    """Maximum number of rule-match attempts across the whole operation."""
    max_rounds: int | None = None
    """Maximum number of fixpoint rounds."""
    max_evidence: int = 1024
    """Maximum number of evidence trees expanded by one ``explain`` call."""
    deadline: float | None = None
    """Optional absolute monotonic deadline (``time.monotonic`` seconds)."""


@dataclass(slots=True)
class Stats:
    firings: int = 0
    rounds: int = 0
    derived: int = 0
    retracted: int = 0

    def reset(self) -> None:
        self.firings = self.rounds = self.derived = self.retracted = 0


class CancelToken:
    """Cooperative cancellation token (thread-safe)."""

    def __init__(self) -> None:
        self._event = threading.Event()

    def cancel(self) -> None:
        self._event.set()

    @property
    def cancelled(self) -> bool:
        return self._event.is_set()


class CountdownCancel(CancelToken):
    """Cancels after ``after`` observed :meth:`tick` calls (deterministic)."""

    def __init__(self, after: int) -> None:
        super().__init__()
        self._remaining = after

    def tick(self) -> None:
        self._remaining -= 1
        if self._remaining <= 0:
            self.cancel()


def check_budget(
    limits: Limits | None, token: CancelToken | None, stats: Stats
) -> None:
    if isinstance(token, CountdownCancel):
        token.tick()
    if token is not None and token.cancelled:
        raise CancellationError("evaluation cancelled")
    if limits is not None:
        if limits.max_firings is not None and stats.firings > limits.max_firings:
            raise LimitExceeded(f"max_firings={limits.max_firings} exceeded")
        if limits.deadline is not None and time.monotonic() > limits.deadline:
            raise LimitExceeded("deadline exceeded")


class RuleIndex:
    """Rules grouped by ``(label, arity)`` so lookups are direct."""

    def __init__(self, rules: Iterable[Rule] = ()) -> None:
        self._rules: dict[tuple[str, int], list[Rule]] = {}
        for rule in rules:
            self.add(rule)

    def add(self, rule: Rule) -> None:
        bucket = self._rules.setdefault((rule.label, rule.arity), [])
        if not any(r.name == rule.name for r in bucket):
            bucket.append(rule)

    def remove(self, name: str) -> Rule | None:
        for key, bucket in list(self._rules.items()):
            for pos, rule in enumerate(bucket):
                if rule.name == name:
                    del bucket[pos]
                    if not bucket:
                        del self._rules[key]
                    return rule
        return None

    def get(self, name: str) -> Rule | None:
        for bucket in self._rules.values():
            for rule in bucket:
                if rule.name == name:
                    return rule
        return None

    def names(self) -> tuple[str, ...]:
        return tuple(r.name for b in self._rules.values() for r in b)

    def candidates(self, node: Node) -> tuple[Rule, ...]:
        return tuple(self._rules.get((node.label, node.arity), ()))

    def fire(
        self,
        node_id: int,
        node: Node,
        states: Mapping[int, frozenset[str]],
        stats: Stats,
        limits: Limits | None = None,
        token: CancelToken | None = None,
    ) -> tuple[frozenset[str], tuple[Proof, ...]]:
        """Return every state this node derives in one evaluation step."""
        new_states: set[str] = set()
        proofs: list[Proof] = []
        child_state_sets = [states.get(ch, frozenset()) for ch in node.children]
        attrs = node.attr_map()
        for rule in self.candidates(node):
            check_budget(limits, token, stats)
            stats.firings += 1
            if not rule.condition.holds(attrs):
                continue
            kids: list[tuple[int, str]] = []
            ok = True
            for pos, wanted in enumerate(rule.child_states):
                chosen = sorted(wanted & child_state_sets[pos])
                if not chosen:
                    ok = False
                    break
                kids.append((node.children[pos], chosen[0]))
            if ok:
                new_states.add(rule.state)
                proofs.append((node_id, rule.state, rule.name, tuple(kids)))
        return frozenset(new_states), tuple(proofs)


def least_fixpoint(
    dag: Dag,
    rules: RuleIndex | Sequence[Rule],
    *,
    limits: Limits | None = None,
    token: CancelToken | None = None,
    stats: Stats | None = None,
) -> dict[int, tuple[frozenset[str], tuple[Proof, ...]]]:
    """Synchronous-round least fixpoint starting from the empty assignment.

    One round fires every rule at every node against the state snapshot at the
    round start, so iteration order cannot manufacture states.  A pure rule
    cycle never grounded by a nullary rule yields nothing.
    """
    if isinstance(rules, RuleIndex):
        index = rules
    else:
        from .model import Rule as _Rule
        index = RuleIndex(
            r if isinstance(r, Rule) else _Rule.parse(r) for r in rules
        )
    stats = stats or Stats()
    limits = limits or Limits()
    ids = dag.ids()
    states: dict[int, frozenset[str]] = {i: frozenset() for i in ids}
    proofs: dict[int, dict[str, list[Proof]]] = {i: {} for i in ids}
    while True:
        if limits.max_rounds is not None and stats.rounds >= limits.max_rounds:
            raise LimitExceeded(f"max_rounds={limits.max_rounds} exceeded")
        snapshot = {i: states[i] for i in ids}
        changed = False
        stats.rounds += 1
        for nid in ids:
            node = dag.node(nid)
            derived, new_proofs = index.fire(
                nid, node, snapshot, stats, limits, token
            )
            if derived - states[nid]:
                stats.derived += len(derived - states[nid])
                states[nid] = states[nid] | derived
                changed = True
            for proof in new_proofs:
                head = proof[1]
                known_rules = {pp[2] for pp in proofs[nid].get(head, ())}
                if proof[2] not in known_rules:
                    proofs[nid].setdefault(head, []).append(proof)
        if not changed:
            break
    return {
        nid: (states[nid], tuple(p for ps in proofs[nid].values() for p in ps))
        for nid in ids
    }


# --------------------------------------------------------------------- evidence
@dataclass(frozen=True, slots=True)
class Evidence:
    """A concrete acceptance witness rooted at one occurrence of a subtree."""

    node_id: int
    state: str
    rule: str
    path: tuple[int | str, ...]
    children: tuple["Evidence", ...] = field(default_factory=tuple)

    @property
    def path_str(self) -> str:
        return "$" + "".join(
            f"[{p}]" if isinstance(p, int) else f".{p}" for p in self.path
        )

    def leaves(self) -> tuple["Evidence", ...]:
        if not self.children:
            return (self,)
        out: list[Evidence] = []
        for child in self.children:
            out.extend(child.leaves())
        return tuple(out)


Table = Mapping[int, tuple[frozenset[str], Sequence[Proof]]]


def explain(
    dag: Dag,
    node_id: int,
    state: str,
    table: Table,
    *,
    path: tuple[int | str, ...] = (),
    cap: int = 1024,
) -> tuple[Evidence, ...]:
    """Enumerate all proof trees for ``state`` at this occurrence.

    ``path`` is the occurrence path from the query root.  The same shared
    subtree reached through two parents is expanded with two distinct paths, so
    the returned witnesses never conflate occurrences.
    """
    out: list[Evidence] = []
    budget = cap

    def head_proofs(nid: int, wanted: str) -> list[Proof]:
        states_set, proof_list = table[nid]
        if wanted not in states_set:
            return []
        return [p for p in proof_list if p[1] == wanted]

    def expand(nid: int, wanted: str, cur_path: tuple, sink: list[Evidence]) -> None:
        nonlocal budget
        for proof in head_proofs(nid, wanted):
            _, _, rule_name, kids = proof
            child_options: list[list[Evidence]] = []
            for idx, (cid, cstate) in enumerate(kids):
                opts: list[Evidence] = []
                expand(cid, cstate, cur_path + (idx,), opts)
                if not opts:
                    break
                child_options.append(opts)
            else:
                combine(proof, child_options, 0, [], cur_path, sink)

    def combine(proof, options, idx, chosen, cur_path, sink) -> None:
        nonlocal budget
        if budget <= 0:
            raise LimitExceeded(f"max_evidence={cap} exceeded")
        nid = proof[0]
        wanted = proof[1]
        rule_name = proof[2]
        if idx == len(options):
            budget -= 1
            sink.append(
                Evidence(nid, wanted, rule_name, cur_path, tuple(chosen))
            )
            return
        for ev in options[idx]:
            chosen.append(ev)
            combine(proof, options, idx + 1, chosen, cur_path, sink)
            chosen.pop()

    expand(node_id, state, tuple(path), out)
    return tuple(out)
