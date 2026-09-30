"""sensiscan: sensitive-field scanner for nested output trees.

Synthetic data only.  See README.md for parameter definitions and the
scope/limitations of the evaluation.
"""
from .report import Evidence, Report
from .scanner import Budget, Rule, WhitelistEntry, scan_document
from .stream import StreamScanner

__all__ = [
    "Budget",
    "Evidence",
    "Report",
    "Rule",
    "StreamScanner",
    "WhitelistEntry",
    "scan_document",
]
