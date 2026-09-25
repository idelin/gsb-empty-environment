"""geomesh: transactional editing of planar meshes with exact arithmetic."""
from .exact import (
    make_point,
    on_segment,
    orient,
    polygon_is_simple,
    segments_intersect,
    signed_area2,
    strictly_on_segment,
    to_fraction,
)
from .model import Edge, Face, GeometryError, Mesh, Vertex
from .ops import Patch, Transaction, apply_entry

__all__ = [
    "Edge",
    "Face",
    "GeometryError",
    "Mesh",
    "Patch",
    "Transaction",
    "Vertex",
    "apply_entry",
    "make_point",
    "on_segment",
    "orient",
    "polygon_is_simple",
    "segments_intersect",
    "signed_area2",
    "strictly_on_segment",
    "to_fraction",
]
