"""reprodiff: evidence-producing reproducibility comparator.

Compares two directory trees (including nested ZIP archives) and produces
a verdict plus an evidence package.  An independent verifier can re-confirm
the conclusion using only the two raw inputs, the rules and the evidence.
"""

from .compare import Comparator, ComparisonResult, compare_trees
from .evidence import load_evidence, save_evidence
from .model import EVIDENCE_FORMAT, Limits, Verdict
from .rules import JsonKeyRule, RuleError, RuleSet, ZipTimestampRule
from .verify import VerificationReport, verify_evidence

__all__ = [
    "Comparator",
    "ComparisonResult",
    "compare_trees",
    "load_evidence",
    "save_evidence",
    "EVIDENCE_FORMAT",
    "Limits",
    "Verdict",
    "JsonKeyRule",
    "RuleError",
    "RuleSet",
    "ZipTimestampRule",
    "VerificationReport",
    "verify_evidence",
]
