"""Minimum-cost transduction over an explicitly defined finite-state transducer.

A transducer consumes input symbols and emits output strings; every edge
carries a non-negative integer cost.  No regular-expression or automata
libraries are used: the alphabet and the transition structure are given
explicitly and the search is implemented from scratch (heapq only).

Ordering / tie-breaking
-----------------------
Paths are ordered by the key

    (total_cost, output_length, output_string, edge_id_sequence)

i.e. first by total cost, then by output length, then lexicographically by
the output string, and finally by the sequence of edge ids.  This order is
*well-founded* on achievable outputs even in the presence of zero-cost
cycles:

* costs are non-negative integers, so the first component can only decrease
  finitely often;
* for a fixed cost, the output length is a non-negative integer and can only
  decrease finitely often;
* for a fixed cost and length there are only finitely many strings of that
  length, so the lexicographic component can only decrease finitely often.

This is exactly why "output length" must precede "lexicographic": a
zero-cost cycle that emits output in the middle of a string (e.g. emitting
"a" before a final "b" gives "b", "ab", "aab", ...) would otherwise produce
an infinite strictly descending lexicographic chain with no minimum.

Termination argument
--------------------
* best(): Dijkstra over states.  Every edge relaxation strictly increases
  the key (the edge-id sequence grows, and a sequence is always
  lexicographically smaller than any proper extension of itself), so each
  state is settled at most once and stale heap entries are skipped.  Finite
  states => termination.  Zero-cost epsilon cycles and zero-cost
  empty-output cycles cannot improve any key, hence cannot cause
  re-settling.
* k_best(): best-first enumeration of paths.  Two safeguards:
    1. Dominated zero-cycle pruning: an edge with cost 0 and empty output
       leading to a state already on the current path is never extended.
       Such a cycle changes neither cost nor output, and the shorter prefix
       path has a strictly smaller edge-id sequence, so every continuation
       of the pruned path is dominated by the corresponding continuation of
       the prefix path.  This makes graphs whose only cycles are zero-cost
       empty-output (epsilon) cycles finitely enumerable.
    2. Budget: the number of heap pops is bounded.  If a reachable
       zero-cost cycle emits non-empty output on the way to a final state,
       infinitely many distinct outputs exist and no finite enumeration can
       be complete; the budget guarantees termination and the result keeps
       the remaining frontier so the search can be resumed/inspected.
* brute_force_best(): enumerates simple paths only.  Any accepting path can
  be reduced to a simple path by removing cycles; removing a cycle never
  increases cost or output length, and when it increases neither, the
  removed cycle had zero cost and empty output, so the output string is
  unchanged.  Hence the minimum over simple paths equals the minimum over
  all paths, and the enumeration is finite.
"""

from __future__ import annotations

from dataclasses import dataclass
from heapq import heappop, heappush
from itertools import count

# Default pop budget for k_best.  Enumeration cost is O(budget x path
# depth); on graphs with infinitely many paths (any reachable productive
# cycle) a runaway search is bounded by this budget.
DEFAULT_BUDGET = 10_000


@dataclass(frozen=True)
class Edge:
    """One transition.  ``inp is None`` means epsilon (no input consumed)."""

    id: int
    src: str
    dst: str
    inp: str | None
    out: str
    cost: int


@dataclass(frozen=True)
class Step:
    """One edge traversal inside a path evidence."""

    edge_id: int
    src: str
    dst: str
    inp: str | None
    out: str
    cost: int


@dataclass
class Evidence:
    """Proof witness for one transduction result: the exact edge sequence."""

    steps: list[Step]

    @property
    def total_cost(self) -> int:
        return sum(s.cost for s in self.steps)

    @property
    def output(self) -> str:
        return "".join(s.out for s in self.steps)

    @property
    def input_consumed(self) -> list[str]:
        return [s.inp for s in self.steps if s.inp is not None]

    @property
    def edge_ids(self) -> tuple[int, ...]:
        return tuple(s.edge_id for s in self.steps)

    def key(self) -> tuple:
        return (self.total_cost, len(self.output), self.output, self.edge_ids)

    def render(self) -> str:
        lines = [
            f"total_cost={self.total_cost} output={self.output!r} "
            f"input_consumed={self.input_consumed} edge_ids={list(self.edge_ids)}"
        ]
        for s in self.steps:
            inp = "eps" if s.inp is None else s.inp
            lines.append(
                f"  {s.src} -[e{s.edge_id} in={inp} out={s.out!r} cost={s.cost}]-> {s.dst}"
            )
        if not self.steps:
            lines.append("  (empty path: initial state is accepting)")
        return "\n".join(lines)


@dataclass
class KBestResult:
    """Result of enumerating the first K distinct outputs."""

    evidences: list[Evidence]
    complete: bool  # False iff the pop budget was exhausted before finishing
    pops: int
    budget: int
    frontier_size: int  # candidates still in the heap when enumeration stopped
    frontier_sample: list  # keys of the best remaining candidates (the frontier)


class Transducer:
    def __init__(
        self,
        states,
        alphabet,
        edges,
        initial,
        finals,
    ):
        self.states = set(states)
        self.alphabet = set(alphabet)
        self.initial = initial
        self.finals = set(finals)
        self.edges = list(edges)

        if self.initial not in self.states:
            raise ValueError(f"unknown initial state {self.initial!r}")
        unknown = self.finals - self.states
        if unknown:
            raise ValueError(f"unknown final states {sorted(unknown)!r}")
        seen_ids = set()
        self.adj: dict[str, list[Edge]] = {s: [] for s in self.states}
        for e in self.edges:
            if e.id in seen_ids:
                raise ValueError(f"duplicate edge id {e.id}")
            seen_ids.add(e.id)
            if e.src not in self.states or e.dst not in self.states:
                raise ValueError(f"edge {e.id} references unknown state")
            if e.cost < 0:
                raise ValueError(f"edge {e.id} has negative cost {e.cost}")
            if e.inp is not None and e.inp not in self.alphabet:
                raise ValueError(f"edge {e.id} input {e.inp!r} not in alphabet")
            for ch in e.out:
                if ch not in self.alphabet:
                    raise ValueError(
                        f"edge {e.id} output symbol {ch!r} not in alphabet"
                    )
            self.adj[e.src].append(e)
        for lst in self.adj.values():
            lst.sort(key=lambda e: e.id)
        self._by_id = {e.id: e for e in self.edges}

    # ------------------------------------------------------------------ #
    # helpers
    # ------------------------------------------------------------------ #
    def _evidence(self, edge_ids: tuple[int, ...]) -> Evidence:
        steps = []
        for eid in edge_ids:
            e = self._by_id[eid]
            steps.append(Step(e.id, e.src, e.dst, e.inp, e.out, e.cost))
        return Evidence(steps)

    @staticmethod
    def _extend_key(key, edge: Edge, ids: tuple[int, ...]) -> tuple:
        cost, out_len, out, _ = key
        new_ids = ids + (edge.id,)
        return (cost + edge.cost, out_len + len(edge.out), out + edge.out, new_ids)

    # ------------------------------------------------------------------ #
    # single best transduction (Dijkstra over states)
    # ------------------------------------------------------------------ #
    def best(self) -> Evidence | None:
        """Return the minimum-key accepting path, or None if none exists."""
        start_key = (0, 0, "", ())
        dist = {self.initial: start_key}
        ids_of = {self.initial: ()}
        heap = [(start_key, self.initial)]
        settled = set()
        while heap:
            key, state = heappop(heap)
            if state in settled:
                continue  # stale heap entry
            settled.add(state)
            if state in self.finals:
                return self._evidence(ids_of[state])
            for e in self.adj[state]:
                if e.dst in settled:
                    continue
                nkey = self._extend_key(key, e, ids_of[state])
                if e.dst not in dist or nkey < dist[e.dst]:
                    dist[e.dst] = nkey
                    ids_of[e.dst] = ids_of[state] + (e.id,)
                    heappush(heap, (nkey, e.dst))
        return None

    # ------------------------------------------------------------------ #
    # first K distinct outputs (best-first path enumeration)
    # ------------------------------------------------------------------ #
    def k_best(self, k: int, budget: int = DEFAULT_BUDGET) -> KBestResult:
        """Enumerate the first ``k`` distinct outputs in key order.

        Multiple paths producing the same output are counted once (the
        first, i.e. minimum-key, occurrence is kept).  If the pop budget is
        exhausted before ``k`` outputs are found or the search space is
        drained, the remaining frontier is preserved in the result.
        """
        if k < 1:
            raise ValueError("k must be >= 1")
        evidences: list[Evidence] = []
        seen_outputs = set()
        counter = count()
        start_key = (0, 0, "", ())
        # heap entry: (key, unique_counter, state, edge_ids, states_on_path)
        heap = [(start_key, next(counter), self.initial, (), (self.initial,))]
        pops = 0
        complete = False
        while heap and len(evidences) < k:
            if pops >= budget:
                break
            key, _, state, ids, path_states = heappop(heap)
            pops += 1
            if state in self.finals:
                out = key[2]
                if out not in seen_outputs:
                    seen_outputs.add(out)
                    evidences.append(self._evidence(ids))
            for e in self.adj[state]:
                if e.cost == 0 and not e.out and e.dst in path_states:
                    # Dominated zero-cost empty-output (epsilon) cycle:
                    # skipping it cannot remove any distinct output or any
                    # minimal key; see module docstring.
                    continue
                nkey = self._extend_key(key, e, ids)
                heappush(
                    heap,
                    (nkey, next(counter), e.dst, ids + (e.id,), path_states + (e.dst,)),
                )
        else:
            # loop ended without `break`: k reached or heap drained
            complete = True
        frontier_sample = sorted(entry[0] for entry in heap)[:8]
        return KBestResult(
            evidences=evidences,
            complete=complete,
            pops=pops,
            budget=budget,
            frontier_size=len(heap),
            frontier_sample=frontier_sample,
        )

    # ------------------------------------------------------------------ #
    # exhaustive cross-checks (finite, for small graphs)
    # ------------------------------------------------------------------ #
    def brute_force_best(self) -> Evidence | None:
        """Minimum-key accepting *simple* path via exhaustive DFS.

        Equals the minimum over all paths (see module docstring); used to
        cross-check :meth:`best` on small graphs.
        """
        best_key = None
        best_ids = None

        def dfs(state, visited, key, ids):
            nonlocal best_key, best_ids
            if state in self.finals and (best_key is None or key < best_key):
                best_key, best_ids = key, ids
            for e in self.adj[state]:
                if e.dst in visited:
                    continue
                dfs(e.dst, visited | {e.dst}, self._extend_key(key, e, ids), ids + (e.id,))

        dfs(self.initial, {self.initial}, (0, 0, "", ()), ())
        return self._evidence(best_ids) if best_ids is not None else None

    def brute_force_k_best(self, k: int, max_steps: int) -> list[Evidence]:
        """First ``k`` distinct outputs among accepting paths of at most
        ``max_steps`` edges, via exhaustive DFS.  Cross-checks
        :meth:`k_best` on small graphs with a bounded path length."""
        best_key_by_output: dict[str, tuple] = {}
        best_ids_by_output: dict[str, tuple] = {}

        def dfs(state, key, ids):
            if state in self.finals:
                out = key[2]
                if out not in best_key_by_output or key < best_key_by_output[out]:
                    best_key_by_output[out] = key
                    best_ids_by_output[out] = ids
            if len(ids) >= max_steps:
                return
            for e in self.adj[state]:
                dfs(e.dst, self._extend_key(key, e, ids), ids + (e.id,))

        dfs(self.initial, (0, 0, "", ()), ())
        ordered = sorted(best_key_by_output.items(), key=lambda kv: kv[1])
        return [self._evidence(best_ids_by_output[out]) for out, _ in ordered[:k]]
