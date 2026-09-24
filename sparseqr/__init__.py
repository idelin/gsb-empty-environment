"""Sparse QR least squares for small/medium matrices (pure standard library)."""

from .sparse_matrix import SparseMatrix
from .qr import QRLSResult, qr_least_squares

__all__ = ["SparseMatrix", "QRLSResult", "qr_least_squares"]
