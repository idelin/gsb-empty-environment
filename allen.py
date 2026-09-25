"""Allen interval algebra, path consistency, and exact integer witness search.

The module uses only the Python standard library.  Atomic Allen relations are
represented by their usual one/two-character symbols.
"""

from __future__ import annotations

from collections import deque
from itertools import product
from dataclasses import dataclass
from datetime import datetime, timezone
from typing import Iterable, Optional


RELATIONS = (
    "<", ">", "m", "mi", "o", "oi", "s", "si", "d", "di", "f", "fi", "=",
)
RELATION_INDEX = {symbol: index for index, symbol in enumerate(RELATIONS)}
_INVERSE_INDEX = (1, 0, 3, 2, 5, 4, 11, 10, 9, 8, 7, 6, 12)
ALL_RELATIONS_MASK = (1 << len(RELATIONS)) - 1
EQUAL_RELATION = "="
EQUAL_MASK = 1 << RELATION_INDEX[EQUAL_RELATION]


def relation_mask(relations: Iterable[str]) -> int:
    mask = 0
    for relation in relations:
        mask |= 1 << _relation_index(relation)
    return mask


def relations_in(mask: int) -> tuple[str, ...]:
    return tuple(
        RELATIONS[index]
        for index in range(len(RELATIONS))
        if mask & (1 << index)
    )


def format_relations(mask: int) -> str:
    return "".join(relations_in(mask)) or "{}"


def inverse_mask(mask: int) -> int:
    result = 0
    for index, relation in enumerate(RELATIONS):
        if mask & (1 << index):
            result |= 1 << _INVERSE_INDEX[index]
    return result


def _relation_index(relation: str) -> int:
    try:
        return RELATION_INDEX[relation]
    except KeyError as exc:
        raise ValueError(f"unknown Allen relation: {relation!r}") from exc


def _relation_between_integer(left: tuple[int, int], right: tuple[int, int]) -> str:
    a0, a1 = left
    b0, b1 = right
    if a1 < b0:
        return "<"
    if a0 > b1:
        return ">"
    if a1 == b0:
        return "m"
    if a0 == b1:
        return "mi"
    if a0 == b0 and a1 == b1:
        return "="
    if a0 < b0 and b0 < a1 < b1:
        return "o"
    if b0 < a0 < b1 and b1 < a1:
        return "oi"
    if a0 == b0 and a1 < b1:
        return "s"
    if a0 == b0 and b1 < a1:
        return "si"
    if b0 < a0 and a1 < b1:
        return "d"
    if a0 < b0 and b1 < a1:
        return "di"
    if b0 < a0 and a1 == b1:
        return "f"
    if a0 < b0 and a1 == b1:
        return "fi"
    raise AssertionError("unreachable endpoint ordering")


def _build_synthetic_composition_table() -> tuple[tuple[frozenset[str], ...], ...]:
    """Synthesize composition from all six-endpoint weak orders.

    Each endpoint receives an offset rank in 0..5.  Repeated ranks model
    endpoint equality; ranks strictly order every remaining endpoint.  The
    enumeration therefore covers exactly the endpoint interpretations that can
    realize each pair of atomic Allen relations.
    """
    table = [
        [set() for _ in RELATIONS]
        for _ in RELATIONS
    ]
    for a0, a1, b0, b1, c0, c1 in product(range(6), repeat=6):
        if not (a0 < a1 and b0 < b1 and c0 < c1):
            continue
        first = _relation_between_integer((a0, a1), (b0, b1))
        second = _relation_between_integer((b0, b1), (c0, c1))
        composed = _relation_between_integer((a0, a1), (c0, c1))
        table[RELATION_INDEX[first]][RELATION_INDEX[second]].add(composed)
    return tuple(tuple(frozenset(cell) for cell in row) for row in table)


COMPOSITION_TABLE = _build_synthetic_composition_table()


def compose(first_mask: int, second_mask: int) -> int:
    result = 0
    for first_index, first in enumerate(RELATIONS):
        if not first_mask & (1 << first_index):
            continue
        for second_index, second in enumerate(RELATIONS):
            if second_mask & (1 << second_index):
                result |= relation_mask(COMPOSITION_TABLE[first_index][second_index])
    return result


class Duration:
    """An exact signed duration measured in integer nanoseconds."""

    __slots__ = ("nanos",)

    def __init__(self, nanos: int) -> None:
        self.nanos = int(nanos)

    @classmethod
    def seconds(cls, value: int = 0) -> "Duration":
        return cls(int(value) * 1_000_000_000)

    def __eq__(self, other: object) -> bool:
        return isinstance(other, Duration) and self.nanos == other.nanos

    def __lt__(self, other: "Duration") -> bool:
        return self.nanos < other.nanos

    def __le__(self, other: "Duration") -> bool:
        return self.nanos <= other.nanos

    def __add__(self, other: "Duration") -> "Duration":
        return Duration(self.nanos + other.nanos)

    def __sub__(self, other: "Duration") -> "Duration":
        return Duration(self.nanos - other.nanos)

    def __neg__(self) -> "Duration":
        return Duration(-self.nanos)

    def __repr__(self) -> str:
        return f"Duration(nanos={self.nanos})"


class TimePoint:
    """An exact instant on a monotonic integer time line."""

    __slots__ = ("nanos",)

    def __init__(self, nanos: int) -> None:
        self.nanos = int(nanos)

    @classmethod
    def epoch(cls) -> "TimePoint":
        return cls(0)

    def __eq__(self, other: object) -> bool:
        return isinstance(other, TimePoint) and self.nanos == other.nanos

    def __lt__(self, other: "TimePoint") -> bool:
        return self.nanos < other.nanos

    def __le__(self, other: "TimePoint") -> bool:
        return self.nanos <= other.nanos

    def __add__(self, duration: Duration) -> "TimePoint":
        if not isinstance(duration, Duration):
            return NotImplemented
        return TimePoint(self.nanos + duration.nanos)

    def __sub__(self, other):
        if isinstance(other, TimePoint):
            return Duration(self.nanos - other.nanos)
        if isinstance(other, Duration):
            return TimePoint(self.nanos - other.nanos)
        return NotImplemented

    def __repr__(self) -> str:
        return f"TimePoint(nanos={self.nanos})"


class WallClock:
    """A timezone-aware civil timestamp, distinct from a monotonic time point."""

    __slots__ = ("_datetime",)

    def __init__(self, value: datetime) -> None:
        if value.tzinfo is None:
            raise ValueError("WallClock requires a timezone-aware datetime")
        self._datetime = value

    @classmethod
    def utc(cls, year: int, month: int, day: int, hour: int = 0,
            minute: int = 0, second: int = 0) -> "WallClock":
        return cls(datetime(year, month, day, hour, minute, second,
                            tzinfo=timezone.utc))

    @property
    def datetime(self) -> datetime:
        return self._datetime

    def as_utc(self) -> "WallClock":
        return WallClock(self._datetime.astimezone(timezone.utc))

    def to_time_point(self, epoch: datetime | None = None) -> TimePoint:
        origin = epoch or datetime(1970, 1, 1, tzinfo=timezone.utc)
        if origin.tzinfo is None:
            raise ValueError("epoch must be timezone-aware")
        delta = self._datetime.astimezone(timezone.utc) - origin
        return TimePoint(delta.days * 86_400_000_000_000
                         + delta.seconds * 1_000_000_000
                         + delta.microseconds * 1_000)

    def __eq__(self, other: object) -> bool:
        return (isinstance(other, WallClock)
                and self._datetime == other._datetime)

    def __lt__(self, other: "WallClock") -> bool:
        return self._datetime < other._datetime

    def __repr__(self) -> str:
        return f"WallClock({self._datetime.isoformat()!r})"


@dataclass(frozen=True)
class Interval:
    start: TimePoint
    end: TimePoint

    def __post_init__(self) -> None:
        if not self.start < self.end:
            raise ValueError("interval start must be strictly less than end")

    @property
    def duration(self) -> Duration:
        return self.end - self.start

    def relation_to(self, other: "Interval") -> str:
        return relation_between_time_points(self.start, self.end,
                                            other.start, other.end)


@dataclass(frozen=True)
class IntegerInterval:
    start: int
    end: int

    def __post_init__(self) -> None:
        if self.start >= self.end:
            raise ValueError("interval start must be strictly less than end")

    def as_interval(self) -> Interval:
        return Interval(TimePoint(self.start), TimePoint(self.end))


def relation_between_time_points(a0: TimePoint, a1: TimePoint,
                                 b0: TimePoint, b1: TimePoint) -> str:
    return _relation_between_integer((a0.nanos, a1.nanos), (b0.nanos, b1.nanos))


def relation_between_integer_intervals(left: IntegerInterval,
                                       right: IntegerInterval) -> str:
    return _relation_between_integer((left.start, left.end),
                                     (right.start, right.end))


@dataclass(frozen=True)
class RevisionEvidence:
    revision_id: int
    reason: str
    removed: tuple[str, ...]
    path: Optional[tuple[int, int, int]] = None
    supports: frozenset[tuple[str, str, str]] = frozenset()


@dataclass(frozen=True)
class Conflict:
    kind: str
    message: str
    i: Optional[int] = None
    j: Optional[int] = None
    k: Optional[int] = None
    left_relations: tuple[str, ...] = ()
    right_relations: tuple[str, ...] = ()
    supported: tuple[str, ...] = ()
    current: tuple[str, ...] = ()
    previous_evidence: Optional[RevisionEvidence] = None

    def explanation(self) -> str:
        return self.message


@dataclass(frozen=True)
class PropagationResult:
    status: str
    conflict: Optional[Conflict] = None
    revisions: int = 0
    processed_arcs: int = 0

    @property
    def is_consistent(self) -> bool:
        return self.status == "consistent"

    @property
    def budget_exhausted(self) -> bool:
        return self.status == "budget_exhausted"


@dataclass
class _Snapshot:
    domains: tuple[int, ...]
    queue: tuple[tuple[int, int], ...]
    queued: frozenset[tuple[int, int]]
    evidence: dict[tuple[int, int], RevisionEvidence]
    revision_counter: int
    original: tuple[tuple[int, int, int], ...]
    processed_arcs: int


class IntervalNetwork:
    def __init__(self, size: int) -> None:
        self.size = size
        self._domains = [ALL_RELATIONS_MASK] * (size * size)
        for index in range(size):
            self._domains[index * size + index] = EQUAL_MASK
        self._queue: deque[tuple[int, int]] = deque()
        self._queued: set[tuple[int, int]] = set()
        self._evidence: dict[tuple[int, int], RevisionEvidence] = {}
        self._snapshots: list[_Snapshot] = []
        self._revision_counter = 0
        self._original: list[tuple[int, int, int]] = []
        self.processed_arcs = 0
        self._last_revised = False

    @classmethod
    def from_constraints(
        cls,
        size: int,
        constraints: Iterable[tuple[int, int, Iterable[str] | int]],
    ) -> "IntervalNetwork":
        network = cls(size)
        for i, j, relations in constraints:
            mask = relations if isinstance(relations, int) else relation_mask(relations)
            network.restrict(i, j, mask)
        return network

    def clone(self) -> "IntervalNetwork":
        cloned = IntervalNetwork(self.size)
        cloned._domains = self._domains.copy()
        cloned._queue = deque(self._queue)
        cloned._queued = set(self._queued)
        cloned._evidence = self._evidence.copy()
        cloned._snapshots = []
        cloned._revision_counter = self._revision_counter
        cloned._original = self._original.copy()
        cloned.processed_arcs = self.processed_arcs
        cloned._last_revised = False
        return cloned

    def relations(self, i: int, j: int) -> tuple[str, ...]:
        return relations_in(self._domains[self._index(i, j)])

    def mask(self, i: int, j: int) -> int:
        return self._domains[self._index(i, j)]

    def evidence(self, i: int, j: int) -> Optional[RevisionEvidence]:
        return self._evidence.get((i, j))

    @property
    def pending_arcs(self) -> tuple[tuple[int, int], ...]:
        return tuple(self._queue)

    def push(self) -> None:
        self._snapshots.append(_Snapshot(
            domains=tuple(self._domains),
            queue=tuple(self._queue),
            queued=frozenset(self._queued),
            evidence=self._evidence.copy(),
            revision_counter=self._revision_counter,
            original=tuple(self._original),
            processed_arcs=self.processed_arcs,
        ))

    def pop(self) -> None:
        if not self._snapshots:
            raise IndexError("cannot pop without a matching push")
        snapshot = self._snapshots.pop()
        self._domains = list(snapshot.domains)
        self._queue = deque(snapshot.queue)
        self._queued = set(snapshot.queued)
        self._evidence = snapshot.evidence.copy()
        self._revision_counter = snapshot.revision_counter
        self._original = list(snapshot.original)
        self.processed_arcs = snapshot.processed_arcs

    def discard_choice_point(self) -> None:
        if not self._snapshots:
            raise IndexError("cannot discard without a matching push")
        self._snapshots.pop()

    def restrict(self, i: int, j: int,
                 relations: Iterable[str] | int) -> Optional[Conflict]:
        allowed = relations if isinstance(relations, int) else relation_mask(relations)
        self._original.append((i, j, allowed))
        conflict = self._revise(i, j, allowed, reason="input", path=None)
        if conflict is not None:
            return conflict
        if i != j:
            return self._revise(j, i, inverse_mask(allowed), reason="input",
                                path=None)
        return None

    def propagate(self, budget: Optional[int] = None) -> PropagationResult:
        if budget is not None and budget < 0:
            raise ValueError("budget must be non-negative or None")
        revisions = 0
        processed = 0
        while self._queue:
            if budget is not None and processed >= budget:
                return PropagationResult("budget_exhausted", revisions=revisions,
                                         processed_arcs=processed)
            i, k = self._queue.popleft()
            self._queued.discard((i, k))
            processed += 1
            self.processed_arcs += 1
            for middle in range(self.size):
                if middle == i or middle == k:
                    continue
                outgoing = compose(self.mask(i, k), self.mask(k, middle))
                conflict = self._revise(
                    i, middle, outgoing, reason="path", path=(i, k, middle)
                )
                if conflict is not None:
                    return PropagationResult("conflict", conflict=conflict,
                                             revisions=revisions,
                                             processed_arcs=processed)
                if self._last_revised:
                    revisions += 1
                incoming = compose(self.mask(middle, i), self.mask(i, k))
                conflict = self._revise(
                    middle, k, incoming, reason="path", path=(middle, i, k)
                )
                if conflict is not None:
                    return PropagationResult("conflict", conflict=conflict,
                                             revisions=revisions,
                                             processed_arcs=processed)
                if self._last_revised:
                    revisions += 1
        return PropagationResult("consistent", revisions=revisions,
                                 processed_arcs=processed)

    def _index(self, i: int, j: int) -> int:
        if not (0 <= i < self.size and 0 <= j < self.size):
            raise IndexError("node index outside network")
        return i * self.size + j

    def _enqueue(self, arc: tuple[int, int]) -> None:
        if arc not in self._queued:
            self._queued.add(arc)
            self._queue.append(arc)

    def _revise(self, i: int, j: int, allowed: int, *, reason: str,
                path: Optional[tuple[int, int, int]]) -> Optional[Conflict]:
        self._last_revised = False
        index = self._index(i, j)
        current = self._domains[index]
        updated = current & allowed
        if updated == current:
            return None
        if updated == 0:
            supports = frozenset()
            return self._path_conflict(i, j, path, allowed, current, supports)
        removed = tuple(
            relation for relation in RELATIONS
            if current & (1 << RELATION_INDEX[relation])
            and not updated & (1 << RELATION_INDEX[relation])
        )
        supports = self._supports(i, j, updated, path) if path else frozenset()
        self._revision_counter += 1
        self._evidence[(i, j)] = RevisionEvidence(
            revision_id=self._revision_counter,
            reason=reason,
            removed=removed,
            path=path,
            supports=supports,
        )
        self._domains[index] = updated
        self._last_revised = True
        self._enqueue((i, j))
        if i != j:
            inverse_updated = inverse_mask(updated)
            inverse_current = self._domains[self._index(j, i)]
            if inverse_updated != inverse_current:
                inverse_removed = tuple(
                    relation for relation in RELATIONS
                    if inverse_current & (1 << RELATION_INDEX[relation])
                    and not inverse_updated & (1 << RELATION_INDEX[relation])
                )
                self._revision_counter += 1
                self._evidence[(j, i)] = RevisionEvidence(
                    revision_id=self._revision_counter,
                    reason=reason,
                    removed=inverse_removed,
                    path=None if path is None else (path[2], path[1], path[0]),
                    supports=frozenset(),
                )
                self._domains[self._index(j, i)] = inverse_updated
                self._enqueue((j, i))
        return None

    def _supports(self, i: int, j: int, updated: int,
                  path: Optional[tuple[int, int, int]]) -> frozenset[tuple[str, str, str]]:
        if path is None:
            return frozenset()
        _, middle, _ = path
        supports = []
        for result in RELATIONS:
            result_bit = 1 << RELATION_INDEX[result]
            if not updated & result_bit:
                continue
            for left in self.relations(i, middle):
                for right in self.relations(middle, j):
                    if result_bit & relation_mask(
                        COMPOSITION_TABLE[RELATION_INDEX[left]][RELATION_INDEX[right]]
                    ):
                        supports.append((result, left, right))
                        break
                else:
                    continue
                break
        return frozenset(supports)

    def _path_conflict(self, i: int, j: int,
                       path: Optional[tuple[int, int, int]],
                       supported: int, current: int,
                       supports: frozenset) -> Conflict:
        if path is None:
            message = (
                f"direct restriction emptied R[{i},{j}]: "
                f"{format_relations(current)} ∩ allowed = ∅"
            )
            conflict = Conflict(
                kind="direct",
                message=message,
                i=i,
                j=j,
                supported=relations_in(supported),
                current=relations_in(current),
                previous_evidence=self._evidence.get((i, j)),
            )
        else:
            _, middle, _ = path
            message = (
                f"path conflict on {i}->{middle}->{j}: "
                f"{format_relations(self.mask(i, middle))} ∘ "
                f"{format_relations(self.mask(middle, j))} = "
                f"{format_relations(supported)} has no relation left in "
                f"{format_relations(current)}"
            )
            conflict = Conflict(
                kind="path",
                message=message,
                i=i,
                j=j,
                k=middle,
                left_relations=self.relations(i, middle),
                right_relations=self.relations(middle, j),
                supported=relations_in(supported),
                current=relations_in(current),
                previous_evidence=self._evidence.get((i, j)),
            )
        return conflict


@dataclass(frozen=True)
class FailedBranch:
    assignments: tuple[tuple[int, int, str], ...]
    conflict: Conflict


@dataclass(frozen=True)
class SearchResult:
    status: str
    witness: Optional[dict[int, IntegerInterval]] = None
    conflict: Optional[Conflict] = None
    branches_explored: int = 0
    failed_branches: tuple[FailedBranch, ...] = ()
    reason: str = ""

    @property
    def is_satisfiable(self) -> bool:
        return self.status == "satisfiable"

    @property
    def is_unsatisfiable(self) -> bool:
        return self.status == "unsatisfiable"

    @property
    def is_unknown(self) -> bool:
        return self.status == "unknown"


def _choose_branch_pair(network: IntervalNetwork) -> Optional[tuple[int, int]]:
    best: Optional[tuple[int, int]] = None
    best_size = 14
    for i in range(network.size):
        for j in range(i + 1, network.size):
            mask = network.mask(i, j)
            size = mask.bit_count()
            if 1 < size < best_size:
                best_size = size
                best = (i, j)
    return best


class _DisjointSet:
    def __init__(self, size: int) -> None:
        self.parent = list(range(size))
        self.rank = [0] * size

    def find(self, node: int) -> int:
        while self.parent[node] != node:
            self.parent[node] = self.parent[self.parent[node]]
            node = self.parent[node]
        return node

    def union(self, left: int, right: int) -> None:
        left_root = self.find(left)
        right_root = self.find(right)
        if left_root == right_root:
            return
        if self.rank[left_root] < self.rank[right_root]:
            left_root, right_root = right_root, left_root
        self.parent[right_root] = left_root
        if self.rank[left_root] == self.rank[right_root]:
            self.rank[left_root] += 1


def _endpoint_index(node: int, endpoint: int, size: int) -> int:
    return 2 * node + endpoint


def _add_atomic_order_relation(constraints, relation: str, i: int, j: int,
                               size: int) -> None:
    ai = _endpoint_index(i, 0, size)
    bi = _endpoint_index(i, 1, size)
    ci = _endpoint_index(j, 0, size)
    di = _endpoint_index(j, 1, size)
    equal = constraints[0]
    strict = constraints[1]
    if relation == "<":
        strict.append((bi, ci))
    elif relation == ">":
        strict.append((di, ai))
    elif relation == "m":
        equal.append((bi, ci))
    elif relation == "mi":
        equal.append((di, ai))
    elif relation == "o":
        strict.extend([(ai, ci), (ci, bi), (bi, di)])
    elif relation == "oi":
        strict.extend([(ci, ai), (ai, di), (di, bi)])
    elif relation == "s":
        equal.append((ai, ci))
        strict.append((bi, di))
    elif relation == "si":
        equal.append((ai, ci))
        strict.append((di, bi))
    elif relation == "d":
        strict.extend([(ci, ai), (bi, di)])
    elif relation == "di":
        strict.extend([(ai, ci), (di, bi)])
    elif relation == "f":
        strict.extend([(ci, ai), (bi, di)])
        equal.append((bi, di))
    elif relation == "fi":
        strict.extend([(ai, ci), (bi, di)])
        equal.append((bi, di))
    elif relation == "=":
        equal.extend([(ai, ci), (bi, di)])
    else:
        raise AssertionError(f"non-atomic branch relation: {relation}")


def _integer_witness_from_atomic(
    network: IntervalNetwork,
) -> tuple[Optional[dict[int, IntegerInterval]], Optional[Conflict]]:
    size = network.size
    equalities: list[tuple[int, int]] = []
    strict_edges: list[tuple[int, int]] = []
    assignments: list[tuple[int, int, str]] = []
    for node in range(size):
        strict_edges.append((
            _endpoint_index(node, 0, size),
            _endpoint_index(node, 1, size),
        ))
    for i in range(size):
        for j in range(i + 1, size):
            relations = network.relations(i, j)
            if len(relations) != 1:
                return None, Conflict("non_atomic",
                                      f"R[{i},{j}] is still disjunctive")
            relation = relations[0]
            assignments.append((i, j, relation))
            _add_atomic_order_relation((equalities, strict_edges), relation,
                                       i, j, size)
    dsu = _DisjointSet(2 * size)
    for left, right in equalities:
        dsu.union(left, right)
    classes = sorted({dsu.find(node) for node in range(2 * size)})
    class_index = {node: index for index, node in enumerate(classes)}
    graph: dict[int, set[int]] = {index: set() for index in range(len(classes))}
    indegree = [0] * len(classes)
    for source, target in strict_edges:
        source_class = class_index[dsu.find(source)]
        target_class = class_index[dsu.find(target)]
        if source_class == target_class:
            return None, Conflict(
                "endpoint_cycle",
                "strict endpoint ordering collapsed through equalities for "
                f"path {source} < {target}; assignments="
                + ", ".join(f"{i}{relation}{j}" for i, j, relation in assignments),
            )
        if target_class not in graph[source_class]:
            graph[source_class].add(target_class)
            indegree[target_class] += 1
    queue = deque(node for node, degree in enumerate(indegree) if degree == 0)
    ranks: dict[int, int] = {}
    rank = 0
    while queue:
        node = queue.popleft()
        ranks[node] = rank
        rank += 1
        for target in graph[node]:
            indegree[target] -= 1
            if indegree[target] == 0:
                queue.append(target)
    if len(ranks) != len(classes):
        return None, Conflict(
            "endpoint_cycle",
            "endpoint strict-order graph contains a directed cycle",
        )
    witness = {}
    for node in range(size):
        start = ranks[class_index[dsu.find(_endpoint_index(node, 0, size))]]
        end = ranks[class_index[dsu.find(_endpoint_index(node, 1, size))]]
        witness[node] = IntegerInterval(start, end)
    return witness, None


def find_integer_witness(
    network: IntervalNetwork,
    *,
    branch_budget: Optional[int] = None,
    propagation_budget: Optional[int] = None,
) -> SearchResult:
    if branch_budget is not None and branch_budget < 0:
        raise ValueError("branch_budget must be non-negative or None")
    working = network.clone()
    initial = working.propagate(propagation_budget)
    if initial.budget_exhausted:
        return SearchResult("unknown", reason="initial propagation budget exhausted")
    if initial.conflict is not None:
        return SearchResult(
            "unsatisfiable",
            conflict=initial.conflict,
            reason="path consistency already proved a conflict",
        )

    failed: list[FailedBranch] = []
    branches = 0
    assignments: list[tuple[int, int, str]] = []
    budget_reason = ""

    def backtrack() -> Optional[dict[int, IntegerInterval]]:
        nonlocal branches
        pair = _choose_branch_pair(working)
        if pair is None:
            witness, conflict = _integer_witness_from_atomic(working)
            if witness is not None:
                return witness
            if conflict is not None:
                failed.append(FailedBranch(tuple(assignments), conflict))
            return None
        if branch_budget is not None and branches >= branch_budget:
            return None
        i, j = pair
        choices = working.relations(i, j)
        for relation in choices:
            if branch_budget is not None and branches >= branch_budget:
                return None
            branches += 1
            working.push()
            assignments.append((i, j, relation))
            direct_conflict = working.restrict(i, j, relation)
            result = None
            if direct_conflict is None:
                result = working.propagate(propagation_budget)
                if result.budget_exhausted:
                    budget_reason = (
                        "propagation budget exhausted after "
                        f"{branches} branches"
                    )
                    return None
                if result.conflict is not None:
                    direct_conflict = result.conflict
            if direct_conflict is None:
                witness = backtrack()
                if witness is not None:
                    return witness
            else:
                failed.append(FailedBranch(tuple(assignments), direct_conflict))
            working.pop()
            assignments.pop()
        return None

    witness = backtrack()
    if witness is not None:
        return SearchResult("satisfiable", witness=witness,
                            branches_explored=branches)
    if budget_reason:
        return SearchResult(
            "unknown",
            branches_explored=branches,
            failed_branches=tuple(failed),
            reason=budget_reason,
        )
    if branch_budget is not None and branches >= branch_budget:
        return SearchResult(
            "unknown",
            branches_explored=branches,
            failed_branches=tuple(failed),
            reason=f"branch budget exhausted after {branches} branches",
        )
    if propagation_budget is not None:
        pending = len(working.pending_arcs)
        if pending:
            return SearchResult(
                "unknown",
                branches_explored=branches,
                failed_branches=tuple(failed),
                reason=f"propagation budget exhausted with {pending} pending arcs",
            )
    proof = Conflict(
        "global_exhaustion",
        f"exhausted all {branches} atomic relation branches; every branch "
        "ends in a path or endpoint-order conflict",
    )
    return SearchResult(
        "unsatisfiable",
        conflict=proof,
        branches_explored=branches,
        failed_branches=tuple(failed),
        reason="complete bounded atomic-network search found no global witness",
    )


def exhaustive_integer_witness(
    network: IntervalNetwork,
    *,
    max_endpoint: Optional[int] = None,
) -> Optional[dict[int, IntegerInterval]]:
    """Enumerate actual small integer intervals as an independent cross-check."""
    size = network.size
    if max_endpoint is None:
        max_endpoint = 2 * size - 1
    intervals = [
        IntegerInterval(start, end)
        for start in range(max_endpoint + 1)
        for end in range(start + 1, max_endpoint + 1)
    ]
    assignment: dict[int, IntegerInterval] = {}
    domains = {(i, j): network.mask(i, j)
               for i in range(size) for j in range(size)}

    def assigned_consistent(node: int, candidate: IntegerInterval) -> bool:
        for other, other_interval in assignment.items():
            left_relation = relation_between_integer_intervals(candidate,
                                                               other_interval)
            right_relation = relation_between_integer_intervals(other_interval,
                                                                candidate)
            if not domains[(node, other)] & (1 << RELATION_INDEX[left_relation]):
                return False
            if not domains[(other, node)] & (1 << RELATION_INDEX[right_relation]):
                return False
        return True

    def choose_node() -> Optional[int]:
        remaining = [node for node in range(size) if node not in assignment]
        if not remaining:
            return None

        def score(node: int) -> tuple[int, int]:
            assigned_neighbors = sum(
                1 for other in assignment
                if domains[(node, other)] != ALL_RELATIONS_MASK
            )
            return (-assigned_neighbors, node)

        return min(remaining, key=score)

    def backtrack() -> Optional[dict[int, IntegerInterval]]:
        node = choose_node()
        if node is None:
            return dict(assignment)
        for candidate in intervals:
            if assigned_consistent(node, candidate):
                assignment[node] = candidate
                witness = backtrack()
                if witness is not None:
                    return witness
                del assignment[node]
        return None

    return backtrack()
