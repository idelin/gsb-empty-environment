"""Least-fixpoint evaluation engine, run snapshots and path-evidence proofs.

Semantics: states are derived bottom-up as the *least* fixpoint of the rule
system. Cyclic rule schemas (state cycles across rules) converge; a set of
states that only supports itself cyclically, with no base rule ever firing,
is never derived ("no root support => not self-sustaining").
"""

from __future__ import annotations

from dataclasses import dataclass, field
from typing import Callable, Dict, List, Mapping, Optional, Sequence, Set, Tuple

from .core import (Automaton, Node, NoProofError, Path, ResourceLimitError,
                   CancelledError, Violation, occurrence_map, postorder_unique)


@dataclass(frozen=True)
class Limits:
    """Resource bounds for evaluation."""
    max_steps: int = 100_000          # (node, rule) applications
    max_iterations: int = 1_000       # fixpoint sweeps
    max_facts: int = 200_000          # derived (node, state) pairs
    max_derivations_per_fact: int = 32


class CancelToken:
    """Cooperative cancellation: call ``cancel()`` from any thread/hook;
    the engine raises CancelledError at the next step boundary."""

    __slots__ = ("_flag",)

    def __init__(self) -> None:
        self._flag = False

    def cancel(self) -> None:
        self._flag = True

    @property
    def cancelled(self) -> bool:
        return self._flag


@dataclass(frozen=True)
class Derivation:
    """One support for a (node, state) fact: a rule plus the child states
    it consumed (position-wise, matching the node's children)."""
    rid: str
    child_states: Tuple[str, ...]


@dataclass
class EvalStats:
    steps: int = 0
    iterations: int = 0
    node_evaluations: Dict[Node, int] = field(default_factory=dict)

    def evaluations_of(self, node: Node) -> int:
        return self.node_evaluations.get(node, 0)


Fact = Tuple[Node, str]
Facts = Dict[Node, Set[str]]
Derivations = Dict[Fact, List[Derivation]]

StepHook = Optional[Callable[[int, Node, object], None]]


def compute(automaton: Automaton, nodes: Sequence[Node], *,
            facts: Optional[Facts] = None,
            derivations: Optional[Derivations] = None,
            dirty: Optional[Set[Node]] = None,
            limits: Optional[Limits] = None,
            token: Optional[CancelToken] = None,
            on_step: StepHook = None) -> Tuple[Facts, Derivations, EvalStats]:
    """Run the least-fixpoint computation over ``nodes`` (postorder).

    If ``dirty`` is given, only those nodes are recomputed (from scratch,
    using the cached facts of their unchanged children); otherwise every
    node is computed. ``facts``/``derivations`` are updated in place when
    provided, enabling incremental maintenance.
    """
    limits = limits or Limits()
    facts = facts if facts is not None else {}
    derivations = derivations if derivations is not None else {}
    stats = EvalStats()

    if dirty is None:
        work = list(nodes)
        for node in work:
            facts.setdefault(node, set())
    else:
        work = [n for n in nodes if n in dirty]
        for node in work:
            facts[node] = set()
            for key in [k for k in derivations if k[0] == node]:
                del derivations[key]

    def fact_count() -> int:
        return sum(len(s) for s in facts.values())

    iteration = 0
    changed = True
    while changed:
        iteration += 1
        if iteration > limits.max_iterations:
            raise ResourceLimitError(
                f"fixpoint did not converge within {limits.max_iterations} "
                f"iterations")
        changed = False
        for node in work:
            for rule in automaton.rules_for(node.label, node.arity):
                stats.steps += 1
                stats.node_evaluations[node] = \
                    stats.node_evaluations.get(node, 0) + 1
                if stats.steps > limits.max_steps:
                    raise ResourceLimitError(
                        f"step limit {limits.max_steps} exceeded")
                if token is not None and token.cancelled:
                    raise CancelledError(
                        f"cancelled after {stats.steps} steps")
                if on_step is not None:
                    on_step(stats.steps, node, rule)
                if not rule.matches(node, automaton.registry):
                    continue
                supported = all(
                    need in facts.get(child, ())
                    for child, need in zip(node.children, rule.child_states))
                if not supported:
                    continue
                target = facts[node]
                if rule.result not in target:
                    target.add(rule.result)
                    changed = True
                    if fact_count() > limits.max_facts:
                        raise ResourceLimitError(
                            f"fact limit {limits.max_facts} exceeded")
                key = (node, rule.result)
                dlist = derivations.setdefault(key, [])
                deriv = Derivation(rule.rid, rule.child_states)
                if deriv not in dlist and \
                        len(dlist) < limits.max_derivations_per_fact:
                    dlist.append(deriv)
    stats.iterations = iteration
    return facts, derivations, stats


@dataclass(frozen=True)
class Proof:
    """Evidence that ``state`` holds at the occurrence ``path`` of ``node``.
    Two occurrences of a shared subtree yield proofs with different paths."""
    path: Path
    node: Node
    state: str
    rule_id: str
    children: Tuple["Proof", ...] = ()

    def render(self, indent: int = 0) -> str:
        line = (f"{'  ' * indent}{self.state} @ path={self.path} "
                f"via {self.rule_id} [{self.node.label}/{self.node.arity}]")
        return "\n".join([line] + [c.render(indent + 1)
                                   for c in self.children])


class RunResult:
    """Immutable snapshot of one evaluation. Safe to query after later
    edits of the originating session."""

    def __init__(self, automaton: Automaton, root: Node,
                 facts: Mapping[Node, Set[str]],
                 derivations: Mapping[Fact, Sequence[Derivation]],
                 violations: Sequence[Violation],
                 stats: Optional[EvalStats] = None) -> None:
        self.automaton = automaton
        self.root = root
        self._facts: Dict[Node, frozenset] = {
            n: frozenset(s) for n, s in facts.items()}
        self._derivations: Dict[Fact, Tuple[Derivation, ...]] = {
            k: tuple(v) for k, v in derivations.items()}
        self.violations: Tuple[Violation, ...] = tuple(violations)
        self.stats = stats
        self._occ: Optional[Dict[Node, List[Path]]] = None

    @property
    def root_states(self) -> frozenset:
        return self._facts.get(self.root, frozenset())

    @property
    def accepted(self) -> bool:
        return (not self.violations
                and bool(self.root_states & self.automaton.accepting))

    def states_of(self, node: Node) -> frozenset:
        return self._facts.get(node, frozenset())

    def facts_view(self) -> Dict[Node, frozenset]:
        return dict(self._facts)

    def node_at(self, path: Path) -> Node:
        node = self.root
        for index in path:
            node = node.children[index]
        return node

    def states_at(self, path: Path) -> frozenset:
        return self.states_of(self.node_at(path))

    def occurrences(self, node: Optional[Node] = None):
        if self._occ is None:
            self._occ = occurrence_map(self.root)
        if node is None:
            return {n: tuple(p) for n, p in self._occ.items()}
        return tuple(self._occ.get(node, ()))

    def derivations_of(self, node: Node, state: str) -> Tuple[Derivation, ...]:
        return self._derivations.get((node, state), ())

    def proof(self, state: str, path: Path = ()) -> Proof:
        node = self.node_at(path)
        ders = self._derivations.get((node, state))
        if not ders:
            raise NoProofError(
                f"state {state!r} has no support at path {path}")
        return self._build(node, state, ders[0], path)

    def _build(self, node: Node, state: str, deriv: Derivation,
               path: Path) -> Proof:
        children = []
        for index, child_state in enumerate(deriv.child_states):
            child = node.children[index]
            cders = self._derivations.get((child, child_state))
            if not cders:
                raise NoProofError(
                    f"broken support: {child_state!r} at path {path + (index,)}")
            children.append(
                self._build(child, child_state, cders[0], path + (index,)))
        return Proof(path=path, node=node, state=state,
                     rule_id=deriv.rid, children=tuple(children))


def check_constraints(automaton: Automaton,
                      facts: Mapping[Node, Set[str]]) -> List[Violation]:
    out: List[Violation] = []
    for constraint in automaton.constraints:
        out.extend(constraint.check(facts))
    return out


def evaluate(automaton: Automaton, root: Node, *,
             limits: Optional[Limits] = None,
             token: Optional[CancelToken] = None,
             on_step: StepHook = None,
             validate: bool = True) -> RunResult:
    """One-shot full evaluation of ``root``."""
    if validate:
        automaton.alphabet.validate_tree(root)
    nodes = postorder_unique(root)
    facts, derivations, stats = compute(
        automaton, nodes, limits=limits, token=token, on_step=on_step)
    return RunResult(automaton, root, facts, derivations,
                     check_constraints(automaton, facts), stats)
