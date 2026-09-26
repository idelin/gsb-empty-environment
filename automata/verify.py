"""Standalone counterexample verifier.

Re-interprets the distinguishing string on the *original* NFAs with the
independent interpreter and replays the per-step edge evidence.  Usable as
a library (``verify_counterexample``) or as a CLI::

    python -m automata.verify A.json B.json cex.json
"""
from __future__ import annotations

import argparse
import json
import sys
from typing import List

from .inclusion import Counterexample
from .nfa import NFA, accepts
from .serialize import cex_from_dict, nfa_from_dict


def verify_counterexample(nfa_a: NFA, nfa_b: NFA, cex: Counterexample) -> List[str]:
    """Return a list of problems; empty means the counterexample is valid."""
    problems: List[str] = []
    word = cex.word
    if len(cex.steps) != len(word):
        problems.append(
            f"evidence has {len(cex.steps)} steps for a word of length {len(word)}"
        )

    # 1. Independent membership check on the original NFAs.
    if not accepts(nfa_a, word):
        problems.append("word is not accepted by automaton A")
    if accepts(nfa_b, word):
        problems.append("word is accepted by automaton B")

    # 2. Replay the per-step original-edge evidence.
    cur_a = nfa_a.eps_closure({nfa_a.initial})
    cur_b = nfa_b.eps_closure({nfa_b.initial})
    for i, step in enumerate(cex.steps[: len(word)]):
        sym = word[i]
        if not step.atom.contains(sym):
            problems.append(
                f"step {i}: symbol {sym} not inside evidence atom "
                f"[{step.atom.lo},{step.atom.hi}]"
            )
        for tag, nfa, recorded in (
            ("A", nfa_a, step.edges_a),
            ("B", nfa_b, step.edges_b),
        ):
            cur = cur_a if tag == "A" else cur_b
            expected = {}
            for s in cur:
                for e in nfa.edges.get(s, ()):
                    if e.symbols.contains(sym):
                        expected[e.id] = e
            got = {e.id: e for e in recorded}
            if got.keys() != expected.keys():
                problems.append(
                    f"step {i}: {tag}-evidence edge ids {sorted(got)} "
                    f"!= independently computed {sorted(expected)}"
                )
            elif got != expected:
                problems.append(f"step {i}: {tag}-evidence edges do not match the originals")
        cur_a = nfa_a.eps_closure(
            {e.dst for s in cur_a for e in nfa_a.edges.get(s, ()) if e.symbols.contains(sym)}
        )
        cur_b = nfa_b.eps_closure(
            {e.dst for s in cur_b for e in nfa_b.edges.get(s, ()) if e.symbols.contains(sym)}
        )

    # 3. Final configurations must match and be accepting only on the A side.
    if cex.final_a != cur_a:
        problems.append(f"final A-subset {sorted(cex.final_a)} != replayed {sorted(cur_a)}")
    if cex.final_b != cur_b:
        problems.append(f"final B-subset {sorted(cex.final_b)} != replayed {sorted(cur_b)}")
    if not (cur_a & nfa_a.finals):
        problems.append("A does not end in a final state")
    if cur_b & nfa_b.finals:
        problems.append("B ends in a final state")
    return problems


def main(argv=None) -> int:
    parser = argparse.ArgumentParser(
        prog="automata.verify",
        description="Independently verify a distinguishing-string counterexample.",
    )
    parser.add_argument("nfa_a", help="JSON definition of automaton A")
    parser.add_argument("nfa_b", help="JSON definition of automaton B")
    parser.add_argument("counterexample", help="JSON counterexample to check")
    args = parser.parse_args(argv)
    with open(args.nfa_a) as f:
        nfa_a = nfa_from_dict(json.load(f))
    with open(args.nfa_b) as f:
        nfa_b = nfa_from_dict(json.load(f))
    with open(args.counterexample) as f:
        cex = cex_from_dict(nfa_a, nfa_b, json.load(f))
    problems = verify_counterexample(nfa_a, nfa_b, cex)
    if problems:
        for p in problems:
            print(f"FAIL: {p}")
        return 1
    print(
        f"OK: word {list(cex.word)} is accepted by A and rejected by B; "
        f"all {len(cex.steps)} evidence steps check out."
    )
    return 0


if __name__ == "__main__":
    sys.exit(main())
