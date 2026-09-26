"""Elementwise fusion engine with differential interpreters and verification."""

from .autodiff import backward
from .diff import run_exhaustive
from .fuser import (Evidence, IllegalFusion, Kernel, attempt_fuse,
                    compile_program, format_plan)
from .interp import ElementwiseError, Stats, run_fused, run_unfused
from .program import Op, Program
from .tensor import Tensor
from .verify import Comparison, Verdict, compare_tensors, verify_gradients

__all__ = [
    "Tensor", "Program", "Op",
    "compile_program", "attempt_fuse", "format_plan",
    "Evidence", "IllegalFusion",
    "run_unfused", "run_fused", "Stats", "ElementwiseError",
    "backward", "verify_gradients", "compare_tensors", "Comparison", "Verdict",
    "run_exhaustive",
]
