"""Deterministic, thread-local, key-seeded pseudo random numbers.

Stochastic operators (``dropout``) draw from the innermost active
:class:`rng_key`.  Recomputing a function with the same key reproduces the
exact same stream of masks, which is what lets a recompute node bind a random
key at forward time and faithfully replay the forward pass during backward.
"""

from __future__ import annotations

import threading

_tls = threading.local()
_DRAW_LOCK = threading.Lock()


def _state():
    st = getattr(_tls, "stack", None)
    if st is None:
        st = []
        _tls.stack = st
    return st


class rng_key:
    """Context manager binding stochastic draws to ``(seed, draw_counter)``."""

    def __init__(self, seed):
        self.seed = int(seed)
        self.counter = 0

    def __enter__(self):
        _state().append(self)
        return self

    def __exit__(self, *exc):
        stack = _state()
        assert stack and stack[-1] is self, "rng_key contexts exited out of order"
        stack.pop()
        return False

    def draw_unit(self):
        with _DRAW_LOCK:
            self.counter += 1
            n = self.counter
        return _splitmix_unit(self.seed, n)


def current_key():
    stack = _state()
    return stack[-1] if stack else None


def random_unit():
    key = current_key()
    if key is None:
        raise RuntimeError(
            "stochastic op used outside of an rng_key(...) context; "
            "bind a key so the draw can be replayed"
        )
    return key.draw_unit()


def _splitmix_unit(seed, counter):
    x = (seed * 0x9E3779B185EBCA87 + counter * 0xC2B2AE3D27D4EB4F)
    x &= 0xFFFFFFFFFFFFFFFF
    x ^= x >> 30
    x = (x * 0xBF58476D1CE4E5B9) & 0xFFFFFFFFFFFFFFFF
    x ^= x >> 27
    x = (x * 0x94D049BB133111EB) & 0xFFFFFFFFFFFFFFFF
    x ^= x >> 31
    return x / float(1 << 64)
