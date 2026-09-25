"""Layered compositing of exact 0/1 coverage.

Every Mask is produced by the same exact scanline core, so coverage is
a well-defined bit per pixel: compositing layers with clips and boolean
masks cannot introduce cracks or double-counted coverage along shared
edges -- the bits either combine or they do not.
"""
from __future__ import annotations

from fractions import Fraction
from typing import Optional, Sequence

from .geometry import Transform, to_frac
from .polygon import Polygon
from .scanline import fill_row_spans

_HALF = Fraction(1, 2)


class Mask:
    """A width x height bitmap of exact 0/1 coverage."""

    def __init__(self, width: int, height: int, data=None):
        self.width = width
        self.height = height
        self.data = bytearray(data) if data is not None else bytearray(width * height)
        if len(self.data) != width * height:
            raise ValueError("mask data size mismatch")

    @classmethod
    def from_polygon(cls, polygon: Polygon, width: int, height: int,
                     origin=(0, 0), transform: Optional[Transform] = None) -> "Mask":
        poly = polygon.transformed(transform) if transform is not None else polygon
        ox = to_frac(origin[0])
        oy = to_frac(origin[1])
        mask = cls(width, height)
        for py in range(height):
            yc = oy + py + _HALF
            for s, e in fill_row_spans(poly.rings, yc, 0, width, poly.fill_rule, ox):
                base = py * width
                mask.data[base + s: base + e] = b"\x01" * (e - s)
        return mask

    def get(self, px: int, py: int) -> int:
        return self.data[py * self.width + px]

    def count(self) -> int:
        return sum(self.data)

    def _check(self, other: "Mask") -> None:
        if (self.width, self.height) != (other.width, other.height):
            raise ValueError("mask size mismatch")

    def _combine(self, other: "Mask", op) -> "Mask":
        self._check(other)
        return Mask(self.width, self.height,
                    bytes(op(a, b) for a, b in zip(self.data, other.data)))

    def __and__(self, other: "Mask") -> "Mask":
        return self._combine(other, lambda a, b: a & b)

    def __or__(self, other: "Mask") -> "Mask":
        return self._combine(other, lambda a, b: a | b)

    def __xor__(self, other: "Mask") -> "Mask":
        return self._combine(other, lambda a, b: a ^ b)

    def __sub__(self, other: "Mask") -> "Mask":
        return self._combine(other, lambda a, b: a & (1 - b))

    def __invert__(self) -> "Mask":
        return Mask(self.width, self.height, bytes(1 - a for a in self.data))


class Layer:
    """A coverage mask with an optional clip and an optional boolean mask.

    Effective coverage is ``mask AND clip AND select``; omitted parts
    default to "everything".
    """

    def __init__(self, mask: Mask, clip: Optional[Mask] = None,
                 select: Optional[Mask] = None):
        self.mask = mask
        self.clip = clip
        self.select = select

    def effective(self) -> Mask:
        result = self.mask
        if self.clip is not None:
            result = result & self.clip
        if self.select is not None:
            result = result & self.select
        return result


def composite(layers: Sequence[Layer], width: int, height: int) -> Mask:
    """Blend layers bottom-to-top under the unified 0/1 coverage rule.

    With exact binary coverage, "over" is union: a pixel is covered iff
    some layer's effective coverage covers it.
    """
    out = Mask(width, height)
    for layer in layers:
        out = out | layer.effective()
    return out
