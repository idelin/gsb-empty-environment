"""Independent brute-force cross-check of the planner's optimal value.

Exhaustively optimizes over *all* policy trees of depth <= D by plain
recursion (memoized on (node, remaining depth)).  It re-implements action
generation from scratch instead of importing the planner, so a bug in the
planner's search cannot silently propagate here.

Usage: python3.11 bruteforce.py MODEL.json --depth 8
Prints the minimum worst-branch cost achievable within the depth bound,
or "inf" if no policy of that depth covers everything.
"""
from __future__ import annotations

import argparse
import json
import math
import sys

from mealy import Model

INF = math.inf


class BruteForce:
    def __init__(self, model: Model, max_depth: int):
        self.m = model
        self.max_depth = max_depth
        self.memo: dict = {}
        self.target = model.all_transitions

    def initial_node(self):
        return frozenset((s, frozenset()) for s in self.m.initial_candidates)

    def solve(self, node, depth) -> float:
        if all(self.target <= cov for _s, cov in node):
            return 0
        if depth == 0:
            return INF
        key = (node, depth)
        if key in self.memo:
            return self.memo[key]
        states = {s for s, _c in node}
        best = INF
        # inputs safe for every candidate state
        for sym in self.m.inputs:
            if any((s, sym) not in self.m.trans for s in states):
                continue
            branches: dict[str, set] = {}
            for s, cov in node:
                ns, out = self.m.trans[(s, sym)]
                branches.setdefault(out, set()).add((ns, cov | {(s, sym)}))
            worst = max((self.solve(frozenset(ws), depth - 1)
                         for ws in branches.values()), default=INF)
            if worst < INF:
                best = min(best, self.m.input_costs[sym] + worst)
        # reset, if legal for every candidate state
        if self.m.has_reset() and states <= self.m.reset_allowed:
            child = frozenset((self.m.reset_target, cov) for _s, cov in node)
            sub = self.solve(child, depth - 1)
            if sub < INF:
                best = min(best, self.m.reset_cost + sub)
        self.memo[key] = best
        return best


def main(argv=None) -> int:
    ap = argparse.ArgumentParser(description="brute-force optimal value check")
    ap.add_argument("model")
    ap.add_argument("--depth", type=int, required=True)
    args = ap.parse_args(argv)
    model = Model.from_json_file(args.model)
    bf = BruteForce(model, args.depth)
    value = bf.solve(bf.initial_node(), args.depth)
    json.dump({"model": model.name, "depth": args.depth,
               "optimal_within_depth": (None if value == INF else value)},
              sys.stdout, indent=2)
    sys.stdout.write("\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
