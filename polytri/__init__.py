"""Exact triangulation of simple polygons with holes (stdlib only)."""

from .preprocess import PolygonError, clean_ring, normalize_polygon
from .triangulate import Triangle, TriangulationError, triangulate
from .validate import ValidationReport, validate_triangulation

__all__ = [
    "PolygonError",
    "Triangle",
    "TriangulationError",
    "ValidationReport",
    "clean_ring",
    "normalize_polygon",
    "triangulate",
    "validate_triangulation",
]
