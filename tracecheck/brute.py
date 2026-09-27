"""Brute-force bounded word enumeration, used to cross-check the
on-the-fly product search on small models."""

from __future__ import annotations


def enumerate_visible_traces(machine, bound):
    """All visible traces of length <= bound, via subset determinization."""
    traces = set()
    start = machine.start_subset()
    if not start:
        return traces
    traces.add(())
    frontier = {(): start}
    for _ in range(bound):
        nxt = {}
        for word, subset in frontier.items():
            for label in machine.visible_alphabet:
                stepped = machine.subset_step(subset, label)
                if stepped:
                    longer = word + (label,)
                    traces.add(longer)
                    nxt[longer] = stepped
        frontier = nxt
    return traces


def brute_force_distinguishing_word(first, second, bound):
    """Shortest word (ties broken lexicographically) in the symmetric
    difference of the two bounded trace sets, or None."""
    traces_first = enumerate_visible_traces(first, bound)
    traces_second = enumerate_visible_traces(second, bound)
    diff = traces_first ^ traces_second
    if not diff:
        return None
    return min(diff, key=lambda w: (len(w), w))
