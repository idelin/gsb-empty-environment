"""Independent brute-force verifier.

Enumerates every word of length <= L explicitly.  Only feasible for
small L; used to cross-check the product-graph oracle and to confirm
final in-domain equivalence.
"""
from __future__ import annotations

from itertools import product


def iter_words(alphabet, max_len):
    """Yield all words of length <= max_len in (length, lex) order."""
    for n in range(max_len + 1):
        for tup in product(alphabet, repeat=n):
            yield "".join(tup)


def brute_force_counterexample(hidden, hypothesis, alphabet, max_len):
    """(length, lex)-minimal word on which the two DFAs disagree, or None."""
    for w in iter_words(alphabet, max_len):
        if hidden.accepts(w) != hypothesis.accepts(w):
            return w
    return None


def domain_equivalent(dfa_a, dfa_b, alphabet, max_len):
    """True iff the two DFAs agree on every word of length <= max_len."""
    return brute_force_counterexample(dfa_a, dfa_b, alphabet, max_len) is None


class BruteForceCheckedOracle:
    """Wraps a product-graph oracle and validates every answer against
    full enumeration.  Only usable for small L."""

    def __init__(self, product_oracle, hidden, alphabet, max_len):
        self._oracle = product_oracle
        self._hidden = hidden
        self._alphabet = tuple(alphabet)
        self._max_len = max_len
        self.version = product_oracle.version

    def find_counterexample(self, hypothesis):
        ce = self._oracle.find_counterexample(hypothesis)
        expected = brute_force_counterexample(
            self._hidden, hypothesis, self._alphabet, self._max_len)
        if ce != expected:
            raise AssertionError(
                f"product oracle returned {ce!r}, brute force says {expected!r}")
        return ce
