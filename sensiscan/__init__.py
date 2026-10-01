"""sensiscan: sensitive-data scanner for JSON-like output trees and
streaming text. Synthetic-data testing tool; not a privacy proof."""

from .core import (
    DictRule,
    Evidence,
    FieldRule,
    ScanBudget,
    ScanReport,
    Scanner,
    WhitelistEntry,
)
from .normalize import DEFAULT_PIPELINES, PIPELINES, normalize
from .stream import StreamScanner

__all__ = [
    "DictRule",
    "Evidence",
    "FieldRule",
    "ScanBudget",
    "ScanReport",
    "Scanner",
    "StreamScanner",
    "WhitelistEntry",
    "DEFAULT_PIPELINES",
    "PIPELINES",
    "normalize",
]
