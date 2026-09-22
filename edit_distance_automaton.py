"""Weighted edit-distance automaton (Levenshtein-style) built from scratch.

- Explicit alphabet and explicit transition structure (no regex/automata libs).
- Explicit insert / delete / substitute costs; adjacent transposition only
  counts as a single edit when its cost is explicitly declared.
- Streaming: feed one symbol at a time, the automaton keeps the set of
  reachable states ``{pattern_position: best_cost}``; when several paths
  reach the same state only the optimal (minimum) cost is kept.
- Batch query returns the minimum distance together with the edit path.
- The empty pattern matches exactly the strings of length <= k
  (with unit costs; generally: len(text) * insert_cost <= k).

Python 3.11, standard library only.
"""

from __future__ import annotations

from dataclasses import dataclass
from math import inf
from typing import Optional


@dataclass(frozen=True)
class EditCosts:
    """Explicit edit operation costs.

    ``transpose=None`` means an adjacent swap is NOT a single edit
    (it then costs one delete + one insert, or two substitutes).
    """

    insert: int = 1
    delete: int = 1
    substitute: int = 1
    transpose: Optional[int] = None

    def __post_init__(self) -> None:
        for name in ("insert", "delete", "substitute"):
            if getattr(self, name) < 0:
                raise ValueError(f"{name} cost must be >= 0")
        if self.transpose is not None and self.transpose < 0:
            raise ValueError("transpose cost must be >= 0")


@dataclass(frozen=True)
class EditOp:
    """One step of an edit path."""

    kind: str           # 'match' | 'substitute' | 'insert' | 'delete' | 'transpose'
    pattern_chars: str  # pattern symbol(s) consumed ("" for insert)
    text_chars: str     # text symbol(s) produced ("" for delete)
    cost: int

    def __str__(self) -> str:
        if self.kind == "match":
            return f"match({self.pattern_chars})"
        if self.kind == "substitute":
            return f"sub({self.pattern_chars}->{self.text_chars})+{self.cost}"
        if self.kind == "insert":
            return f"ins({self.text_chars})+{self.cost}"
        if self.kind == "delete":
            return f"del({self.pattern_chars})+{self.cost}"
        return f"transpose({self.pattern_chars}->{self.text_chars})+{self.cost}"


class EditDistanceAutomaton:
    """Edit-distance-<=k automaton for a fixed pattern.

    States are pattern positions ``0..len(pattern)``.  The transition
    structure is exposed explicitly via :meth:`transitions` (input-consuming)
    and :meth:`epsilon_transitions` (deletions, consume no input).
    """

    def __init__(self, pattern: str, alphabet, k: int,
                 costs: Optional[EditCosts] = None) -> None:
        if k < 0:
            raise ValueError("k must be >= 0")
        self.pattern = pattern
        self.alphabet = frozenset(alphabet)
        if not self.alphabet:
            raise ValueError("alphabet must be non-empty")
        unknown = set(pattern) - self.alphabet
        if unknown:
            raise ValueError(f"pattern symbols not in alphabet: {sorted(unknown)}")
        self.k = k
        self.costs = costs if costs is not None else EditCosts()
        self.reset()

    # ------------------------------------------------------------------
    # Explicit transition structure
    # ------------------------------------------------------------------
    def transitions(self, position: int, char: str):
        """Input-consuming transitions out of ``position`` on ``char``.

        Returns a list of ``(next_position, added_cost, op_kind)``.
        """
        if char not in self.alphabet:
            raise ValueError(f"symbol {char!r} not in alphabet")
        if not 0 <= position <= len(self.pattern):
            raise ValueError(f"bad state {position}")
        out = [(position, self.costs.insert, "insert")]
        if position < len(self.pattern):
            if self.pattern[position] == char:
                out.append((position + 1, 0, "match"))
            else:
                out.append((position + 1, self.costs.substitute, "substitute"))
        return out

    def epsilon_transitions(self, position: int):
        """Deletion transitions (consume no input symbol)."""
        if not 0 <= position <= len(self.pattern):
            raise ValueError(f"bad state {position}")
        if position < len(self.pattern):
            return [(position + 1, self.costs.delete, "delete")]
        return []

    # ------------------------------------------------------------------
    # Streaming interface
    # ------------------------------------------------------------------
    def reset(self) -> "EditDistanceAutomaton":
        """Back to the start configuration: deletions of pattern prefixes."""
        frontier = {}
        cost = 0
        for i in range(len(self.pattern) + 1):
            if cost > self.k:
                break
            frontier[i] = cost
            cost += self.costs.delete
        self._frontier = frontier
        self._prev_frontier = None
        self._prev_char = None
        self.symbols_consumed = 0
        return self

    @property
    def states(self):
        """Current streaming state set ``{pattern_position: best_cost}``."""
        return dict(self._frontier)

    def step(self, char: str) -> "EditDistanceAutomaton":
        """Consume one input symbol, updating the state set."""
        if char not in self.alphabet:
            raise ValueError(f"symbol {char!r} not in alphabet")
        m = len(self.pattern)
        nxt = {}

        def relax(pos: int, cost: int) -> None:
            # prune states over budget; keep only the best cost per state
            if cost > self.k:
                return
            if cost < nxt.get(pos, inf):
                nxt[pos] = cost

        for pos, cost in self._frontier.items():
            for npos, dcost, _op in self.transitions(pos, char):
                relax(npos, cost + dcost)

        # Adjacent transposition: only when explicitly declared.  Needs the
        # state set from two steps back and the previously consumed symbol.
        if self.costs.transpose is not None and self._prev_frontier is not None:
            for pos, cost in self._prev_frontier.items():
                if (pos + 1 < m
                        and self.pattern[pos] == char
                        and self.pattern[pos + 1] == self._prev_char):
                    relax(pos + 2, cost + self.costs.transpose)

        # Epsilon-closure: deletions move forward without consuming input.
        # Costs are non-negative and moves are forward, one ascending pass.
        for pos in range(m):
            if pos in nxt:
                relax(pos + 1, nxt[pos] + self.costs.delete)

        self._prev_frontier = self._frontier
        self._prev_char = char
        self._frontier = nxt
        self.symbols_consumed += 1
        return self

    def feed(self, text: str) -> "EditDistanceAutomaton":
        for ch in text:
            self.step(ch)
        return self

    def best_cost(self) -> Optional[int]:
        """Best cost of an accepting state, or None if none survives."""
        return self._frontier.get(len(self.pattern))

    def accepts(self) -> bool:
        cost = self.best_cost()
        return cost is not None and cost <= self.k

    def match(self, text: str):
        """Streaming convenience: reset, feed, return (accepted, best_cost)."""
        self.reset().feed(text)
        return self.accepts(), self.best_cost()

    # ------------------------------------------------------------------
    # Batch interface: minimum distance + edit path
    # ------------------------------------------------------------------
    def min_distance(self, text: str) -> int:
        return self.min_distance_and_path(text)[0]

    def min_distance_and_path(self, text: str):
        """Full DP over (text prefix) x (pattern prefix).

        Returns ``(distance, ops)`` where ``ops`` is a list of :class:`EditOp`
        transforming ``self.pattern`` into ``text`` with total cost
        ``distance``.  The distance is the global minimum (may exceed k).
        """
        for ch in text:
            if ch not in self.alphabet:
                raise ValueError(f"symbol {ch!r} not in alphabet")
        pat, c = self.pattern, self.costs
        m, n = len(pat), len(text)
        d = [[inf] * (m + 1) for _ in range(n + 1)]
        back = [[None] * (m + 1) for _ in range(n + 1)]
        d[0][0] = 0
        for j in range(1, m + 1):
            d[0][j] = d[0][j - 1] + c.delete
            back[0][j] = (0, j - 1, EditOp("delete", pat[j - 1], "", c.delete))
        for t in range(1, n + 1):
            d[t][0] = d[t - 1][0] + c.insert
            back[t][0] = (t - 1, 0, EditOp("insert", "", text[t - 1], c.insert))
        for t in range(1, n + 1):
            ch = text[t - 1]
            for j in range(1, m + 1):
                p = pat[j - 1]
                cands = []
                if ch == p:
                    cands.append((d[t - 1][j - 1],
                                  (t - 1, j - 1, EditOp("match", p, ch, 0))))
                cands.append((d[t - 1][j - 1] + c.substitute,
                              (t - 1, j - 1, EditOp("substitute", p, ch, c.substitute))))
                cands.append((d[t][j - 1] + c.delete,
                              (t, j - 1, EditOp("delete", p, "", c.delete))))
                cands.append((d[t - 1][j] + c.insert,
                              (t - 1, j, EditOp("insert", "", ch, c.insert))))
                if (c.transpose is not None and t >= 2 and j >= 2
                        and pat[j - 2] == ch and text[t - 2] == p):
                    cands.append((d[t - 2][j - 2] + c.transpose,
                                  (t - 2, j - 2,
                                   EditOp("transpose", pat[j - 2:j],
                                          text[t - 2:t], c.transpose))))
                d[t][j], back[t][j] = min(cands, key=lambda cand: cand[0])
        ops = []
        t, j = n, m
        while t > 0 or j > 0:
            pt, pj, op = back[t][j]
            ops.append(op)
            t, j = pt, pj
        ops.reverse()
        return d[n][m], ops

    def explain(self, text: str):
        """(accepted, min_distance, edit_path) for ``text``."""
        distance, ops = self.min_distance_and_path(text)
        return distance <= self.k, distance, ops
