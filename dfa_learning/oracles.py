"""Query oracles.

The test side holds the hidden machine and exposes it only through these
interfaces.  The learner may call membership queries and bounded equivalence
queries; it never sees the hidden machine's states or transition table.

Both oracles carry a ``version`` string.  Checkpoints are bound to this
version: if the hidden machine (or anything about the oracle) changes, the
version must change, and previously saved evidence is rejected.
"""

from __future__ import annotations

from collections import deque


class DomainLimitError(Exception):
    """A query fell outside the bounded query domain (words of length <= L)."""


class MembershipOracle:
    """Answers membership queries against the hidden machine."""

    def __init__(self, hidden, max_len, version="oracle-v1"):
        self._hidden = hidden
        self.max_len = max_len
        self.version = version
        self.query_count = 0

    def query(self, word):
        """Return True iff the hidden machine accepts ``word`` (|word| <= L)."""
        if len(word) > self.max_len:
            raise DomainLimitError(
                f"membership query {word!r} exceeds the domain bound L={self.max_len}"
            )
        self.query_count += 1
        return self._hidden.accepts(word)


class BoundedEquivalenceOracle:
    """Bounded equivalence query over the product graph.

    Searches the product of the hidden machine and the hypothesis with a
    breadth-first walk, expanding symbols in alphabet order.  This visits at
    most ``|Q_hidden| * |Q_hypothesis|`` product states instead of enumerating
    the exponentially many words of length <= L.

    Returns the shortest counterexample in the domain, breaking ties by
    lexicographic order (with respect to the given alphabet order), or None
    when the two machines agree on every word of length <= L.
    """

    def __init__(self, hidden, alphabet, max_len, version="oracle-v1"):
        self._hidden = hidden
        self.alphabet = tuple(alphabet)
        self.max_len = max_len
        self.version = version
        self.query_count = 0

    def find_counterexample(self, hypothesis):
        """Return a shortest, lex-least counterexample word, or None."""
        self.query_count += 1
        hidden = self._hidden
        start = (hidden.start, hypothesis.start)
        if hidden.is_accepting(start[0]) != hypothesis.is_accepting(start[1]):
            return ""
        visited = {start}
        queue = deque([(start, "")])
        while queue:
            (qh, qp), word = queue.popleft()
            if len(word) >= self.max_len:
                continue
            for symbol in self.alphabet:
                nh = hidden.step(qh, symbol)
                np_ = hypothesis.step(qp, symbol)
                pair = (nh, np_)
                if pair in visited:
                    continue
                visited.add(pair)
                candidate = word + symbol
                if hidden.is_accepting(nh) != hypothesis.is_accepting(np_):
                    return candidate
                queue.append((pair, candidate))
        return None
