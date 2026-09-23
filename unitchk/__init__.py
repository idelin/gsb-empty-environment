"""unitchk：单位/量纲联合类型检查与整数精确约束求解。

- 乘幂单位 = 整数指数向量；比例与仿射偏移单位分开；
- 未知单位：约束求解（整数解格 / 不相容证据）或分配自由维度；
- 版本化转换图 + 独立代入验证；全程有理数精确计算。
"""
from .core import (QType, Registry, Unit, UnitKind, UnitTypeError,
                   add, div, expect, mul, power, sub)
from .constraints import (Div, Equation, Leaf, LinearSystem, Mul, Pow,
                          build_system, check_equation, eval_known,
                          solution_unit_vectors)
from .graph import (AffineMap, Contradiction, ConversionGraph, Edge,
                    conversion_between)
from .solver import Infeasible, Solution, solve_integer_system
from .steps import Step, StepLog
from . import verify

__all__ = [
    "QType", "Registry", "Unit", "UnitKind", "UnitTypeError",
    "add", "sub", "mul", "div", "power", "expect",
    "Leaf", "Mul", "Div", "Pow", "Equation", "LinearSystem",
    "build_system", "check_equation", "eval_known", "solution_unit_vectors",
    "AffineMap", "Contradiction", "ConversionGraph", "Edge", "conversion_between",
    "Solution", "Infeasible", "solve_integer_system",
    "Step", "StepLog", "verify",
]
