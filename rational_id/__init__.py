"""有理多项式表达式恒等判定包（标准库实现）。"""
from .checker import (IDENTICAL, NOT_IDENTICAL, UNKNOWN, CheckResult, check)

__all__ = ["check", "CheckResult", "IDENTICAL", "NOT_IDENTICAL", "UNKNOWN"]
