"""Zero-dependency COO/CSR/CSC sparse matrices, views, transactions, checksums."""

from .dense import DenseMatrix
from .matrix import (
    CSC,
    CSR,
    ELIMINATE,
    KEEP,
    SliceView,
    SparseMatrix,
    Transaction,
)
from .pages import DEFAULT_PAGE_SIZE, STATS, PagedBuffer, reset_stats
from .serialize import dump_batch, dumps, load_batch, loads

__all__ = [
    "CSR",
    "CSC",
    "ELIMINATE",
    "KEEP",
    "DenseMatrix",
    "PagedBuffer",
    "SliceView",
    "SparseMatrix",
    "Transaction",
    "STATS",
    "DEFAULT_PAGE_SIZE",
    "reset_stats",
    "dumps",
    "loads",
    "dump_batch",
    "load_batch",
]
