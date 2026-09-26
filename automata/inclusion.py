"""Language inclusion L(A) ⊆ L(B) via on-demand product-graph BFS.

The product of the two lazily determinized NFAs is explored level by level,
so the first counterexample found is a *shortest* distinguishing string.
Every step of the counterexample carries evidence: the concrete symbol, the
partition atom it was picked from, and the original NFA edges used on both
sides.  A state budget bounds the search; exhaustion yields UNDECIDED and
the search can be paused (``step``) or serialized and resumed (``snapshot``
/ ``restore``).
"""
from __future__ import annotations

from collections import deque
from dataclasses import dataclass
from enum import Enum
from typing import Deque, Dict, FrozenSet, List, Optional, Tuple

from .determinize import LazyDFA
from .nfa import Edge, NFA
from .symbols import Interval, find_atom, refine_atoms

Node = Tuple[FrozenSet[int], FrozenSet[int]]


class Status(Enum):
    RUNNING = "running"
    INCLUDED = "included"            # L(A) ⊆ L(B) proved
    NOT_INCLUDED = "not_included"    # counterexample found
    UNDECIDED = "undecided"          # state budget exhausted


@dataclass(frozen=True)
class StepEvidence:
    symbol: int                      # concrete symbol chosen for the word
    atom: Interval                   # partition atom the symbol was picked from
    edges_a: Tuple[Edge, ...]        # original A-edges taken at this step
    edges_b: Tuple[Edge, ...]        # original B-edges taken at this step


@dataclass(frozen=True)
class Counterexample:
    word: Tuple[int, ...]
    steps: Tuple[StepEvidence, ...]
    final_a: FrozenSet[int]
    final_b: FrozenSet[int]


def _matching_edges(nfa: NFA, subset: FrozenSet[int], atom: Interval) -> Tuple[Edge, ...]:
    out = [
        e
        for s in subset
        for e in nfa.edges.get(s, ())
        if e.symbols.intersects_interval(atom)
    ]
    out.sort(key=lambda e: e.id)
    return tuple(out)


class InclusionSearch:
    """Resumable BFS over the product graph of two :class:`LazyDFA`."""

    def __init__(
        self,
        dfa_a: LazyDFA,
        dfa_b: LazyDFA,
        budget: int = 10_000,
        product_cache: Optional[dict] = None,
    ):
        self.dfa_a = dfa_a
        self.dfa_b = dfa_b
        self.budget = budget
        self.expanded = 0
        start: Node = (dfa_a.initial(), dfa_b.initial())
        self._frontier: Deque[Node] = deque([start])
        self._visited: set[Node] = {start}
        self._parent: Dict[Node, Optional[Tuple[Node, StepEvidence]]] = {start: None}
        self.status = Status.RUNNING
        self.counterexample: Optional[Counterexample] = None
        # node -> (deps_a, deps_b, expansions); shared/pruned by the checker
        self._product_cache = product_cache if product_cache is not None else {}

    # -- expansion ---------------------------------------------------------
    def _expand(self, node: Node) -> List[Tuple[Interval, Node, StepEvidence]]:
        cached = self._product_cache.get(node)
        if cached is not None:
            return cached[2]
        sa, sb = node
        ia = self.dfa_a.info(sa)
        ib = self.dfa_b.info(sb)
        # Unify the two alphabets' boundaries before pairing transitions.
        atoms = refine_atoms(ia.atoms, ib.atoms)
        expansions: List[Tuple[Interval, Node, StepEvidence]] = []
        for atom in atoms:
            aa = find_atom(ia.atoms, atom)
            ab = find_atom(ib.atoms, atom)
            # Missing transitions (or symbols outside a side's alphabet)
            # fall into the implicit dead state: the empty subset.
            na = ia.moves[aa] if aa is not None else frozenset()
            nb = ib.moves[ab] if ab is not None else frozenset()
            ev = StepEvidence(
                symbol=atom.lo,
                atom=atom,
                edges_a=_matching_edges(self.dfa_a.nfa, sa, atom),
                edges_b=_matching_edges(self.dfa_b.nfa, sb, atom),
            )
            expansions.append((atom, (na, nb), ev))
        self._product_cache[node] = (ia.deps, ib.deps, expansions)
        return expansions

    # -- search ------------------------------------------------------------
    def step(self, max_expansions: int = 1) -> Status:
        """Expand at most ``max_expansions`` product states, then pause."""
        if self.status in (Status.INCLUDED, Status.NOT_INCLUDED):
            return self.status
        self.status = Status.RUNNING
        while max_expansions > 0:
            if not self._frontier:
                self.status = Status.INCLUDED
                return self.status
            if self.expanded >= self.budget:
                self.status = Status.UNDECIDED
                return self.status
            node = self._frontier.popleft()
            sa, sb = node
            if self.dfa_a.info(sa).is_final and not self.dfa_b.info(sb).is_final:
                self.status = Status.NOT_INCLUDED
                self.counterexample = self._build_counterexample(node)
                return self.status
            self.expanded += 1
            max_expansions -= 1
            for _atom, nxt, ev in self._expand(node):
                if nxt not in self._visited:
                    self._visited.add(nxt)
                    self._parent[nxt] = (node, ev)
                    self._frontier.append(nxt)
        return self.status

    def run(self) -> Status:
        """Run until a verdict or until the budget is exhausted."""
        return self.step(1 << 62)

    def _build_counterexample(self, node: Node) -> Counterexample:
        steps: List[StepEvidence] = []
        cur = node
        while True:
            rec = self._parent[cur]
            if rec is None:
                break
            parent, ev = rec
            steps.append(ev)
            cur = parent
        steps.reverse()
        return Counterexample(
            word=tuple(ev.symbol for ev in steps),
            steps=tuple(steps),
            final_a=node[0],
            final_b=node[1],
        )

    # -- pause / resume ------------------------------------------------------
    def snapshot(self) -> dict:
        """Serialize the paused search to a JSON-compatible dict."""
        nodes = list(self._visited)
        index = {n: i for i, n in enumerate(nodes)}

        def enc_node(n: Node):
            return [sorted(n[0]), sorted(n[1])]

        def enc_ev(ev: StepEvidence):
            return {
                "symbol": ev.symbol,
                "atom": [ev.atom.lo, ev.atom.hi],
                "edges_a": [e.id for e in ev.edges_a],
                "edges_b": [e.id for e in ev.edges_b],
            }

        parents = []
        for child, rec in self._parent.items():
            if rec is None:
                parents.append([index[child], -1, None])
            else:
                parent, ev = rec
                parents.append([index[child], index[parent], enc_ev(ev)])
        return {
            "budget": self.budget,
            "expanded": self.expanded,
            "status": self.status.value,
            "nodes": [enc_node(n) for n in nodes],
            "frontier": [index[n] for n in self._frontier],
            "parents": parents,
        }

    @classmethod
    def restore(
        cls,
        dfa_a: LazyDFA,
        dfa_b: LazyDFA,
        snap: dict,
        product_cache: Optional[dict] = None,
    ) -> "InclusionSearch":
        """Resume a search previously serialized with :meth:`snapshot`."""
        status = Status(snap["status"])
        if status in (Status.INCLUDED, Status.NOT_INCLUDED):
            raise ValueError("cannot restore a finished search")
        search = cls(dfa_a, dfa_b, budget=snap["budget"], product_cache=product_cache)
        nodes: List[Node] = [
            (frozenset(a), frozenset(b)) for a, b in snap["nodes"]
        ]
        search._visited = set(nodes)
        search._frontier = deque(nodes[i] for i in snap["frontier"])
        search._parent = {}
        for child_i, parent_i, ev_d in snap["parents"]:
            child = nodes[child_i]
            if parent_i < 0:
                search._parent[child] = None
            else:
                ev = StepEvidence(
                    symbol=ev_d["symbol"],
                    atom=Interval(*ev_d["atom"]),
                    edges_a=tuple(dfa_a.nfa.edge_by_id(i) for i in ev_d["edges_a"]),
                    edges_b=tuple(dfa_b.nfa.edge_by_id(i) for i in ev_d["edges_b"]),
                )
                search._parent[child] = (nodes[parent_i], ev)
        search.expanded = snap["expanded"]
        search.status = status
        return search


@dataclass(frozen=True)
class InclusionResult:
    status: Status
    counterexample: Optional[Counterexample]
    expanded: int


class InclusionChecker:
    """Owns the lazy DFAs and the product cache for a pair of NFAs.

    After an edge edit, only the cache entries depending on the touched
    states are dropped; the inclusion *conclusion* is never reused across
    a structural change and is always recomputed.
    """

    def __init__(self, nfa_a: NFA, nfa_b: NFA, budget: int = 10_000):
        self.nfa_a = nfa_a
        self.nfa_b = nfa_b
        self.dfa_a = LazyDFA(nfa_a)
        self.dfa_b = LazyDFA(nfa_b)
        self.budget = budget
        self.product_cache: dict = {}
        self.invalidation_log: List[dict] = []
        self._versions: Optional[Tuple[int, int]] = None
        self._search: Optional[InclusionSearch] = None
        self._result: Optional[InclusionResult] = None

    def _sync(self) -> None:
        versions = (self.nfa_a.version, self.nfa_b.version)
        if versions == self._versions:
            return
        dirty_a = set(self.nfa_a.dirty)
        dirty_b = set(self.nfa_b.dirty)
        self.nfa_a.dirty.clear()
        self.nfa_b.dirty.clear()
        if dirty_a:
            self.dfa_a.invalidate(dirty_a)
        if dirty_b:
            self.dfa_b.invalidate(dirty_b)
        removed = 0
        if self.product_cache:
            if not dirty_a and not dirty_b and self._versions is not None:
                # Version changed without dirty-state info: be conservative.
                removed = len(self.product_cache)
                self.product_cache.clear()
            else:
                doomed = [
                    n
                    for n, (da, db, _exp) in self.product_cache.items()
                    if (da & dirty_a) or (db & dirty_b)
                ]
                for n in doomed:
                    del self.product_cache[n]
                removed = len(doomed)
        if self._versions is not None:
            self.invalidation_log.append(
                {
                    "dirty_a": sorted(dirty_a),
                    "dirty_b": sorted(dirty_b),
                    "removed": removed,
                    "survived": len(self.product_cache),
                }
            )
        self._versions = versions
        self._search = InclusionSearch(
            self.dfa_a, self.dfa_b, self.budget, self.product_cache
        )
        self._result = None  # old inclusion conclusion is never reused

    @property
    def search(self) -> InclusionSearch:
        self._sync()
        assert self._search is not None
        return self._search

    def check(self) -> InclusionResult:
        self._sync()
        if self._result is None:
            assert self._search is not None
            status = self._search.run()
            self._result = InclusionResult(
                status=status,
                counterexample=self._search.counterexample,
                expanded=self._search.expanded,
            )
        return self._result
