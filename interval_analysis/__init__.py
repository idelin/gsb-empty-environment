"""Interval analysis for small integer programs.

Public API:
    interval  -- the interval abstract domain
    lang      -- program AST and concrete (replayable) interpreter
    analyzer  -- abstract interpreter with widening / optional narrowing
    verifier  -- explicit-state verifier for finite-state models
"""

from .interval import Interval, BOTTOM, TOP
from .analyzer import Analyzer, AnalysisResult, Diagnostic
from .verifier import Verifier, Verdict, VerificationResult

__all__ = [
    "Interval",
    "BOTTOM",
    "TOP",
    "Analyzer",
    "AnalysisResult",
    "Diagnostic",
    "Verifier",
    "Verdict",
    "VerificationResult",
]
