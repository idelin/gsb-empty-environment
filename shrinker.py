#!/usr/bin/env python3
"""Finite-domain counterexample shrinker for integer vectors.

Given an integer vector and a deterministic failure predicate, shrink the
counterexample using only two operations:

  * delete an element;
  * decrease the absolute value of an element by one, moving it toward zero
    without ever changing its sign (a positive element can reach 0 but never
    becomes negative, and vice versa).

The objective is lexicographic on the key

    (length, sum of absolute values, vector lexicographic order).

The shrinker performs a best-first search over the finite reachable space.
Every predicate call is cached so no vector is ever evaluated twice, and the
number of real predicate evaluations is bounded by a budget.

Why the result can be trusted
-----------------------------
Every shrink operation strictly decreases the objective key, so the reachable
space is a finite DAG ordered by the key.  States are popped from a priority
queue in key order and every popped state is expanded (its one-step
neighbours are generated), which eventually generates *every* reachable
state.  A predicate evaluation is spent only on states whose key is strictly
smaller than the best failing state found so far.  The search ends only when
the queue is empty, i.e. when every reachable state with a smaller key than
the final result has been generated and tested.  Only then is
``proven_minimal`` set to true -- a local minimum is never reported as a
global one.  If the predicate budget or the expansion cap stops the search
first, the result is reported as unproven together with the unexplored
frontier (generated-but-untested states that could still hide a smaller
counterexample).

CLI
---
    python3 shrinker.py --predicate consecutive_equal_nonzero \
        --vector '[4,4,9]' --budget 10000

Input may also come from stdin or ``--input FILE`` as either a JSON array
``[4, 4, 9]`` or a JSON object
``{"vector": [...], "predicate": "...", "budget": N, "max_expansions": M}``.
Command-line flags override the fields of an input object.  The result is
printed to stdout as a JSON object (see README.md for the field reference).
"""

from __future__ import annotations

import argparse
import heapq
import json
import sys
from typing import Callable, Iterable

FRONTIER_DISPLAY_LIMIT = 100
DEFAULT_BUDGET = 10_000
DEFAULT_MAX_EXPANSIONS = 1_000_000


# ---------------------------------------------------------------------------
# Predicate registry
# ---------------------------------------------------------------------------

PREDICATES: dict[str, Callable[[list[int]], bool]] = {}


def predicate(name: str) -> Callable[[Callable[[list[int]], bool]], Callable[[list[int]], bool]]:
    """Register a deterministic failure predicate under ``name``."""

    def decorate(fn: Callable[[list[int]], bool]) -> Callable[[list[int]], bool]:
        PREDICATES[name] = fn
        return fn

    return decorate


@predicate("consecutive_equal_nonzero")
def _consecutive_equal_nonzero(vector: list[int]) -> bool:
    """True iff two consecutive elements are equal and non-zero."""
    return any(a == b != 0 for a, b in zip(vector, vector[1:]))


@predicate("any_negative")
def _any_negative(vector: list[int]) -> bool:
    """True iff the vector contains a negative element."""
    return any(x < 0 for x in vector)


@predicate("sum_abs_at_least_10")
def _sum_abs_at_least_10(vector: list[int]) -> bool:
    """True iff the sum of absolute values is at least 10."""
    return sum(abs(x) for x in vector) >= 10


# ---------------------------------------------------------------------------
# Search space
# ---------------------------------------------------------------------------

def key_of(vector: Iterable[int]) -> tuple[int, int, tuple[int, ...]]:
    """Objective key: (length, sum of absolute values, lexicographic order)."""
    v = tuple(vector)
    return (len(v), sum(abs(x) for x in v), v)


def neighbors(vector: tuple[int, ...]) -> Iterable[tuple[int, ...]]:
    """All vectors reachable from ``vector`` by exactly one shrink operation."""
    for i, x in enumerate(vector):
        yield vector[:i] + vector[i + 1:]
        if x > 0:
            yield vector[:i] + (x - 1,) + vector[i + 1:]
        elif x < 0:
            yield vector[:i] + (x + 1,) + vector[i + 1:]


class _BudgetExhausted(Exception):
    pass


# ---------------------------------------------------------------------------
# Shrinker
# ---------------------------------------------------------------------------

class Shrinker:
    """Budgeted, caching, best-first shrinker.

    The predicate cache lives on the instance, so several ``shrink`` calls on
    the same ``Shrinker`` share both the cache and the remaining budget.
    """

    def __init__(
        self,
        pred: Callable[[list[int]], bool],
        budget: int = DEFAULT_BUDGET,
        max_expansions: int = DEFAULT_MAX_EXPANSIONS,
    ) -> None:
        if budget < 0:
            raise ValueError("budget must be non-negative")
        if max_expansions < 0:
            raise ValueError("max_expansions must be non-negative")
        self.predicate = pred
        self.budget = budget
        self.max_expansions = max_expansions
        self.cache: dict[tuple[int, ...], bool] = {}
        self.calls = 0
        self.cache_hits = 0

    def _test(self, vector: tuple[int, ...]) -> bool:
        """Evaluate the predicate with caching; raise when over budget."""
        if vector in self.cache:
            self.cache_hits += 1
            return self.cache[vector]
        if self.calls >= self.budget:
            raise _BudgetExhausted
        self.calls += 1
        outcome = bool(self.predicate(list(vector)))
        self.cache[vector] = outcome
        return outcome

    def shrink(self, vector: Iterable[int]) -> dict:
        start = tuple(int(x) for x in vector)
        notes: list[str] = []

        try:
            input_fails = self._test(start)
        except _BudgetExhausted:
            notes.append(
                "predicate budget exhausted before the input vector could be "
                "tested; the input is returned unverified"
            )
            return self._report(
                start, best=None, input_fails=None, proven=False,
                frontier=[start], frontier_complete=False,
                budget_exhausted=True, expansion_capped=False,
                expansions=0, generated=1, notes=notes,
            )

        if not input_fails:
            notes.append(
                "input vector does not satisfy the failure predicate; "
                "returned unchanged (nothing to shrink)"
            )
            return self._report(
                start, best=None, input_fails=False, proven=False,
                frontier=[], frontier_complete=True,
                budget_exhausted=False, expansion_capped=False,
                expansions=0, generated=1, notes=notes,
            )

        best = start
        heap: list[tuple[tuple[int, int, tuple[int, ...]], tuple[int, ...]]] = []
        visited = {start}
        expansions = 0
        budget_exhausted = False
        expansion_capped = False

        def expand(v: tuple[int, ...]) -> None:
            nonlocal expansions
            expansions += 1
            for nb in neighbors(v):
                if nb not in visited:
                    visited.add(nb)
                    heapq.heappush(heap, (key_of(nb), nb))

        if self.max_expansions > 0:
            expand(start)
        else:
            expansion_capped = True
            notes.append("expansion cap is zero; no neighbour of the input was generated")

        while heap:
            key, state = heapq.heappop(heap)
            if key < key_of(best):
                # Only states that could beat the current best are worth a
                # predicate evaluation.
                try:
                    fails = self._test(state)
                except _BudgetExhausted:
                    budget_exhausted = True
                    heapq.heappush(heap, (key, state))
                    notes.append("predicate budget exhausted during search")
                    break
                if fails:
                    best = state
            # Expansion is free (no predicate call) and is required for
            # completeness: smaller reachable states may only be generated
            # through states that are themselves not smaller than the best.
            if expansions >= self.max_expansions:
                expansion_capped = True
                heapq.heappush(heap, (key, state))
                notes.append(
                    "expansion cap reached; part of the reachable space was "
                    "never generated"
                )
                break
            expand(state)

        proven = not budget_exhausted and not expansion_capped and not heap
        best_key = key_of(best)
        frontier = sorted(
            (state for key, state in heap if key < best_key), key=key_of
        )
        frontier_complete = not heap
        if proven:
            notes.append(
                "every reachable vector with a smaller objective key was "
                "generated and tested; the result is the proven global minimum"
            )
        elif frontier and frontier_complete is False:
            notes.append(
                "result is NOT proven minimal: the listed frontier states were "
                "never tested, and further unexplored states may exist beyond "
                "them"
            )
        elif frontier:
            notes.append(
                "result is NOT proven minimal: the listed frontier states were "
                "never tested"
            )

        return self._report(
            start, best=best, input_fails=True, proven=proven,
            frontier=frontier, frontier_complete=frontier_complete,
            budget_exhausted=budget_exhausted, expansion_capped=expansion_capped,
            expansions=expansions, generated=len(visited), notes=notes,
        )

    def _report(
        self,
        start: tuple[int, ...],
        best: tuple[int, ...] | None,
        input_fails: bool | None,
        proven: bool,
        frontier: list[tuple[int, ...]],
        frontier_complete: bool,
        budget_exhausted: bool,
        expansion_capped: bool,
        expansions: int,
        generated: int,
        notes: list[str],
    ) -> dict:
        result = list(best) if best is not None else list(start)
        return {
            "input": list(start),
            "input_fails": input_fails,
            "result": result,
            "result_key": [len(result), sum(abs(x) for x in result), result],
            "proven_minimal": proven,
            "budget": self.budget,
            "budget_exhausted": budget_exhausted,
            "expansion_capped": expansion_capped,
            "predicate_calls": self.calls,
            "cache_hits": self.cache_hits,
            "cache_size": len(self.cache),
            "expansions": expansions,
            "states_generated": generated,
            "unexplored_frontier": [
                list(state) for state in frontier[:FRONTIER_DISPLAY_LIMIT]
            ],
            "unexplored_frontier_total": len(frontier),
            "frontier_complete": frontier_complete,
            "notes": notes,
        }


# ---------------------------------------------------------------------------
# CLI
# ---------------------------------------------------------------------------

def _load_spec(args: argparse.Namespace, parser: argparse.ArgumentParser) -> dict:
    if args.vector is not None:
        raw = args.vector
    elif args.input and args.input != "-":
        with open(args.input, "r", encoding="utf-8") as handle:
            raw = handle.read()
    else:
        raw = sys.stdin.read()
    try:
        spec = json.loads(raw)
    except json.JSONDecodeError as exc:
        parser.error(f"invalid JSON input: {exc}")
    if isinstance(spec, list):
        spec = {"vector": spec}
    if not isinstance(spec, dict) or "vector" not in spec:
        parser.error("input must be a JSON array or an object with a 'vector' field")
    vector = spec["vector"]
    if (
        not isinstance(vector, list)
        or any(isinstance(x, bool) or not isinstance(x, int) for x in vector)
    ):
        parser.error("'vector' must be a JSON array of integers")
    return spec


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(
        description="Finite-domain counterexample shrinker for integer vectors."
    )
    parser.add_argument(
        "--predicate",
        choices=sorted(PREDICATES),
        help="name of the deterministic failure predicate",
    )
    parser.add_argument(
        "--budget",
        type=int,
        default=None,
        help=f"maximum number of real predicate evaluations "
        f"(default {DEFAULT_BUDGET})",
    )
    parser.add_argument(
        "--max-expansions",
        type=int,
        default=None,
        help=f"maximum number of state expansions (default "
        f"{DEFAULT_MAX_EXPANSIONS})",
    )
    parser.add_argument("--vector", help="input vector as a JSON array, e.g. '[4,4,9]'")
    parser.add_argument(
        "--input",
        help="JSON input file (array or object); omit or use '-' to read stdin",
    )
    args = parser.parse_args(argv)

    spec = _load_spec(args, parser)
    vector = spec["vector"]

    predicate_name = args.predicate or spec.get("predicate")
    if predicate_name is None:
        parser.error("a predicate is required (--predicate or 'predicate' in the input)")
    if predicate_name not in PREDICATES:
        parser.error(
            f"unknown predicate {predicate_name!r}; "
            f"choose from {', '.join(sorted(PREDICATES))}"
        )

    budget = args.budget if args.budget is not None else spec.get("budget", DEFAULT_BUDGET)
    max_expansions = (
        args.max_expansions
        if args.max_expansions is not None
        else spec.get("max_expansions", DEFAULT_MAX_EXPANSIONS)
    )
    if not isinstance(budget, int) or budget < 0:
        parser.error("budget must be a non-negative integer")
    if not isinstance(max_expansions, int) or max_expansions < 0:
        parser.error("max_expansions must be a non-negative integer")

    shrinker = Shrinker(
        PREDICATES[predicate_name], budget=budget, max_expansions=max_expansions
    )
    report = shrinker.shrink(vector)
    report["predicate"] = predicate_name
    json.dump(report, sys.stdout, indent=2)
    sys.stdout.write("\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
