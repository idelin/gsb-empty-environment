"""State normalizers.

A normalizer maps a concrete state to a canonical key plus a *recovery
mapping*.  The pair (key, recovery) must always be sufficient to rebuild
the original concrete state, so normalization never loses information:

    recover(*normalize(state)) == state
"""
from __future__ import annotations

from .encoding import decode_state, encode_state


class IdentityNormalizer:
    """Canonical form is the state itself; recovery mapping is empty."""

    def normalize(self, state) -> tuple[bytes, bytes]:
        return encode_state(state), b""

    def recover(self, key: bytes, recovery: bytes):
        return decode_state(key)


class PermutationNormalizer:
    """Canonicalizes a tuple state by sorting its elements.

    Useful for models with symmetric components.  The recovery mapping
    stores the permutation that restores the original element order, so
    the concrete state remains fully reconstructible.
    """

    def normalize(self, state) -> tuple[bytes, bytes]:
        if not isinstance(state, tuple):
            raise TypeError("PermutationNormalizer expects tuple states")
        order = sorted(range(len(state)), key=lambda i: encode_state(state[i]))
        key = encode_state(tuple(state[i] for i in order))
        recovery = encode_state(tuple(order))
        return key, recovery

    def recover(self, key: bytes, recovery: bytes):
        sorted_state = decode_state(key)
        order = decode_state(recovery)
        result = [None] * len(order)
        for sorted_pos, original_pos in enumerate(order):
            result[original_pos] = sorted_state[sorted_pos]
        return tuple(result)
