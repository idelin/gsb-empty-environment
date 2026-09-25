"""rulecov: coverage / overlap / conflict analysis for priority rule sets."""
from .model import Spec, SpecError, load_spec, evaluate
from .analyze import analyze, GENERATOR
from .evidence import verify_report

__all__ = ["Spec", "SpecError", "load_spec", "evaluate", "analyze",
           "verify_report", "GENERATOR"]
__version__ = GENERATOR["version"]
