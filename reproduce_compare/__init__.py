"""Reproducibility comparison library for directory trees and nested ZIPs."""

from __future__ import annotations

from .archive import Budget
from .compare import (
    DIFFERENT,
    EQUAL,
    EQUIVALENT,
    UNDETERMINABLE,
    ComparisonResult,
    compare_trees,
)
from .evidence import build_bundle, read_bundle, write_bundle
from .rules import RuleError, load_rules
from .verify import VerificationResult, verify_bundle

__all__ = [
    "Budget",
    "DIFFERENT",
    "EQUAL",
    "EQUIVALENT",
    "UNDETERMINABLE",
    "ComparisonResult",
    "compare_trees",
    "build_bundle",
    "read_bundle",
    "write_bundle",
    "RuleError",
    "load_rules",
    "VerificationResult",
    "verify_bundle",
]
