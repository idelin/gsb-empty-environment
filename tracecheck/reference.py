"""Brute-force enumeration of bounded visible words.

Used as an independent oracle to cross-check the product search on small
models; deliberately shares no logic with the search itself beyond the
basic subset step.
"""
from __future__ import annotations

from .determinize import Determinizer


def visible_words(machine, bound):
    """All visible words of length <= ``bound`` accepted by ``machine``."""
    det = Determinizer(machine)
    words = {()}  # the empty trace is always present
    frontier = [(det.initial_subset(), ())]
    for _ in range(bound):
        nxt = []
        for subset, word in frontier:
            for action in sorted(machine.alphabet):
                target = det.step(subset, action)
                if target:
                    words.add(word + (action,))
                    nxt.append((target, word + (action,)))
        frontier = nxt
    return words


def reference_compare(left, right, bound):
    """Return (shortest distinguishing word, side) or None if equal within bound."""
    words_left = visible_words(left, bound)
    words_right = visible_words(right, bound)
    candidates = [(w, "left_only") for w in words_left - words_right]
    candidates += [(w, "right_only") for w in words_right - words_left]
    if not candidates:
        return None
    return min(candidates, key=lambda item: (len(item[0]), item[0]))
