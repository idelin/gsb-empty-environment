"""核心模型：整数指数向量、比例/仿射单位分离、联合类型运算规则。

- 乘幂单位表示为基维度上的整数指数向量，如 速度 = L^1 T^-1。
- 比例单位（ratio）与仿射绝对单位（absolute，如绝对温度）分开：
  绝对量默认不参与普通乘除/乘幂；两绝对量之差是差量（比例量），可参与。
"""
from __future__ import annotations

from dataclasses import dataclass, replace
from enum import Enum
from fractions import Fraction

from .steps import StepLog


class UnitKind(Enum):
    RATIO = "ratio"        # 比例单位：可自由参与乘除
    ABSOLUTE = "absolute"  # 仿射绝对单位：默认禁止乘除，差量可参与


class UnitTypeError(Exception):
    """单位类型错误，携带出错前已记录的变换步骤。"""

    def __init__(self, message, steps=()):
        super().__init__(message)
        self.steps = list(steps)


@dataclass(frozen=True)
class Unit:
    name: str
    dim: tuple[int, ...]               # 整数指数向量
    scale: Fraction                    # 相对一致基准的精确比例因子
    kind: UnitKind = UnitKind.RATIO
    offset: Fraction = Fraction(0)     # 绝对仿射：基准值 = scale * x + offset
    unknown: bool = False


@dataclass(frozen=True)
class QType:
    """量的类型：整数指数向量 + 是否为绝对仿射量。"""
    dim: tuple[int, ...]
    absolute: bool = False


class Registry:
    """单位注册表：维护维度符号表与单位定义。"""

    def __init__(self, dimensions=()):
        self.dimensions = list(dimensions)
        self.index = {s: i for i, s in enumerate(self.dimensions)}
        self.units: dict[str, Unit] = {}
        self._unknown_seq = 0

    def vector(self, dim) -> tuple[int, ...]:
        if dim is None:
            return (0,) * len(self.dimensions)
        if isinstance(dim, dict):
            v = [0] * len(self.dimensions)
            for sym, e in dim.items():
                if sym not in self.index:
                    raise KeyError(f"未知维度符号: {sym}")
                v[self.index[sym]] = int(e)
            return tuple(v)
        dim = tuple(int(e) for e in dim)
        if len(dim) != len(self.dimensions):
            raise ValueError("指数向量长度与维度数不一致")
        return dim

    def define(self, name, dim=None, scale=1, kind=UnitKind.RATIO, offset=0) -> Unit:
        scale = Fraction(scale)
        offset = Fraction(offset)
        if kind is UnitKind.RATIO and offset != 0:
            raise ValueError("比例单位不允许带仿射偏移（比例与偏移分开）")
        unit = Unit(name, self.vector(dim), scale, kind, offset)
        self.units[name] = unit
        return unit

    def unknown(self, name=None) -> Unit:
        """注册未知单位：为其分配一个自由维度。"""
        if name is None:
            self._unknown_seq += 1
            name = f"X{self._unknown_seq}"
        if name in self.units:
            raise ValueError(f"单位已存在: {name}")
        sym = f"dim<{name}>"
        self.dimensions.append(sym)
        self.index[sym] = len(self.dimensions) - 1
        for uname, u in self.units.items():
            self.units[uname] = replace(u, dim=u.dim + (0,))
        dim = (0,) * (len(self.dimensions) - 1) + (1,)
        unit = Unit(name, dim, Fraction(1), UnitKind.RATIO, Fraction(0), unknown=True)
        self.units[name] = unit
        return unit

    def fmt_dim(self, dim) -> str:
        parts = []
        for sym, e in zip(self.dimensions, dim):
            if e == 0:
                continue
            parts.append(sym if e == 1 else f"{sym}^{e}")
        return "·".join(parts) if parts else "1"


def _dim_str(dim) -> str:
    return "(" + ",".join(str(e) for e in dim) + ")"


def add(t1: QType, t2: QType, log: StepLog) -> QType:
    if t1.dim != t2.dim:
        raise UnitTypeError(
            f"量纲不同，不能相加: {_dim_str(t1.dim)} vs {_dim_str(t2.dim)}", log.steps)
    if t1.absolute and t2.absolute:
        raise UnitTypeError(
            "两个绝对仿射量不能相加（适用域：加法至少一侧须为差量/比例量）", log.steps)
    log.record("add", "同量纲相加；绝对+差量→绝对，比例+比例→比例")
    return QType(t1.dim, t1.absolute or t2.absolute)


def sub(t1: QType, t2: QType, log: StepLog) -> QType:
    if t1.dim != t2.dim:
        raise UnitTypeError(
            f"量纲不同，不能相减: {_dim_str(t1.dim)} vs {_dim_str(t2.dim)}", log.steps)
    if t2.absolute and not t1.absolute:
        raise UnitTypeError(
            "比例量不能减绝对仿射量（适用域：被减数为绝对量时，减数须为同量纲绝对量或差量）",
            log.steps)
    log.record("sub", "同量纲相减；绝对−绝对→差量(比例)，绝对−差量→绝对，比例−比例→比例")
    return QType(t1.dim, t1.absolute and not t2.absolute)


def mul(t1: QType, t2: QType, log: StepLog) -> QType:
    if t1.absolute or t2.absolute:
        raise UnitTypeError(
            "绝对仿射量（如绝对温度）不参与普通乘法；请先作差得到差量", log.steps)
    log.record("mul", "比例量相乘：整数指数向量逐维相加")
    return QType(tuple(a + b for a, b in zip(t1.dim, t2.dim)), False)


def div(t1: QType, t2: QType, log: StepLog) -> QType:
    if t1.absolute or t2.absolute:
        raise UnitTypeError(
            "绝对仿射量不参与普通除法；请先作差得到差量", log.steps)
    log.record("div", "比例量相除：整数指数向量逐维相减")
    return QType(tuple(a - b for a, b in zip(t1.dim, t2.dim)), False)


def power(t: QType, exp: int, log: StepLog) -> QType:
    if not isinstance(exp, int) or isinstance(exp, bool):
        raise UnitTypeError(
            "指数必须为整数（适用域：整数幂，保证指数向量仍为整数）", log.steps)
    if t.absolute:
        raise UnitTypeError("绝对仿射量不参与乘幂；请先作差得到差量", log.steps)
    log.record("pow", "比例量整数幂：指数向量逐维乘以整数指数", f"exp={exp}")
    return QType(tuple(exp * a for a in t.dim), False)


def expect(t: QType, unit: Unit, log: StepLog, what: str = "结果") -> QType:
    """断言类型与目标单位完全一致（指数向量 + 仿射标志）。"""
    want = QType(unit.dim, unit.kind is UnitKind.ABSOLUTE)
    if t != want:
        raise UnitTypeError(
            f"{what}类型不符: 得到 {_dim_str(t.dim)}"
            f"{'(绝对)' if t.absolute else ''}，期望 {unit.name} "
            f"{_dim_str(unit.dim)}{'(绝对)' if want.absolute else ''}",
            log.steps)
    log.record("expect", "类型等价：指数向量与仿射标志完全一致", f"{what} == {unit.name}")
    return t
