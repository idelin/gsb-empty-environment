"""Semiring definitions shared by the sparse multiplication kernel.

Each semiring exposes a zero element (additive identity / annihilator),
a one element (multiplicative identity) and the two combine operations.
Absent cells and explicit zeros are both canonicalised to "missing" by
the engine, so a stored value is never the semiring zero.
"""

from __future__ import annotations

INF = float("inf")


class Semiring:
    name = "abstract"
    zero = None
    one = None

    def add(self, a, b):
        raise NotImplementedError

    def mul(self, a, b):
        raise NotImplementedError

    def validate(self, v):
        """Type-check a user supplied value, returning its canonical form."""
        raise NotImplementedError

    def is_zero(self, v):
        return v == self.zero


class BooleanSemiring(Semiring):
    """OR/AND semiring over {False, True}."""

    name = "boolean"
    zero = False
    one = True

    def add(self, a, b):
        return a or b

    def mul(self, a, b):
        return a and b

    def validate(self, v):
        if not isinstance(v, bool):
            raise TypeError(f"boolean semiring requires bool, got {type(v).__name__}")
        return v


class IntSemiring(Semiring):
    """Integer (+, *) semiring."""

    name = "int"
    zero = 0
    one = 1

    def add(self, a, b):
        return a + b

    def mul(self, a, b):
        return a * b

    def validate(self, v):
        if isinstance(v, bool) or not isinstance(v, int):
            raise TypeError(f"int semiring requires int, got {type(v).__name__}")
        return v


class MinPlusSemiring(Semiring):
    """(min, +) tropical semiring over integers and +inf."""

    name = "minplus"
    zero = INF
    one = 0

    def add(self, a, b):
        return a if a <= b else b

    def mul(self, a, b):
        return a + b

    def validate(self, v):
        if isinstance(v, bool) or not isinstance(v, (int, float)):
            raise TypeError(f"minplus semiring requires int or +inf, got {type(v).__name__}")
        if isinstance(v, float) and v != INF:
            raise TypeError("minplus values must be integers or +inf")
        return v

    def is_zero(self, v):
        return v == INF


_REGISTRY = {
    BooleanSemiring.name: BooleanSemiring,
    IntSemiring.name: IntSemiring,
    MinPlusSemiring.name: MinPlusSemiring,
}


def get_semiring(spec):
    """Resolve a Semiring instance from an instance or a registered name."""
    if isinstance(spec, Semiring):
        return spec
    try:
        return _REGISTRY[spec]()
    except KeyError:
        raise ValueError(f"unknown semiring {spec!r}; expected one of {sorted(_REGISTRY)}")
