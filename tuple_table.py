"""Prefix-compressed tuple table (trie) with incremental support maintenance.

A tuple table stores a set of fixed-length tuples over finite domains.
Tuples sharing a common prefix share trie nodes, so repeated prefixes are
stored only once.  Duplicate tuples are inserted once and never counted
twice.

Two polarities are supported:

* POSITIVE -- the listed tuples are the *allowed* combinations.
* NEGATIVE -- the listed tuples are the *forbidden* combinations.

For positive tables the structure maintains, for every (position, value)
pair, the number of currently valid tuples that contain it.  When a value
is removed from a variable domain, every tuple using that value is
invalidated and the support counters are decremented synchronously; a
trail permits exact undo on backtrack.
"""

from __future__ import annotations


class _Node:
    __slots__ = ("children", "tuple_id")

    def __init__(self) -> None:
        self.children: dict = {}
        self.tuple_id: int | None = None


class TupleTable:
    POSITIVE = "positive"
    NEGATIVE = "negative"

    def __init__(self, arity: int, tuples, polarity: str = POSITIVE) -> None:
        if polarity not in (self.POSITIVE, self.NEGATIVE):
            raise ValueError(f"unknown polarity: {polarity!r}")
        if arity < 0:
            raise ValueError("arity must be >= 0")
        self.arity = arity
        self.polarity = polarity
        self._root = _Node()
        self.tuples: list[tuple] = []          # unique tuples, indexed by id
        self._occurrences: dict[tuple[int, object], list[int]] = {}
        for raw in tuples:
            t = tuple(raw)
            if len(t) != arity:
                raise ValueError(
                    f"tuple {t!r} has arity {len(t)}, expected {arity}")
            self._insert(t)
        # Support bookkeeping: _support_count[pos][value] = number of
        # *valid* tuples containing `value` at position `pos`.
        self._valid = [True] * len(self.tuples)
        self._support_count: list[dict] = [dict() for _ in range(arity)]
        for tid, t in enumerate(self.tuples):
            for pos, value in enumerate(t):
                counts = self._support_count[pos]
                counts[value] = counts.get(value, 0) + 1

    # ------------------------------------------------------------------ #
    # construction / queries
    # ------------------------------------------------------------------ #
    def _insert(self, t: tuple) -> None:
        node = self._root
        for value in t:
            node = node.children.setdefault(value, _Node())
        if node.tuple_id is not None:
            return  # duplicate tuple: stored once, counted once
        tid = len(self.tuples)
        node.tuple_id = tid
        self.tuples.append(t)
        for pos, value in enumerate(t):
            self._occurrences.setdefault((pos, value), []).append(tid)

    def __len__(self) -> int:
        """Number of *unique* tuples."""
        return len(self.tuples)

    def __contains__(self, t) -> bool:
        node = self._root
        for value in t:
            node = node.children.get(value)
            if node is None:
                return False
        return node.tuple_id is not None

    def node_count(self) -> int:
        """Number of trie nodes excluding the root (compression metric)."""
        def walk(node: _Node) -> int:
            return 1 + sum(walk(child) for child in node.children.values())
        return walk(self._root) - 1

    def values_at(self, pos: int) -> set:
        """All values ever appearing at position `pos`."""
        return set(self._support_count[pos])

    # ------------------------------------------------------------------ #
    # support maintenance
    # ------------------------------------------------------------------ #
    def remove_value(self, pos: int, value, trail: list) -> None:
        """Synchronously invalidate every valid tuple using (pos, value).

        Each invalidation is recorded on `trail` as ("tuple", table, tid)
        so it can be undone exactly by `restore_tuple`.
        """
        for tid in self._occurrences.get((pos, value), ()):
            if self._valid[tid]:
                self._valid[tid] = False
                trail.append(("tuple", self, tid))
                for p, v in enumerate(self.tuples[tid]):
                    self._support_count[p][v] -= 1

    def restore_tuple(self, tid: int) -> None:
        """Undo the invalidation of tuple `tid`."""
        self._valid[tid] = True
        for p, v in enumerate(self.tuples[tid]):
            self._support_count[p][v] += 1

    def has_support(self, pos: int, value) -> bool:
        """True iff some *valid* tuple contains `value` at `pos`."""
        return self._support_count[pos].get(value, 0) > 0

    def supported_values(self, pos: int) -> set:
        """Values at `pos` still backed by at least one valid tuple."""
        return {v for v, c in self._support_count[pos].items() if c > 0}

    def valid_count(self) -> int:
        return sum(self._valid)
