"""Core structures: ordered attribute-carrying DAG trees, alphabet, rules, constraints.

Trees are immutable and may share subtrees (DAG). Structural cycles are
impossible by construction (nodes are frozen) and additionally rejected by
``validate_dag`` for defensively crafted objects.
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Callable, Dict, List, Mapping, Optional, Sequence, Tuple


class TreeAutomatonError(Exception):
    """Base class for all errors raised by this package."""


class StructuralCycleError(TreeAutomatonError):
    """A structural cycle was detected in a tree/DAG."""


class AlphabetError(TreeAutomatonError):
    """A node does not conform to the explicitly given alphabet."""


class RuleError(TreeAutomatonError):
    """A rule or predicate reference is malformed."""


class CancelledError(TreeAutomatonError):
    """Evaluation was cancelled through a CancelToken."""


class ResourceLimitError(TreeAutomatonError):
    """A configured resource limit was exceeded during evaluation."""


class NoProofError(TreeAutomatonError):
    """A proof was requested for a state that has no support."""


Path = Tuple[int, ...]


@dataclass(frozen=True)
class Node:
    """Immutable ordered tree node. ``attrs`` is a sorted tuple of pairs so
    nodes stay hashable; structurally equal nodes are the same cache key."""

    label: str
    children: Tuple["Node", ...] = ()
    attrs: Tuple[Tuple[str, object], ...] = ()

    @staticmethod
    def make(label: str, children: Sequence["Node"] = (),
             attrs: Optional[Mapping[str, object]] = None) -> "Node":
        return Node(label, tuple(children), tuple(sorted((attrs or {}).items())))

    @property
    def arity(self) -> int:
        return len(self.children)

    def attr(self, name: str, default: object = None) -> object:
        for key, value in self.attrs:
            if key == name:
                return value
        return default

    def attrs_dict(self) -> Dict[str, object]:
        return dict(self.attrs)

    def with_changes(self, label=None, children=None, attrs=None) -> "Node":
        return Node(
            self.label if label is None else label,
            self.children if children is None else tuple(children),
            self.attrs if attrs is None else tuple(sorted(attrs.items())),
        )


def postorder_unique(root: Node) -> List[Node]:
    """All distinct reachable nodes, children before parents, each once."""
    seen = set()
    order: List[Node] = []
    stack = [(root, False)]
    while stack:
        node, expanded = stack.pop()
        if expanded:
            order.append(node)
            continue
        if node in seen:
            continue
        seen.add(node)
        stack.append((node, True))
        for child in reversed(node.children):
            stack.append((child, False))
    return order


def validate_dag(root: Node) -> bool:
    """Raise StructuralCycleError if the reachable object graph has a cycle.

    Identity-based (not hash-based) so even maliciously crafted cyclic
    objects are detected instead of recursing forever.
    """
    WHITE, GRAY, BLACK = 0, 1, 2
    color: Dict[int, int] = {id(root): GRAY}
    stack = [(root, iter(root.children))]
    while stack:
        node, it = stack[-1]
        descended = False
        for child in it:
            state = color.get(id(child), WHITE)
            if state == GRAY:
                raise StructuralCycleError(
                    f"structural cycle detected through node {child.label!r}")
            if state == WHITE:
                color[id(child)] = GRAY
                stack.append((child, iter(child.children)))
                descended = True
                break
        if not descended:
            color[id(node)] = BLACK
            stack.pop()
    return True


def occurrence_map(root: Node) -> Dict[Node, List[Path]]:
    """Map each distinct node to every root-to-node path where it occurs."""
    occ: Dict[Node, List[Path]] = {}
    stack: List[Tuple[Node, Path]] = [(root, ())]
    while stack:
        node, path = stack.pop()
        occ.setdefault(node, []).append(path)
        for index, child in enumerate(node.children):
            stack.append((child, path + (index,)))
    for paths in occ.values():
        paths.sort()
    return occ


@dataclass(frozen=True)
class Alphabet:
    """Explicit alphabet: label -> allowed arities. A label with several
    arities is fine, but rules and nodes always match one exact arity."""

    arities: Mapping[str, frozenset]

    @staticmethod
    def of(mapping: Mapping[str, Sequence[int]]) -> "Alphabet":
        return Alphabet({label: frozenset(ars) for label, ars in mapping.items()})

    def allows(self, label: str, arity: int) -> bool:
        return arity in self.arities.get(label, frozenset())

    def validate_tree(self, root: Node) -> None:
        validate_dag(root)
        for node, paths in occurrence_map(root).items():
            if not self.allows(node.label, node.arity):
                raise AlphabetError(
                    f"node {node.label!r}/{node.arity} at path {paths[0]} "
                    f"is not in the alphabet")


PredicateFn = Callable[[object, object], bool]


class PredicateRegistry:
    """Named attribute predicates so rules and counterexamples stay
    serializable. A predicate maps (attribute_value, argument) -> bool."""

    def __init__(self) -> None:
        self._fns: Dict[str, PredicateFn] = {}

    def register(self, name: str, fn: PredicateFn) -> PredicateFn:
        self._fns[name] = fn
        return fn

    def get(self, name: str) -> PredicateFn:
        try:
            return self._fns[name]
        except KeyError:
            raise RuleError(f"unknown attribute predicate {name!r}") from None

    def names(self) -> List[str]:
        return sorted(self._fns)


DEFAULT_PREDICATES = PredicateRegistry()
for _name, _fn in {
    "eq": lambda v, a: v == a,
    "ne": lambda v, a: v != a,
    "lt": lambda v, a: v is not None and v < a,
    "le": lambda v, a: v is not None and v <= a,
    "gt": lambda v, a: v is not None and v > a,
    "ge": lambda v, a: v is not None and v >= a,
    "in": lambda v, a: v in a,
    "present": lambda v, a: v is not None,
}.items():
    DEFAULT_PREDICATES.register(_name, _fn)


@dataclass(frozen=True)
class AttrCondition:
    """Attribute condition on a rule: attribute ``name`` must satisfy the
    registered ``predicate`` with ``argument`` (JSON-able for export)."""

    name: str
    predicate: str
    argument: object = None

    def check(self, attrs: Mapping[str, object],
              registry: PredicateRegistry = DEFAULT_PREDICATES) -> bool:
        return registry.get(self.predicate)(attrs.get(self.name), self.argument)

    def to_spec(self) -> dict:
        return {"name": self.name, "predicate": self.predicate,
                "argument": self.argument}

    @staticmethod
    def from_spec(spec: Mapping) -> "AttrCondition":
        return AttrCondition(spec["name"], spec["predicate"],
                             spec.get("argument"))


@dataclass(frozen=True)
class Rule:
    """Transition rule: a node with ``label``/``arity`` whose children carry
    ``child_states`` (position-wise) and whose attributes satisfy
    ``conditions`` may additionally carry state ``result``."""

    rid: str
    label: str
    arity: int
    child_states: Tuple[str, ...]
    result: str
    conditions: Tuple[AttrCondition, ...] = ()

    @staticmethod
    def of(rid: str, label: str, child_states: Sequence[str], result: str,
           conditions: Sequence[AttrCondition] = ()) -> "Rule":
        child_states = tuple(child_states)
        return Rule(rid, label, len(child_states), child_states, result,
                    tuple(conditions))

    def matches(self, node: Node, registry: PredicateRegistry) -> bool:
        # Same label with a different arity never mixes with this rule.
        if node.label != self.label or node.arity != self.arity:
            return False
        return all(cond.check(node.attrs_dict(), registry)
                   for cond in self.conditions)

    def to_spec(self) -> dict:
        return {"rid": self.rid, "label": self.label,
                "child_states": list(self.child_states), "result": self.result,
                "conditions": [c.to_spec() for c in self.conditions]}

    @staticmethod
    def from_spec(spec: Mapping) -> "Rule":
        child_states = tuple(spec["child_states"])
        return Rule(spec["rid"], spec["label"], len(child_states), child_states,
                    spec["result"],
                    tuple(AttrCondition.from_spec(c)
                          for c in spec.get("conditions", ())))


@dataclass(frozen=True)
class Violation:
    kind: str
    detail: str
    node: Optional[Node] = None


class StateConstraint:
    """Global constraint checked against the derived state facts."""

    kind = "constraint"

    def check(self, facts: Mapping[Node, frozenset]) -> List[Violation]:
        raise NotImplementedError

    def to_spec(self) -> dict:
        raise NotImplementedError


@dataclass(frozen=True)
class MutexConstraint(StateConstraint):
    """No node may carry both states at once."""

    state_a: str
    state_b: str
    kind = "mutex"

    def check(self, facts):
        out = []
        for node, states in facts.items():
            if self.state_a in states and self.state_b in states:
                out.append(Violation(
                    self.kind,
                    f"node {node.label!r}/{node.arity} carries both "
                    f"{self.state_a} and {self.state_b}", node))
        return out

    def to_spec(self):
        return {"kind": self.kind, "state_a": self.state_a,
                "state_b": self.state_b}


@dataclass(frozen=True)
class MaxCountConstraint(StateConstraint):
    """At most ``maximum`` distinct nodes may carry ``state``."""

    state: str
    maximum: int
    kind = "max_count"

    def check(self, facts):
        holders = [n for n, s in facts.items() if self.state in s]
        if len(holders) > self.maximum:
            return [Violation(
                self.kind,
                f"state {self.state} held by {len(holders)} nodes "
                f"(maximum {self.maximum})")]
        return []

    def to_spec(self):
        return {"kind": self.kind, "state": self.state, "maximum": self.maximum}


def constraint_from_spec(spec: Mapping) -> StateConstraint:
    kind = spec["kind"]
    if kind == "mutex":
        return MutexConstraint(spec["state_a"], spec["state_b"])
    if kind == "max_count":
        return MaxCountConstraint(spec["state"], spec["maximum"])
    raise RuleError(f"unknown constraint kind {kind!r}")


class Automaton:
    """Bottom-up ordered tree automaton with explicit alphabet, transition
    rules, accepting states and global state constraints."""

    def __init__(self, alphabet: Alphabet, states: Sequence[str],
                 rules: Sequence[Rule], accepting: Sequence[str],
                 constraints: Sequence[StateConstraint] = (),
                 registry: PredicateRegistry = DEFAULT_PREDICATES) -> None:
        self.alphabet = alphabet
        self.states = frozenset(states)
        self.rules = tuple(rules)
        self.accepting = frozenset(accepting)
        self.constraints = tuple(constraints)
        self.registry = registry
        self._validate()
        self._by_signature: Dict[Tuple[str, int], List[Rule]] = {}
        for rule in self.rules:
            self._by_signature.setdefault((rule.label, rule.arity),
                                          []).append(rule)

    def _validate(self) -> None:
        seen = set()
        for rule in self.rules:
            if rule.rid in seen:
                raise RuleError(f"duplicate rule id {rule.rid!r}")
            seen.add(rule.rid)
            if not self.alphabet.allows(rule.label, rule.arity):
                raise RuleError(
                    f"rule {rule.rid!r}: {rule.label!r}/{rule.arity} "
                    f"not in alphabet")
            if len(rule.child_states) != rule.arity:
                raise RuleError(
                    f"rule {rule.rid!r}: {len(rule.child_states)} child states "
                    f"for arity {rule.arity}")
            unknown = (set(rule.child_states) | {rule.result}) - self.states
            if unknown:
                raise RuleError(
                    f"rule {rule.rid!r}: unknown states {sorted(unknown)}")
            for cond in rule.conditions:
                self.registry.get(cond.predicate)  # raises RuleError if missing
        if not self.accepting <= self.states:
            raise RuleError("accepting states must be declared states")

    def rules_for(self, label: str, arity: int) -> Tuple[Rule, ...]:
        return tuple(self._by_signature.get((label, arity), ()))

    def without_rules(self, rids: Sequence[str]) -> "Automaton":
        rids = set(rids)
        return Automaton(self.alphabet, self.states,
                         [r for r in self.rules if r.rid not in rids],
                         self.accepting, self.constraints, self.registry)

    def with_rules(self, extra: Sequence[Rule]) -> "Automaton":
        return Automaton(self.alphabet, self.states,
                         list(self.rules) + list(extra),
                         self.accepting, self.constraints, self.registry)

    def to_spec(self) -> dict:
        return {
            "alphabet": {label: sorted(ars)
                         for label, ars in sorted(self.alphabet.arities.items())},
            "states": sorted(self.states),
            "accepting": sorted(self.accepting),
            "rules": [r.to_spec() for r in self.rules],
            "constraints": [c.to_spec() for c in self.constraints],
        }

    @staticmethod
    def from_spec(spec: Mapping,
                  registry: PredicateRegistry = DEFAULT_PREDICATES) -> "Automaton":
        return Automaton(
            Alphabet.of(spec["alphabet"]),
            spec["states"],
            [Rule.from_spec(r) for r in spec["rules"]],
            spec["accepting"],
            [constraint_from_spec(c) for c in spec.get("constraints", ())],
            registry,
        )
