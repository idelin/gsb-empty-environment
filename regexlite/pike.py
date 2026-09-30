"""Pike VM: priority-ordered NFA simulation with capture tags.

Threads are kept in priority order.  When two threads reach the same NFA
state at the same input position they merge, and the *first* (highest
priority) thread wins -- crucially, the winner's capture tags survive the
merge, so the tags of the selected path are never lost to state merging.

The per-position visited set also cuts epsilon cycles, which is why nested
stars such as ``(a*)*`` terminate here.

Match semantics: anchored at a start position, the VM reports the *longest*
match; among paths reaching that length, the highest-priority one (ordered
alternation, greedy star) supplies the tags.
"""
from __future__ import annotations

from .nfa import NFA

Tags = tuple[int | None, ...]


def closure(nfa: NFA, seeds: list[tuple[int, Tags]], pos: int) -> list[tuple[int, Tags]]:
    """Epsilon closure of ``seeds`` at input position ``pos``.

    Depth-first, following epsilon edges in their stored (priority) order and
    applying tag operations on the way.  First visit to a state wins.
    """
    out: list[tuple[int, Tags]] = []
    visited: set[int] = set()
    stack = list(reversed(seeds))
    while stack:
        state, tags = stack.pop()
        if state in visited:
            continue
        visited.add(state)
        out.append((state, tags))
        for target, slot in reversed(nfa.eps[state]):
            if slot is None:
                next_tags = tags
            else:
                stamped = list(tags)
                stamped[slot] = pos
                next_tags = tuple(stamped)
            stack.append((target, next_tags))
    return out


def initial_closure(nfa: NFA, pos: int) -> list[tuple[int, Tags]]:
    return closure(nfa, [(nfa.start, (None,) * nfa.nslots)], pos)


def accept_thread(nfa: NFA, clist: list[tuple[int, Tags]], pos: int):
    """First (highest-priority) accepting thread in ``clist``, if any."""
    for state, tags in clist:
        if state == nfa.accept:
            return (pos, tags)
    return None


def step(nfa: NFA, clist: list[tuple[int, Tags]], char: str, pos: int):
    """Consume ``char`` (read at ``pos``); return the new thread list."""
    seeds = []
    for state, tags in clist:
        target = nfa.sym[state].get(char)
        if target is not None:
            seeds.append((target, tags))
    return closure(nfa, seeds, pos + 1)


def pike_match_at(nfa: NFA, text: str, start: int = 0):
    """Longest match anchored at ``start``; returns ``(end, tags)`` or None."""
    clist = initial_closure(nfa, start)
    last = None
    pos = start
    while True:
        hit = accept_thread(nfa, clist, pos)
        if hit is not None:
            last = hit
        if pos >= len(text) or not clist:
            return last
        clist = step(nfa, clist, text[pos], pos)
        pos += 1


def pike_search(nfa: NFA, text: str, pos: int = 0):
    """Leftmost-longest search; returns ``(start, end, tags)`` or None."""
    for start in range(pos, len(text) + 1):
        hit = pike_match_at(nfa, text, start)
        if hit is not None:
            end, tags = hit
            return (start, end, tags)
    return None
