"""Unbiased uniform sampling over integer ranges [0, n).

Rejection sampling on bit strings avoids modulo bias and works for
arbitrarily large ``n`` (Python big integers), so huge indices are fine.

Exactness argument (EXACT, not empirical): drawing L = n.bit_length() bits
yields a uniform value over [0, 2**L).  Every v in [0, n) has exactly one
preimage, so conditional on acceptance each v is equally likely; re-sampling
after a rejection preserves that conditional uniformity.  The test suite
verifies this by exhaustively enumerating all 2**L bit draws for small n.
"""

from __future__ import annotations


def randbelow(bit_source, n: int) -> int:
    """Return a uniform integer in [0, n) drawn from ``bit_source``.

    ``bit_source`` must provide ``getrandbits(k) -> int`` returning a
    uniform value in [0, 2**k).  Raises ValueError for n <= 0.
    """
    if not isinstance(n, int) or isinstance(n, bool) or n <= 0:
        raise ValueError("n must be a positive integer")
    if n == 1:
        return 0  # degenerate range: no randomness needed
    bits = n.bit_length()
    while True:
        x = bit_source.getrandbits(bits)
        if x < n:
            return x
