"""Independent brute-force enumerator used only to validate small domains.

This module intentionally does *not* share code with the product-graph
oracle, so the tests cross-check two independent implementations.  It is
only feasible for small L, which is exactly where the spec allows it.
"""

from __future__ import annotations

from itertools import product


def iter_words(alphabet, max_len):
    """Yield every word of length <= max_len in shortlex order.

    Shorter words come first; words of equal length follow the lexicographic
    order induced by ``alphabet``.
    """
    yield ""
    for length in range(1, max_len + 1):
        for combo in product(alphabet, repeat=length):
            yield "".join(combo)


def brute_force_counterexample(hidden, hypothesis, alphabet, max_len):
    """First (shortest, lex-least) word on which the two DFAs disagree."""
    for word in iter_words(alphabet, max_len):
        if hidden.accepts(word) != hypothesis.accepts(word):
            return word
    return None


def domain_equivalent(first, second, alphabet, max_len):
    """True iff the two DFAs agree on every word of length <= max_len."""
    return brute_force_counterexample(first, second, alphabet, max_len) is None
