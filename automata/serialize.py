"""JSON serialization for NFAs and counterexamples.

A counterexample exported here together with the two NFA definitions is
fully self-contained: ``python -m automata.verify A.json B.json cex.json``
re-checks it without any of the search machinery.
"""
from __future__ import annotations

from .inclusion import Counterexample, StepEvidence
from .nfa import NFA, Edge, EpsilonEdge
from .symbols import Interval, SymbolSet


def symset_to_json(ss: SymbolSet) -> list:
    return [[iv.lo, iv.hi] for iv in ss.intervals]


def symset_from_json(data) -> SymbolSet:
    return SymbolSet(Interval(lo, hi) for lo, hi in data)


def nfa_to_dict(nfa: NFA) -> dict:
    return {
        "name": nfa.name,
        "universe": symset_to_json(nfa.universe),
        "num_states": nfa.num_states,
        "initial": nfa.initial,
        "finals": sorted(nfa.finals),
        "edges": [
            {"id": e.id, "src": e.src, "dst": e.dst, "symbols": symset_to_json(e.symbols)}
            for lst in nfa.edges.values()
            for e in lst
        ],
        "epsilon": [
            {"id": e.id, "src": e.src, "dst": e.dst}
            for lst in nfa.eps_edges.values()
            for e in lst
        ],
    }


def nfa_from_dict(d: dict) -> NFA:
    nfa = NFA(
        universe=symset_from_json(d["universe"]),
        num_states=d["num_states"],
        initial=d["initial"],
        finals=d["finals"],
        name=d.get("name", ""),
    )
    max_id = -1
    for e in d["edges"]:
        edge = Edge(e["id"], e["src"], symset_from_json(e["symbols"]), e["dst"])
        nfa.edges.setdefault(edge.src, []).append(edge)
        max_id = max(max_id, edge.id)
    for e in d.get("epsilon", []):
        edge = EpsilonEdge(e["id"], e["src"], e["dst"])
        nfa.eps_edges.setdefault(edge.src, []).append(edge)
        max_id = max(max_id, edge.id)
    nfa._next_id = max_id + 1
    return nfa


def cex_to_dict(cex: Counterexample) -> dict:
    return {
        "word": list(cex.word),
        "final_a": sorted(cex.final_a),
        "final_b": sorted(cex.final_b),
        "steps": [
            {
                "symbol": s.symbol,
                "atom": [s.atom.lo, s.atom.hi],
                "edges_a": [e.id for e in s.edges_a],
                "edges_b": [e.id for e in s.edges_b],
            }
            for s in cex.steps
        ],
    }


def cex_from_dict(nfa_a: NFA, nfa_b: NFA, d: dict) -> Counterexample:
    steps = []
    for s in d["steps"]:
        steps.append(
            StepEvidence(
                symbol=s["symbol"],
                atom=Interval(*s["atom"]),
                edges_a=tuple(nfa_a.edge_by_id(i) for i in s["edges_a"]),
                edges_b=tuple(nfa_b.edge_by_id(i) for i in s["edges_b"]),
            )
        )
    return Counterexample(
        word=tuple(d["word"]),
        steps=tuple(steps),
        final_a=frozenset(d["final_a"]),
        final_b=frozenset(d["final_b"]),
    )
