"""sparsepoly：稀疏多变量多项式（Q 系数）+ 预算化惰性求值 + 外存归并。"""

from .budget import Budget, StepsExhausted
from .core import Polynomial, monomial_sort_key
from .lazy import Add, Deriv, Expr, Leaf, Mul, evaluate_fully

__all__ = [
    "Add",
    "Budget",
    "Deriv",
    "Expr",
    "Leaf",
    "Mul",
    "Polynomial",
    "StepsExhausted",
    "evaluate_fully",
    "monomial_sort_key",
]
