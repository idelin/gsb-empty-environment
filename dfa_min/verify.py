"""Independent product-space search used to verify minimization results."""

from __future__ import annotations

from collections import deque
from dataclasses import dataclass

from .core import DFA


@dataclass
class Verification:
    equivalent: bool
    witness: str | None        # shortest counterexample string, if inequivalent
    pairs_explored: int        # number of product state pairs touched


def product_verify(dfa1: DFA, dfa2: DFA) -> Verification:
    """BFS over the product of two complete DFAs from their start states."""
    if dfa1.alphabet != dfa2.alphabet:
        raise ValueError("alphabets differ")
    alphabet = dfa1.alphabet
    start = (dfa1.start, dfa2.start)
    parent: dict[tuple[str, str], tuple[tuple[str, str], str] | None] = {start: None}
    queue = deque([start])
    mismatch = None
    if (dfa1.start in dfa1.accepting) != (dfa2.start in dfa2.accepting):
        mismatch = start
    while queue and mismatch is None:
        x, y = queue.popleft()
        for a in alphabet:
            nx, ny = dfa1.transitions[x][a], dfa2.transitions[y][a]
            if (nx, ny) not in parent:
                parent[(nx, ny)] = ((x, y), a)
                if (nx in dfa1.accepting) != (ny in dfa2.accepting):
                    mismatch = (nx, ny)
                    break
                queue.append((nx, ny))
    if mismatch is None:
        return Verification(True, None, len(parent))
    chars = []
    cur = mismatch
    while parent[cur] is not None:
        prev, a = parent[cur]
        chars.append(a)
        cur = prev
    return Verification(False, "".join(reversed(chars)), len(parent))


def states_equivalent(dfa: DFA, p: str, q: str):
    """BFS on the pair graph of one DFA.

    Returns ``(equivalent, witness_or_None, pairs_explored)``.
    """
    if p == q:
        return True, None, 0
    start = (p, q)
    parent: dict[tuple[str, str], tuple[tuple[str, str], str] | None] = {start: None}
    queue = deque([start])
    mismatch = None
    if (p in dfa.accepting) != (q in dfa.accepting):
        mismatch = start
    while queue and mismatch is None:
        x, y = queue.popleft()
        for a in dfa.alphabet:
            nx, ny = dfa.transitions[x][a], dfa.transitions[y][a]
            if nx == ny:
                continue
            key = (nx, ny)
            if key not in parent:
                parent[key] = ((x, y), a)
                if (nx in dfa.accepting) != (ny in dfa.accepting):
                    mismatch = key
                    break
                queue.append(key)
    if mismatch is None:
        return True, None, len(parent)
    chars = []
    cur = mismatch
    while parent[cur] is not None:
        prev, a = parent[cur]
        chars.append(a)
        cur = prev
    return False, "".join(reversed(chars)), len(parent)


def dfa_to_data(dfa: DFA) -> dict:
    """Serialize a DFA to plain data (used for JSON and counterexamples)."""
    return {
        "alphabet": list(dfa.alphabet),
        "states": sorted(dfa.states),
        "transitions": {s: dict(dfa.transitions[s]) for s in sorted(dfa.states)},
        "start": dfa.start,
        "accepting": sorted(dfa.accepting),
    }


def counterexample_script(dfa1: DFA, dfa2: DFA, witness: str,
                          name1: str = "dfa1", name2: str = "dfa2") -> str:
    """Emit a self-contained, independently executable counterexample script."""
    data1 = dfa_to_data(dfa1)
    data2 = dfa_to_data(dfa2)
    return f'''#!/usr/bin/env python3
"""Standalone counterexample: {name1} and {name2} disagree on a witness word.

Run with: python3 this_file.py
Exits non-zero if the witness fails to distinguish the two automata.
"""

DFA1 = {data1!r}
DFA2 = {data2!r}
WITNESS = {witness!r}


def run(dfa, word):
    state = dfa["start"]
    for ch in word:
        state = dfa["transitions"][state][ch]
    return state in dfa["accepting"]


def main():
    r1 = run(DFA1, WITNESS)
    r2 = run(DFA2, WITNESS)
    print(f"witness: {{WITNESS!r}}")
    print(f"{name1} accepts: {{r1}}")
    print(f"{name2} accepts: {{r2}}")
    if r1 == r2:
        raise SystemExit("NOT a counterexample: both automata agree")
    print("counterexample confirmed: the automata disagree")


if __name__ == "__main__":
    main()
'''
