"""Rational affine calibrations between clocks.

A calibration maps source-clock seconds to target-clock seconds as
``y = slope * x + offset`` with exact ``fractions.Fraction`` coefficients.
An optional symmetric error bound ``error`` (seconds of output) is tracked
as a symbolic error source so that compositions keep same-source errors
correlated.  No float conversion ever happens.
"""

from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction

from .errorbudget import ErrorBudget


@dataclass(frozen=True)
class AffineCalibration:
    """y = slope * x + offset, with optional symmetric output error bound."""

    source: str
    target: str
    slope: Fraction
    offset: Fraction
    error: Fraction = Fraction(0)   # symmetric bound, in target seconds
    source_id: str = ""             # error-correlation key; defaults to edge id
    error_sign: Fraction = Fraction(1)  # +1 forward, -1 for an inverse edge

    def __post_init__(self):
        object.__setattr__(self, "slope", Fraction(self.slope))
        object.__setattr__(self, "offset", Fraction(self.offset))
        object.__setattr__(self, "error", Fraction(self.error))
        if self.slope == 0:
            raise ValueError("calibration slope must be non-zero")
        if self.error < 0:
            raise ValueError("calibration error bound must be >= 0")
        if not self.source_id:
            object.__setattr__(
                self, "source_id", f"{self.source}->{self.target}"
            )

    @property
    def monotone_increasing(self) -> bool:
        return self.slope > 0

    def map_value(self, x) -> Fraction:
        """Exact forward map of a rational source value."""
        return self.slope * Fraction(x) + self.offset

    def map_budget(self, budget: ErrorBudget) -> ErrorBudget:
        """Forward map of an interval, tracking error correlation.

        The signed error coefficient lets a forward+inverse round trip
        cancel the shared source instead of doubling it.
        """
        out = budget.scale(self.slope).shift(self.offset)
        if self.error:
            out = out.add_term(self.source_id, self.error_sign * self.error)
        return out

    def map_interval(self, lo, hi) -> tuple:
        """Forward map of an interval [lo, hi] (handles negative slope)."""
        lo, hi = Fraction(lo), Fraction(hi)
        center = self.map_value((lo + hi) / 2)
        half = abs(self.slope) * (hi - lo) / 2 + self.error
        return (center - half, center + half)

    def inverse(self) -> "AffineCalibration":
        """The exact inverse calibration, error bound scaled by 1/|slope|.

        The inverse keeps the same ``source_id`` so a forward+inverse round
        trip cancels the shared error term instead of doubling it.
        """
        return AffineCalibration(
            source=self.target,
            target=self.source,
            slope=1 / self.slope,
            offset=-self.offset / self.slope,
            error=self.error / abs(self.slope),
            source_id=self.source_id,
            error_sign=-self.error_sign,
        )

    def preimage(self, y_lo, y_hi) -> tuple:
        """All x that can map into [y_lo, y_hi] under this calibration.

        Because the calibration carries an error bound, the preimage of a
        point is generally an interval: the mapping is monotone but the
        solution is not unique.
        """
        inv = self.inverse()
        lo, hi = Fraction(y_lo), Fraction(y_hi)
        half_in = (hi - lo) / 2
        center = inv.map_value((lo + hi) / 2)
        half_out = abs(inv.slope) * half_in + inv.error
        return (center - half_out, center + half_out)

    def then(self, other: "AffineCalibration", source_id: str = "") -> "AffineCalibration":
        """Compose: first ``self``, then ``other``."""
        if self.target != other.source:
            raise ValueError(
                f"cannot compose {self.source}->{self.target} with "
                f"{other.source}->{other.target}"
            )
        return AffineCalibration(
            source=self.source,
            target=other.target,
            slope=other.slope * self.slope,
            offset=other.slope * self.offset + other.offset,
            source_id=source_id,
        )

    def is_identity(self) -> bool:
        return self.slope == 1 and self.offset == 0
