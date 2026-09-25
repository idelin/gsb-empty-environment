"""Exact scanline polygon filling: multi-ring, holes, transforms, tiles.

Pure standard library (Python 3.11).  All topology is decided with
exact rational arithmetic and a fixed boundary convention -- never
with epsilon tolerances.
"""
from .geometry import Fraction, Point, Transform, point, to_frac
from .polygon import EVEN_ODD, NONZERO, Polygon
from .scanline import fill_row_spans, winding_at
from .canvas import Canvas
from .layers import Layer, Mask, composite

__all__ = [
    "Fraction", "Point", "Transform", "point", "to_frac",
    "EVEN_ODD", "NONZERO", "Polygon",
    "fill_row_spans", "winding_at",
    "Canvas", "Layer", "Mask", "composite",
]
