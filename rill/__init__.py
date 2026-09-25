"""Rill: a small language with structural records and read/write variance."""

from .errors import CompileError, EvalError
from .runner import compile_source, run_file, run_source

__all__ = ["compile_source", "run_source", "run_file", "CompileError", "EvalError"]
