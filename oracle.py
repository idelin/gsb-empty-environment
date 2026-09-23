"""Test-side oracles.

The test side may hold the hidden DFA; the learner never does.  The
learner only receives a membership callable and a bounded equivalence
oracle object.
"""
from __future__ import annotations

import heapq


class MembershipOracle:
    """Answers membership queries against the hidden DFA."""

    def __init__(self, dfa):
        self.dfa = dfa
        self.num_queries = 0

    def __call__(self, word):
        self.num_queries += 1
        return self.dfa.accepts(word)


class BoundedEquivalenceOracle:
    """Bounded equivalence oracle over the product graph.

    Searches the product of the hidden machine and the hypothesis with a
    best-first search keyed by (length, word), so it returns a shortest
    counterexample, and among shortest ones the lexicographically
    smallest with respect to the alphabet order.  Only words of length
    <= max_len are considered.  The search is polynomial in the product
    size; it never enumerates the exponentially many words one by one.
    """

    def __init__(self, hidden, alphabet, max_len, version=None):
        self.hidden = hidden
        self.alphabet = tuple(alphabet)
        self.max_len = max_len
        self.version = version if version is not None else hidden.signature()
        self.num_queries = 0

    def find_counterexample(self, hypothesis):
        """Return the (length, lex)-minimal disagreement word, or None.

        Lexicographic order is defined by the alphabet order, not by
        character codes.
        """
        self.num_queries += 1
        hidden, hyp = self.hidden, hypothesis
        if set(hyp.alphabet) != set(self.alphabet):
            raise ValueError("hypothesis alphabet does not match the domain")
        rank = {a: i for i, a in enumerate(self.alphabet)}
        heap = [((0, ()), "", (hidden.start, hyp.start))]
        visited = set()
        while heap:
            (length, key), word, (qh, qp) = heapq.heappop(heap)
            pair = (qh, qp)
            if pair in visited:
                continue
            visited.add(pair)
            if (qh in hidden.accepting) != (qp in hyp.accepting):
                return word
            if length == self.max_len:
                continue
            for a in self.alphabet:
                heapq.heappush(
                    heap,
                    ((length + 1, key + (rank[a],)), word + a,
                     (hidden.step(qh, a), hyp.step(qp, a))),
                )
        return None
