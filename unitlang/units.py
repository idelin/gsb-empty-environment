"""Dimensions, units and the unit registry.

A dimension is a vector of integer exponents over the base quantities
length (L), mass (M), time (T) and thermodynamic temperature (H).

A unit converts to/from base SI units by  base = value * scale + offset.
The offset is non-zero only for affine (absolute) temperature units.
"""

from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction


@dataclass(frozen=True)
class Dim:
    L: int = 0
    M: int = 0
    T: int = 0
    H: int = 0

    def combine(self, other: "Dim", sign: int) -> "Dim":
        return Dim(
            self.L + sign * other.L,
            self.M + sign * other.M,
            self.T + sign * other.T,
            self.H + sign * other.H,
        )


DIMENSIONLESS = Dim()
LENGTH = Dim(L=1)
MASS = Dim(M=1)
TIME = Dim(T=1)
TEMPERATURE = Dim(H=1)


@dataclass(frozen=True)
class Unit:
    name: str
    dim: Dim
    scale: Fraction
    offset: Fraction = Fraction(0)
    affine: bool = False

    def to_base(self, value: Fraction) -> Fraction:
        return value * self.scale + self.offset

    def from_base(self, base: Fraction) -> Fraction:
        return (base - self.offset) / self.scale


UNITS: dict[str, Unit] = {}


def _reg(name, dim, scale, offset=Fraction(0), affine=False):
    UNITS[name] = Unit(name, dim, Fraction(scale), Fraction(offset), affine)


_reg("m", LENGTH, 1)
_reg("mm", LENGTH, Fraction(1, 1000))
_reg("cm", LENGTH, Fraction(1, 100))
_reg("km", LENGTH, 1000)
_reg("in", LENGTH, Fraction(127, 5000))
_reg("ft", LENGTH, Fraction(381, 1250))
_reg("mi", LENGTH, Fraction(201168, 125))

_reg("s", TIME, 1)
_reg("ms", TIME, Fraction(1, 1000))
_reg("min", TIME, 60)
_reg("h", TIME, 3600)
_reg("day", TIME, 86400)

_reg("kg", MASS, 1)
_reg("g", MASS, Fraction(1, 1000))
_reg("t", MASS, 1000)
_reg("lb", MASS, Fraction(45359237, 100000000))

_reg("Hz", Dim(T=-1), 1)
_reg("N", Dim(L=1, M=1, T=-2), 1)
_reg("Pa", Dim(L=-1, M=1, T=-2), 1)
_reg("J", Dim(L=2, M=1, T=-2), 1)
_reg("W", Dim(L=2, M=1, T=-3), 1)

_reg("K", TEMPERATURE, 1, affine=True)
_reg("°C", TEMPERATURE, 1, Fraction(5463, 20), affine=True)
_reg("°F", TEMPERATURE, Fraction(5, 9), Fraction(45967, 180), affine=True)

_reg("ΔK", TEMPERATURE, 1)
_reg("Δ°C", TEMPERATURE, 1)
_reg("Δ°F", TEMPERATURE, Fraction(5, 9))

KELVIN = UNITS["K"]


def dim_unit_name(dim: Dim) -> str:
    num, den = [], []
    for name, exp in (("kg", dim.M), ("m", dim.L), ("s", dim.T), ("K", dim.H)):
        if exp > 0:
            num.append(name if exp == 1 else f"{name}^{exp}")
        elif exp < 0:
            den.append(name if exp == -1 else f"{name}^{-exp}")
    if not num and not den:
        return "1"
    if not num:
        num = ["1"]
    out = "*".join(num)
    if den:
        out += "/" + "*".join(den)
    return out


_DIM_NAMES = {
    DIMENSIONLESS: "dimensionless quantity",
    LENGTH: "length",
    TIME: "time",
    MASS: "mass",
    TEMPERATURE: "temperature difference",
    Dim(L=2): "area",
    Dim(L=3): "volume",
    Dim(L=1, T=-1): "speed",
    Dim(L=1, T=-2): "acceleration",
    Dim(L=1, M=1, T=-2): "force",
    Dim(L=2, M=1, T=-2): "energy",
    Dim(L=2, M=1, T=-3): "power",
    Dim(L=-1, M=1, T=-2): "pressure",
    Dim(T=-1): "frequency",
}


def describe_dim(dim: Dim) -> str:
    return _DIM_NAMES.get(dim, f"quantity of dimension {dim_unit_name(dim)}")
