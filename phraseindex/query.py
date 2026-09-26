"""Phrase queries: exact phrases, configurable gaps, synonym position graph.

A query is a sequence of *elements*. Each element is a set of alternative
terms (a synonym class occupying one position in the query graph). A
single-term element is written as a plain string.

``gaps[i]`` bounds the number of token positions allowed between the match
of element ``i`` and element ``i + 1``:

  * gap 0  -> strictly adjacent (exact phrase),
  * gap g  -> at most g tokens may sit in between.

Positions are strictly increasing along a match, so one token position can
never serve two query elements: the phrase "a a" requires two distinct
positions of "a".
"""
from __future__ import annotations

from typing import Iterable, List, Optional, Sequence, Tuple, Union

from .postings import BlockedPositions, DecodeCounter, INF

Element = Union[str, Iterable[str]]


class PhraseQuery:
    def __init__(
        self,
        elements: Sequence[Element],
        gaps: Optional[Union[int, Sequence[int]]] = None,
    ):
        if not elements:
            raise ValueError("phrase query needs at least one element")
        norm: List[frozenset] = []
        for el in elements:
            terms = frozenset([el] if isinstance(el, str) else el)
            if not terms:
                raise ValueError("empty synonym set")
            if any(not isinstance(t, str) or not t for t in terms):
                raise ValueError("terms must be non-empty strings")
            norm.append(terms)
        self.elements: Tuple[frozenset, ...] = tuple(norm)
        k = len(self.elements)
        if gaps is None:
            self.gaps: Tuple[int, ...] = (0,) * (k - 1)
        elif isinstance(gaps, int):
            self.gaps = (gaps,) * (k - 1)
        else:
            self.gaps = tuple(int(g) for g in gaps)
            if len(self.gaps) != k - 1:
                raise ValueError("gaps must have len(elements) - 1 entries")
        if any(g < 0 for g in self.gaps):
            raise ValueError("gaps must be >= 0")

    def __repr__(self) -> str:  # pragma: no cover - cosmetic
        return f"PhraseQuery(elements={self.elements!r}, gaps={self.gaps!r})"


def match_field(
    element_positions: Sequence[BlockedPositions],
    gaps: Sequence[int],
    counter: Optional[DecodeCounter] = None,
) -> List[Tuple[int, ...]]:
    """Enumerate all matches within one field of one document.

    Returns a list of position tuples, one position per query element.
    Uses block-max skipping (via ``BlockedPositions.range_iter``) so only
    positions inside the allowed window of the previous element are
    visited -- never a full Cartesian product. Strictly increasing
    positions guarantee no position is reused across elements.
    """
    k = len(element_positions)
    if any(len(ep) == 0 for ep in element_positions):
        return []
    results: List[Tuple[int, ...]] = []

    def extend(i: int, prev: int, acc: List[int]) -> None:
        lo = prev + 1
        hi = prev + gaps[i - 1] + 1
        for p in element_positions[i].range_iter(lo, hi, counter):
            if i == k - 1:
                results.append(tuple(acc) + (p,))
            else:
                acc.append(p)
                extend(i + 1, p, acc)
                acc.pop()

    if k == 1:
        for p in element_positions[0].range_iter(0, INF, counter):
            results.append((p,))
        return results

    for p0 in element_positions[0].range_iter(0, INF, counter):
        extend(1, p0, [p0])
    return results
