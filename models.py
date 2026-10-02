"""Sample finite-state models and the model interface.

A model provides:
    initial()        -> iterable of concrete initial states
    successors(s)    -> iterable of concrete successor states
    is_bad(s)        -> invariant violation predicate
    canonicalize(s)  -> hashable canonical form (optional; default identity)

Canonicalization may compress states (e.g. symmetry reduction). The
checker keeps a recovery map canonical -> concrete representative, so
traces are always replayable with concrete states.
"""
from __future__ import annotations


class Model:
    def canonicalize(self, state):
        return state


class CounterModel(Model):
    """Chain 0 -> 1 -> ... -> n; bad when the counter hits ``bad_at``."""

    def __init__(self, n: int, bad_at: int | None = None):
        self.n = n
        self.bad_at = bad_at

    def initial(self):
        return [0]

    def successors(self, s):
        return [s + 1] if s < self.n else []

    def is_bad(self, s):
        return self.bad_at is not None and s == self.bad_at


class BinaryTreeModel(Model):
    """Complete binary tree of given depth; state is a node index."""

    def __init__(self, depth: int, bad: int | None = None):
        self.depth = depth
        self.bad = bad
        self.max_node = 2 ** (depth + 1) - 2

    def initial(self):
        return [0]

    def successors(self, s):
        out = []
        for child in (2 * s + 1, 2 * s + 2):
            if child <= self.max_node:
                out.append(child)
        return out

    def is_bad(self, s):
        return self.bad is not None and s == self.bad


class GridModel(Model):
    """4-neighbour grid walk from (0, 0); ``bad`` cell and ``blocked`` cells."""

    def __init__(self, w: int, h: int, bad=None, blocked=frozenset()):
        self.w = w
        self.h = h
        self.bad = bad
        self.blocked = frozenset(blocked)

    def initial(self):
        return [(0, 0)]

    def successors(self, s):
        x, y = s
        out = []
        for nx, ny in ((x + 1, y), (x, y + 1), (x - 1, y), (x, y - 1)):
            if 0 <= nx < self.w and 0 <= ny < self.h and (nx, ny) not in self.blocked:
                out.append((nx, ny))
        return out

    def is_bad(self, s):
        return self.bad is not None and s == self.bad


class TokenRingModel(Model):
    """Tokens on a ring; canonical form sorts positions (symmetry compression).

    Concrete state: tuple of token positions on a ring of size ``k``.
    Each move advances one token by one step. Bad when two tokens share
    a cell (if ``bad_together``).
    """

    def __init__(self, k: int, tokens=(0, 1), bad_together: bool = True):
        self.k = k
        self.tokens = tuple(tokens)
        self.bad_together = bad_together

    def initial(self):
        return [self.tokens]

    def successors(self, s):
        out = []
        for i in range(len(s)):
            moved = list(s)
            moved[i] = (moved[i] + 1) % self.k
            out.append(tuple(moved))
        return out

    def is_bad(self, s):
        return self.bad_together and len(set(s)) < len(s)

    def canonicalize(self, s):
        return tuple(sorted(s))


class CollisionState:
    """State with a deliberately constant hash to stress collision handling."""

    __slots__ = ("v",)

    def __init__(self, v: int):
        self.v = v

    def __hash__(self):
        return 42

    def __eq__(self, other):
        return isinstance(other, CollisionState) and self.v == other.v

    def __repr__(self):
        return f"CollisionState({self.v})"


class CollisionChainModel(Model):
    """Chain of CollisionState(0..n); every state shares one hash bucket."""

    def __init__(self, n: int, bad_at: int | None = None):
        self.n = n
        self.bad_at = bad_at

    def initial(self):
        return [CollisionState(0)]

    def successors(self, s):
        return [CollisionState(s.v + 1)] if s.v < self.n else []

    def is_bad(self, s):
        return self.bad_at is not None and s.v == self.bad_at
