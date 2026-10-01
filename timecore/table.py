"""Synthetic offset conversion tables between a tick clock and a time unit.

A table precomputes exact integer conversions at a fixed tick stride.  Any
tick is converted exactly by combining the nearest table row with a rational
remainder -- the table is a cache of exact values, never an approximation,
and no float is ever used.
"""

from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction

from .rounding import RoundingPolicy, round_rational
from .units import Unit


@dataclass(frozen=True)
class ConversionTable:
    """Exact tick -> target-unit conversion backed by a synthetic table."""

    src_unit: Unit
    dst_unit: Unit
    stride: int
    policy: RoundingPolicy
    rows: tuple  # rows[i] = exact dst ticks for src tick i * stride

    @classmethod
    def build(
        cls,
        src_unit: Unit,
        dst_unit: Unit,
        stride: int,
        count: int,
        policy: RoundingPolicy = RoundingPolicy.HALF_EVEN,
    ) -> "ConversionTable":
        if stride <= 0 or count <= 0:
            raise ValueError("stride and count must be positive")
        rows = tuple(
            round_rational(
                Fraction(i * stride) * src_unit.scale / dst_unit.scale, policy
            )
            for i in range(count)
        )
        return cls(src_unit, dst_unit, stride, policy, rows)

    @property
    def offset_per_stride(self) -> Fraction:
        """Exact synthetic offset between consecutive rows, in dst ticks."""
        return (
            Fraction(self.stride) * self.src_unit.scale / self.dst_unit.scale
        )

    def convert_exact(self, tick) -> Fraction:
        """Exact (possibly fractional) dst-tick value of ``tick``."""
        return Fraction(tick) * self.src_unit.scale / self.dst_unit.scale

    def convert(self, tick: int) -> int:
        """Table-assisted exact conversion, identical to direct rounding."""
        base, rem = divmod(tick, self.stride)
        if 0 <= base < len(self.rows):
            row = self.rows[base]
            exact = self.convert_exact(tick)
            row_exact = self.convert_exact(base * self.stride)
            candidate = row + round_rational(
                exact - row_exact, RoundingPolicy.TRUNC
            )
            if abs(candidate - exact) < 1:
                direct = round_rational(exact, self.policy)
                if candidate == direct:
                    return candidate
        return round_rational(self.convert_exact(tick), self.policy)

    def verify(self) -> bool:
        """Check every stored row against direct exact computation."""
        return all(
            row == round_rational(self.convert_exact(i * self.stride), self.policy)
            for i, row in enumerate(self.rows)
        )
