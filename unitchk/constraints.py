"""表达式与约束推导：把单位方程翻译成整数线性系统 A x = b。

未知单位的指数向量是整数变量；乘法/除法/整数幂只对应
指数向量的加/减/整数倍，因此约束保持线性，可精确求解。
"""
from __future__ import annotations

from dataclasses import dataclass, field

from . import core
from .core import QType, UnitKind, UnitTypeError
from .steps import StepLog


class Expr:
    """单位表达式基类。"""


@dataclass(frozen=True)
class Leaf(Expr):
    unit: str


@dataclass(frozen=True)
class Mul(Expr):
    left: Expr
    right: Expr


@dataclass(frozen=True)
class Div(Expr):
    left: Expr
    right: Expr


@dataclass(frozen=True)
class Pow(Expr):
    base: Expr
    exp: int


@dataclass(frozen=True)
class Equation:
    lhs: Expr
    rhs: Expr
    label: str = ""


def eval_known(expr: Expr, registry, log: StepLog) -> QType:
    """对已知单位表达式做联合类型检查（量纲 + 仿射规则）。"""
    if isinstance(expr, Leaf):
        unit = registry.units.get(expr.unit)
        if unit is None:
            raise UnitTypeError(f"未定义的单位: {expr.unit}", log.steps)
        log.record("leaf", "查表：单位的类型即其定义", expr.unit)
        return QType(unit.dim, unit.kind is UnitKind.ABSOLUTE)
    if isinstance(expr, Mul):
        return core.mul(eval_known(expr.left, registry, log),
                        eval_known(expr.right, registry, log), log)
    if isinstance(expr, Div):
        return core.div(eval_known(expr.left, registry, log),
                        eval_known(expr.right, registry, log), log)
    if isinstance(expr, Pow):
        return core.power(eval_known(expr.base, registry, log), expr.exp, log)
    raise TypeError(expr)


def check_equation(eq: Equation, registry, log: StepLog) -> QType:
    lt = eval_known(eq.lhs, registry, log)
    rt = eval_known(eq.rhs, registry, log)
    if lt != rt:
        raise UnitTypeError(
            f"方程 {eq.label or ''} 两侧类型不一致: {lt} vs {rt}", log.steps)
    log.record("equation", "等式两边类型完全一致（指数向量 + 仿射标志）", eq.label)
    return lt


@dataclass
class LinForm:
    """向量值线性型：const[j] + sum coeffs[(u, j)] * x[(u, j)]。"""
    const: list[int]
    coeffs: dict = field(default_factory=dict)  # (unit, j) -> int

    @staticmethod
    def zero(d: int) -> "LinForm":
        return LinForm([0] * d, {})

    def add(self, other: "LinForm", sign: int = 1) -> "LinForm":
        const = [a + sign * b for a, b in zip(self.const, other.const)]
        coeffs = dict(self.coeffs)
        for k, v in other.coeffs.items():
            coeffs[k] = coeffs.get(k, 0) + sign * v
            if coeffs[k] == 0:
                del coeffs[k]
        return LinForm(const, coeffs)

    def scale(self, k: int) -> "LinForm":
        return LinForm([k * c for c in self.const],
                       {key: k * v for key, v in self.coeffs.items()})


def derive_form(expr: Expr, registry, unknowns: set, log: StepLog) -> LinForm:
    d = len(registry.dimensions)
    if isinstance(expr, Leaf):
        if expr.unit in unknowns:
            log.record("derive-leaf", "未知单位：其指数向量作为整数变量", expr.unit)
            return LinForm([0] * d, {(expr.unit, j): 1 for j in range(d)})
        unit = registry.units.get(expr.unit)
        if unit is None:
            raise KeyError(f"未定义的单位: {expr.unit}")
        log.record("derive-leaf",
                   "已知单位：代入其整数指数向量（仿射偏移不进入量纲约束）", expr.unit)
        return LinForm(list(unit.dim), {})
    if isinstance(expr, Mul):
        log.record("derive-mul", "乘法约束：指数向量相加（保持线性）")
        return derive_form(expr.left, registry, unknowns, log).add(
            derive_form(expr.right, registry, unknowns, log))
    if isinstance(expr, Div):
        log.record("derive-div", "除法约束：指数向量相减（保持线性）")
        return derive_form(expr.left, registry, unknowns, log).add(
            derive_form(expr.right, registry, unknowns, log), sign=-1)
    if isinstance(expr, Pow):
        if not isinstance(expr.exp, int) or isinstance(expr.exp, bool):
            raise UnitTypeError("约束推导仅支持整数幂（保持整数线性）", log.steps)
        log.record("derive-pow", "整数幂约束：指数向量乘以整数（保持线性）", f"exp={expr.exp}")
        return derive_form(expr.base, registry, unknowns, log).scale(expr.exp)
    raise TypeError(expr)


@dataclass
class LinearSystem:
    A: list                 # m x n 整数矩阵
    b: list                 # 右端整数向量
    variables: list         # (unit, dim_index)
    var_names: list
    row_labels: list
    equations: list         # 原始方程，供独立代入验证
    unknowns: list
    registry: object


def build_system(equations, registry, unknowns) -> LinearSystem:
    log = StepLog()
    d = len(registry.dimensions)
    unknowns = sorted(unknowns)
    variables = [(u, j) for u in unknowns for j in range(d)]
    pos = {v: k for k, v in enumerate(variables)}
    A, b, labels = [], [], []
    for eq in equations:
        lf = derive_form(eq.lhs, registry, set(unknowns), log)
        rf = derive_form(eq.rhs, registry, set(unknowns), log)
        for j in range(d):
            row = [0] * len(variables)
            for (u, jj), c in lf.coeffs.items():
                if jj == j:
                    row[pos[(u, j)]] += c
            for (u, jj), c in rf.coeffs.items():
                if jj == j:
                    row[pos[(u, j)]] -= c
            A.append(row)
            b.append(rf.const[j] - lf.const[j])
            labels.append(f"{eq.label or '方程'}[{registry.dimensions[j]}]")
    var_names = [f"{u}[{registry.dimensions[j]}]" for u, j in variables]
    return LinearSystem(A, b, variables, var_names, labels,
                        list(equations), unknowns, registry)


def solution_unit_vectors(system: LinearSystem, solution) -> dict:
    """把解映射回每个未知单位：特解向量 + 自由方向（自由维度）。"""
    pos = {v: k for k, v in enumerate(system.variables)}
    d = len(system.registry.dimensions)
    out = {}
    for u in system.unknowns:
        idx = [pos[(u, j)] for j in range(d)]
        base = tuple(solution.particular[k] for k in idx)
        frees = []
        for vec, fcol in zip(solution.lattice, solution.free_columns):
            comp = tuple(vec[k] for k in idx)
            if any(comp):
                frees.append((solution.var_names[fcol], comp))
        out[u] = (base, frees)
    return out
