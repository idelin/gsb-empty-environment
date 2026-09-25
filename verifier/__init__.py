"""Streamable-filter verifier: impulse / sweep / multitone tests,
chunk- and shift-invariance counterexample search with shrinking, and
conservative FIR coefficient certificates.  Standard library only.
"""
from .spec import FilterSpec
from .core import verify
from .reference import StreamingFIR, design_lowpass
from .fixtures import FixtureSet, generate_fixtures
from .report import CheckResult, VerificationReport

__all__ = [
    "FilterSpec",
    "verify",
    "StreamingFIR",
    "design_lowpass",
    "FixtureSet",
    "generate_fixtures",
    "CheckResult",
    "VerificationReport",
]

__version__ = "0.1.0"
