"""Problem data model for the storage charge/discharge scheduler.

All quantities are exact rationals (fractions.Fraction). JSON inputs may use
integers, decimal numbers, or strings such as "3/2" or "1.5".
"""

from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction
from typing import Any


def to_fraction(value: Any) -> Fraction:
    """Convert an int / float / str / Fraction to an exact Fraction."""
    if isinstance(value, Fraction):
        return value
    if isinstance(value, bool):
        raise ValueError(f"not a number: {value!r}")
    if isinstance(value, int):
        return Fraction(value)
    if isinstance(value, float):
        # Exact decimal interpretation: 0.1 -> 1/10 (not the binary float).
        return Fraction(str(value))
    if isinstance(value, str):
        try:
            return Fraction(value)
        except (ValueError, ZeroDivisionError) as exc:
            raise ValueError(f"cannot parse rational number: {value!r}") from exc
    raise ValueError(f"cannot parse rational number: {value!r}")


@dataclass(frozen=True)
class Instance:
    """A storage scheduling instance.

    Units and conventions:
      - capacity:            maximum stored energy.
      - charge_efficiency:   stored energy per unit of grid energy bought (0, 1].
      - discharge_efficiency: delivered energy per unit of stored energy (0, 1].
      - max_charge_power:    max grid energy bought per period.
      - max_discharge_power: max delivered energy per period.
      - initial_energy:      stored energy before period 1.
      - final_energy:        required minimum stored energy after the last period.
      - prices[t]:           grid price in period t (may be negative).
      - demands[t]:          energy that must be delivered from the battery in
                             period t; any extra delivered energy is sold to the
                             grid at the period price.
    """

    capacity: Fraction
    charge_efficiency: Fraction
    discharge_efficiency: Fraction
    max_charge_power: Fraction
    max_discharge_power: Fraction
    initial_energy: Fraction
    final_energy: Fraction
    prices: tuple
    demands: tuple

    @property
    def periods(self) -> int:
        return len(self.prices)

    def validate(self) -> None:
        if self.periods == 0:
            raise ValueError("instance needs at least one period")
        if len(self.prices) != len(self.demands):
            raise ValueError("prices and demands must have the same length")
        if self.capacity <= 0:
            raise ValueError("capacity must be positive")
        if not 0 < self.charge_efficiency <= 1:
            raise ValueError("charge_efficiency must be in (0, 1]")
        if not 0 < self.discharge_efficiency <= 1:
            raise ValueError("discharge_efficiency must be in (0, 1]")
        if self.max_charge_power < 0 or self.max_discharge_power < 0:
            raise ValueError("power limits must be non-negative")
        if not 0 <= self.initial_energy <= self.capacity:
            raise ValueError("initial_energy must be within [0, capacity]")
        if not 0 <= self.final_energy <= self.capacity:
            raise ValueError("final_energy must be within [0, capacity]")
        if any(d < 0 for d in self.demands):
            raise ValueError("demands must be non-negative")

    @classmethod
    def from_dict(cls, data: dict) -> "Instance":
        known = {
            "capacity", "charge_efficiency", "discharge_efficiency",
            "max_charge_power", "max_discharge_power",
            "initial_energy", "final_energy", "prices", "demands",
        }
        optional = {"name", "description"}
        unknown = set(data) - known - optional
        if unknown:
            raise ValueError(f"unknown instance keys: {sorted(unknown)}")
        missing = known - set(data)
        if missing:
            raise ValueError(f"missing instance keys: {sorted(missing)}")
        inst = cls(
            capacity=to_fraction(data["capacity"]),
            charge_efficiency=to_fraction(data["charge_efficiency"]),
            discharge_efficiency=to_fraction(data["discharge_efficiency"]),
            max_charge_power=to_fraction(data["max_charge_power"]),
            max_discharge_power=to_fraction(data["max_discharge_power"]),
            initial_energy=to_fraction(data["initial_energy"]),
            final_energy=to_fraction(data["final_energy"]),
            prices=tuple(to_fraction(p) for p in data["prices"]),
            demands=tuple(to_fraction(d) for d in data["demands"]),
        )
        inst.validate()
        return inst
