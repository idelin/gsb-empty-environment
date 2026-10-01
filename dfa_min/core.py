"""DFA minimization with partition certificates and shortest witnesses.

Only the Python standard library is used.  The alphabet and the transition
structure are supplied explicitly as plain data; no regular-expression or
automata libraries are involved.
"""

from __future__ import annotations

from collections import deque
from dataclasses import dataclass

DEAD_STATE = "__dead__"


@dataclass
class DFA:
    """A deterministic automaton with an explicit alphabet and transitions.

    ``transitions`` maps ``state -> {symbol: target}`` and may be partial;
    :func:`complete` fills missing entries with a dead (sink) state.
    """

    alphabet: tuple[str, ...]
    states: frozenset[str]
    transitions: dict[str, dict[str, str]]
    start: str
    accepting: frozenset[str]


def build_dfa(alphabet, states, transitions, start, accepting) -> DFA:
    """Validate raw input data and build a :class:`DFA`."""
    alphabet = tuple(alphabet)
    if len(set(alphabet)) != len(alphabet):
        raise ValueError("alphabet contains duplicate symbols")
    alphabet = tuple(sorted(alphabet))
    states = frozenset(states)
    accepting = frozenset(accepting)
    if start not in states:
        raise ValueError(f"start state {start!r} is not in states")
    unknown_acc = accepting - states
    if unknown_acc:
        raise ValueError(f"accepting states not in states: {sorted(unknown_acc)}")
    table: dict[str, dict[str, str]] = {}
    for src, row in transitions.items():
        if src not in states:
            raise ValueError(f"transition source {src!r} is not in states")
        nrow = {}
        for sym, dst in row.items():
            if sym not in alphabet:
                raise ValueError(f"symbol {sym!r} is not in the alphabet")
            if dst not in states:
                raise ValueError(f"transition target {dst!r} is not in states")
            nrow[sym] = dst
        table[src] = nrow
    return DFA(alphabet, states, table, start, accepting)


def complete(dfa: DFA) -> DFA:
    """Fill every missing transition with a fresh non-accepting dead state."""
    missing = any(
        len(dfa.transitions.get(s, {})) < len(dfa.alphabet) for s in dfa.states
    )
    if not missing:
        return dfa
    dead = DEAD_STATE
    while dead in dfa.states:
        dead = "_" + dead
    states = dfa.states | {dead}
    trans = {s: dict(dfa.transitions.get(s, {})) for s in dfa.states}
    for s in dfa.states:
        for a in dfa.alphabet:
            trans[s].setdefault(a, dead)
    trans[dead] = {a: dead for a in dfa.alphabet}
    return DFA(dfa.alphabet, states, trans, dfa.start, dfa.accepting)


def reachable_states(dfa: DFA) -> frozenset[str]:
    seen = {dfa.start}
    queue = deque([dfa.start])
    while queue:
        s = queue.popleft()
        for a in dfa.alphabet:
            t = dfa.transitions.get(s, {}).get(a)
            if t is not None and t not in seen:
                seen.add(t)
                queue.append(t)
    return frozenset(seen)


def trim(dfa: DFA) -> DFA:
    """Drop unreachable states."""
    keep = reachable_states(dfa)
    trans = {
        s: {a: t for a, t in dfa.transitions.get(s, {}).items() if t in keep}
        for s in keep
    }
    return DFA(dfa.alphabet, keep, trans, dfa.start, dfa.accepting & keep)


def prepare(dfa: DFA) -> DFA:
    """Complete with a dead state, then remove unreachable states."""
    return trim(complete(dfa))


def refine_partition(dfa: DFA) -> list[frozenset[str]]:
    """Classic partition refinement from the accepting/non-accepting split."""
    states = sorted(dfa.states)
    acc = [s for s in states if s in dfa.accepting]
    non = [s for s in states if s not in dfa.accepting]
    partition = [frozenset(b) for b in (non, acc) if b]
    block_of: dict[str, int] = {}

    def reindex() -> None:
        block_of.clear()
        for i, b in enumerate(partition):
            for s in b:
                block_of[s] = i

    reindex()
    changed = True
    while changed:
        changed = False
        new_partition = []
        for b in partition:
            groups: dict[tuple[int, ...], list[str]] = {}
            for s in sorted(b):
                sig = tuple(block_of[dfa.transitions[s][a]] for a in dfa.alphabet)
                groups.setdefault(sig, []).append(s)
            if len(groups) > 1:
                changed = True
            for sig in sorted(groups):
                new_partition.append(frozenset(groups[sig]))
        partition = new_partition
        reindex()
    return partition


def shortest_distinguishing_strings(dfa: DFA) -> dict[tuple[str, str], str]:
    """Shortest distinguishing string for every inequivalent state pair.

    Reverse BFS over the pair graph, seeded with the acceptance mismatches
    (witness ``""``).  Keys are unordered pairs stored as sorted tuples.
    """
    states = sorted(dfa.states)
    acc = dfa.accepting
    pre = {a: {s: [] for s in states} for a in dfa.alphabet}
    for p in states:
        for a in dfa.alphabet:
            pre[a][dfa.transitions[p][a]].append(p)
    dist: dict[tuple[str, str], str] = {}
    queue: deque[tuple[str, str]] = deque()
    for i, p in enumerate(states):
        for q in states[i + 1:]:
            if (p in acc) != (q in acc):
                dist[(p, q)] = ""
                queue.append((p, q))
    while queue:
        x, y = queue.popleft()
        w = dist[(x, y)]
        for a in dfa.alphabet:
            for u, v in ((x, y), (y, x)):
                for p in pre[a][u]:
                    for q in pre[a][v]:
                        if p == q:
                            continue
                        key = (p, q) if p < q else (q, p)
                        if key not in dist:
                            dist[key] = a + w
                            queue.append(key)
    return dist


def quotient_with_map(dfa: DFA, partition):
    """Build the quotient DFA for a partition; states are block ids ``"0"``..."""
    block_of: dict[str, int] = {}
    for i, b in enumerate(partition):
        for s in b:
            block_of[s] = i
    trans = {}
    for i, b in enumerate(partition):
        rep = min(b)
        trans[str(i)] = {
            a: str(block_of[dfa.transitions[rep][a]]) for a in dfa.alphabet
        }
    quot = DFA(
        dfa.alphabet,
        frozenset(str(i) for i in range(len(partition))),
        trans,
        str(block_of[dfa.start]),
        frozenset(
            str(i) for i, b in enumerate(partition) if b & dfa.accepting
        ),
    )
    return quot, block_of


def canonical_form(dfa: DFA):
    """Rename states ``q0, q1, ...`` in BFS order from the start state.

    Returns ``(canonical_dfa, rename)``.  Isomorphic inputs yield identical
    canonical forms, so renaming the input states cannot change the result.
    """
    rename = {dfa.start: "q0"}
    queue = deque([dfa.start])
    while queue:
        s = queue.popleft()
        for a in dfa.alphabet:
            t = dfa.transitions[s][a]
            if t not in rename:
                rename[t] = f"q{len(rename)}"
                queue.append(t)
    for s in sorted(dfa.states):
        if s not in rename:
            rename[s] = f"q{len(rename)}"
    trans = {
        rename[s]: {a: rename[dfa.transitions[s][a]] for a in dfa.alphabet}
        for s in dfa.states
    }
    out = DFA(
        dfa.alphabet,
        frozenset(rename.values()),
        trans,
        rename[dfa.start],
        frozenset(rename[s] for s in dfa.accepting),
    )
    return out, rename


@dataclass
class Certificate:
    """Full language-equivalence partition certificate."""

    prepared: DFA                       # completed + reachable-trimmed input
    dead_state: str | None              # name of the added dead state, if any
    removed_unreachable: tuple[str, ...]
    partition: tuple[frozenset[str], ...]
    quotient: DFA                       # canonical naming (q0, q1, ...)
    mapping: dict[str, str]             # old state -> canonical quotient state
    witnesses: dict[tuple[str, str], str]   # quotient-state pair -> shortest string
    state_witnesses: dict[tuple[str, str], str]  # prepared-state pair -> shortest string


def minimize(dfa: DFA) -> Certificate:
    """Remove unreachable states, minimize, and certify the partition."""
    completed = complete(dfa)
    added = sorted(completed.states - dfa.states)
    prepared = trim(completed)
    removed = tuple(sorted(completed.states - prepared.states))
    partition = refine_partition(prepared)
    state_wit = shortest_distinguishing_strings(prepared)
    quot, block_of = quotient_with_map(prepared, partition)
    canon, rename = canonical_form(quot)
    mapping = {s: rename[str(block_of[s])] for s in sorted(prepared.states)}
    witnesses: dict[tuple[str, str], str] = {}
    for i in range(len(partition)):
        for j in range(i + 1, len(partition)):
            ri, rj = min(partition[i]), min(partition[j])
            key = (ri, rj) if ri < rj else (rj, ri)
            w = state_wit[key]
            ci, cj = rename[str(i)], rename[str(j)]
            witnesses[(ci, cj) if ci < cj else (cj, ci)] = w
    return Certificate(
        prepared=prepared,
        dead_state=added[0] if added else None,
        removed_unreachable=removed,
        partition=tuple(partition),
        quotient=canon,
        mapping=mapping,
        witnesses=witnesses,
        state_witnesses=state_wit,
    )
