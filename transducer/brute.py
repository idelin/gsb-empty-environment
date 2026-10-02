"""Exhaustive brute-force checker for small finite graphs.

Enumerates every silent-cycle-free path up to `max_steps` edges and keeps
the best key per distinct output.  Intended as an independent oracle to
cross-check `search.min_cost_transduction` and `search.k_best_outputs` on
small machines; it shares no code with the best-first enumerator.

The caller must pick `max_steps` large enough for the machine under test.
When the epsilon subgraph is acyclic, every silent-cycle-free path has at
most n + (n + 1) * |Q| edges (n = input length), so that value is a
complete bound.
"""
from __future__ import annotations

from .model import FST
from .search import Step


def brute_force_best(fst: FST, text: str, max_steps: int) -> list:
    """Return [(key, output, steps), ...] for all distinct outputs, sorted.

    key = (cost, output length, output, edge-id tuple), exactly the order
    used by the best-first enumerator.
    """
    fst.check_text(text)
    n = len(text)
    best: dict[str, tuple] = {}  # output -> (key, steps)
    stack = [
        (
            (fst.start, 0),
            (0, 0, "", ()),
            (),
            frozenset({(0, fst.start, 0)}),
        )
    ]
    while stack:
        (state, pos), key, steps, marks = stack.pop()
        if pos == n and state in fst.accepts:
            out = key[2]
            if out not in best or key < best[out][0]:
                best[out] = (key, steps)
        if len(steps) >= max_steps:
            continue
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
            if mark in marks:
                continue  # silent cycle; see search.py for the argument
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
            stack.append(
                ((tr.dst, npos), nkey, steps + (step,), marks | {mark})
            )
    return sorted((key, out, steps) for out, (key, steps) in best.items())
