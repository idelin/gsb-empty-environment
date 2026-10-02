"""CLI: minimum-cost and top-K transduction over an explicit transducer.

Usage:
    python3 main.py SPEC.json [--k K] [--budget N]

SPEC.json explicitly defines the alphabet and the transition structure:
{
  "states":   ["q0", "q1"],
  "alphabet": ["a", "b"],
  "initial":  "q0",
  "finals":   ["q1"],
  "edges": [
    {"id": 0, "src": "q0", "dst": "q1", "in": "a", "out": "x", "cost": 2},
    {"id": 1, "src": "q0", "dst": "q1", "in": null, "out": "",  "cost": 0}
  ]
}
`"in": null` denotes an epsilon edge (no input consumed).  Costs must be
non-negative integers.  Exit code 1 signals that no accepting path exists.
"""

from __future__ import annotations

import argparse
import json
import sys

from transducer import DEFAULT_BUDGET, Edge, Transducer


def load_transducer(path: str) -> Transducer:
    with open(path, "r", encoding="utf-8") as fh:
        spec = json.load(fh)
    edges = [
        Edge(
            id=e["id"],
            src=e["src"],
            dst=e["dst"],
            inp=e.get("in"),
            out=e.get("out", ""),
            cost=e["cost"],
        )
        for e in spec["edges"]
    ]
    return Transducer(
        states=spec["states"],
        alphabet=spec["alphabet"],
        edges=edges,
        initial=spec["initial"],
        finals=spec["finals"],
    )


def main(argv=None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("spec", help="transducer spec JSON file")
    parser.add_argument("--k", type=int, default=1,
                        help="enumerate the first K distinct outputs (default 1)")
    parser.add_argument("--budget", type=int, default=DEFAULT_BUDGET,
                        help="max heap pops for the K-best enumeration")
    args = parser.parse_args(argv)

    tr = load_transducer(args.spec)

    if args.k == 1:
        ev = tr.best()
        if ev is None:
            print("FAILURE: no accepting path exists")
            return 1
        print("BEST transduction:")
        print(ev.render())
        return 0

    result = tr.k_best(args.k, budget=args.budget)
    if not result.evidences:
        print("FAILURE: no accepting path exists")
        return 1
    print(f"TOP-{args.k} distinct outputs "
          f"(complete={result.complete}, pops={result.pops}, "
          f"frontier_size={result.frontier_size}):")
    for rank, ev in enumerate(result.evidences, 1):
        print(f"[{rank}] {ev.render()}")
    if not result.complete:
        print(f"budget {result.budget} exhausted; frontier retained "
              f"({result.frontier_size} candidates), best remaining keys:")
        for key in result.frontier_sample:
            print(f"  frontier key={key[:3]} edge_ids={list(key[3])}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
