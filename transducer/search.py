"""Best-first search over the (state, input position) product graph.

Candidate paths are totally ordered by the key

    (total cost, output length, output string, edge-id sequence)

i.e. first by total cost, then by output length, then lexicographically by
output, and finally lexicographically by the edge-id sequence.  The order is
preserved when two keys are extended by the same edge (costs are
non-negative, and output *length* is compared before the output *string*, so
appending can never flip the string comparison).  This monotonicity is what
makes best-first popping correct.

Termination argument (epsilon and zero-cost cycles)
---------------------------------------------------
The product graph (state, pos) is finite: |Q| x (n+1) nodes.  A path that
revisits the same (pos, state) with the same output length has traversed a
*silent cycle*: a closed segment that consumed no input and emitted no
output.  Removing that segment yields another valid path with the same
output and <= cost, so the cyclic path can never contribute a new distinct
output nor a needed minimum.  The enumerator therefore prunes any extension
that would close a silent cycle.

After pruning, every remaining edge on a path either consumes input (at
most n times) or strictly grows the output.  Hence, below any fixed key
bound there are only finitely many candidate paths: within one input
position, at most |Q| * (L + 1) nodes can be visited with output length
<= L.  Therefore

* the minimum exists (no infinite strictly descending chain of keys --
  zero-cost silent cycles, the only source of such chains, are excluded),
* best-first enumeration pops the k-th distinct output after finitely many
  steps, and
* if only finitely many silent-cycle-free paths exist, the priority queue
  drains and the search stops on its own.

A zero-cost cycle that *does* emit output is not pruned: it generates
genuinely distinct outputs ("a", "aa", ...), each popped in key order, so
any finite k is still reached.
"""
from __future__ import annotations

import heapq
import itertools
from dataclasses import dataclass, field

from .model import FST

DEFAULT_BUDGET = 200_000


@dataclass(frozen=True)
class Step:
    """Path evidence for one fired edge: what it consumed and emitted."""

    edge_id: int
    src: str
    dst: str
    consumed: str  # "" for an epsilon edge
    produced: str  # "" when the edge emits nothing
    cost: float
    pos_before: int
    pos_after: int


@dataclass
class Transduction:
    """Result of a single minimum-cost transduction query."""

    ok: bool
    cost: float | None = None
    output: str | None = None
    steps: tuple[Step, ...] = ()
    pops: int = 0
    reason: str = ""


@dataclass
class RankedOutput:
    """One distinct output in a k-best enumeration, with its best path."""

    rank: int
    output: str
    cost: float
    key: tuple
    steps: tuple[Step, ...]


@dataclass
class KBestResult:
    """Result of a k-best distinct-output enumeration."""

    k: int
    results: list[RankedOutput] = field(default_factory=list)
    complete: bool = False  # True: k reached or search space exhausted
    budget_exhausted: bool = False
    pops: int = 0
    frontier: list = field(default_factory=list)  # pending keys, best first

    @property
    def distinct_outputs(self) -> int:
        return len(self.results)


@dataclass
class _Partial:
    node: tuple  # (state, pos)
    key: tuple  # (cost, output length, output, edge-id tuple)
    steps: tuple
    marks: frozenset  # (pos, state, output length) visited along the path


def _enumerate(fst: FST, text: str, k: int, budget: int, frontier_size: int = 8):
    """Best-first enumeration of complete paths; distinct outputs only.

    Returns (records, budget_exhausted, pops, frontier) where each record is
    (key, steps) for one distinct output, in increasing key order.
    """
    n = len(text)
    start = _Partial(
        node=(fst.start, 0),
        key=(0, 0, "", ()),
        steps=(),
        marks=frozenset({(0, fst.start, 0)}),
    )
    counter = itertools.count()
    pq: list = [(start.key, next(counter), start)]
    records: list = []
    seen: set = set()
    pops = 0
    budget_exhausted = False

    while pq and len(records) < k:
        if pops >= budget:
            budget_exhausted = True
            break
        key, _, cur = heapq.heappop(pq)
        pops += 1
        state, pos = cur.node

        if pos == n and state in fst.accepts:
            out = key[2]
            if out not in seen:
                # First time this output is popped: best-first order makes
                # this its minimum-key silent-cycle-free path.  Later pops of
                # the same output are duplicates and are not counted.
                seen.add(out)
                records.append((key, cur.steps))
                if len(records) >= k:
                    break
            # Fall through: an accept node may still have outgoing epsilon
            # edges leading to further accept states.

        for tr in fst.out_edges(state):
            if tr.input_symbol is None:
                npos = pos
            else:
                if pos >= n or text[pos] != tr.input_symbol:
                    continue
                npos = pos + 1
            nout = key[2] + tr.output
            nkey = (key[0] + tr.cost, len(nout), nout, key[3] + (tr.edge_id,))
            mark = (npos, tr.dst, len(nout))
            if mark in cur.marks:
                # Silent cycle: the segment since the previous visit consumed
                # no input and emitted no output.  Prune (see module docstring
                # for the termination/existence argument).
                continue
            step = Step(
                edge_id=tr.edge_id,
                src=tr.src,
                dst=tr.dst,
                consumed="" if tr.input_symbol is None else tr.input_symbol,
                produced=tr.output,
                cost=tr.cost,
                pos_before=pos,
                pos_after=npos,
            )
            nxt = _Partial(
                node=(tr.dst, npos),
                key=nkey,
                steps=cur.steps + (step,),
                marks=cur.marks | {mark},
            )
            heapq.heappush(pq, (nkey, next(counter), nxt))

    frontier = [entry[0] for entry in heapq.nsmallest(frontier_size, pq)]
    return records, budget_exhausted, pops, frontier


def min_cost_transduction(
    fst: FST, text: str, budget: int = DEFAULT_BUDGET
) -> Transduction:
    """Minimum-cost transduction of `text`, with path evidence.

    Tie-breaks: total cost, then output length, then output string, then
    edge-id sequence.  Returns ok=False when no accepting path consumes the
    whole input (or when the pop budget ran out first).
    """
    fst.check_text(text)
    records, exhausted, pops, _frontier = _enumerate(fst, text, k=1, budget=budget)
    if records:
        key, steps = records[0]
        return Transduction(
            ok=True, cost=key[0], output=key[2], steps=steps, pops=pops
        )
    reason = "no accepting path consumes the whole input"
    if exhausted:
        reason = "search budget exhausted before any accepting path was found"
    return Transduction(ok=False, pops=pops, reason=reason)


def k_best_outputs(
    fst: FST,
    text: str,
    k: int,
    budget: int = DEFAULT_BUDGET,
    frontier_size: int = 8,
) -> KBestResult:
    """First `k` distinct outputs, in key order, with per-output evidence.

    Multiple paths producing the same output are counted once (the best one
    supplies the evidence).  If the pop budget runs out first, the remaining
    frontier (best pending keys) is preserved in the result.
    """
    if k < 1:
        raise ValueError("k must be >= 1")
    fst.check_text(text)
    records, exhausted, pops, frontier = _enumerate(
        fst, text, k=k, budget=budget, frontier_size=frontier_size
    )
    ranked = [
        RankedOutput(rank=i + 1, output=key[2], cost=key[0], key=key, steps=steps)
        for i, (key, steps) in enumerate(records)
    ]
    return KBestResult(
        k=k,
        results=ranked,
        complete=not exhausted,
        budget_exhausted=exhausted,
        pops=pops,
        frontier=frontier,
    )
