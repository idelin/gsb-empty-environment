"""单位注册表、量纲运算与单位换算。

量纲用 ((轴, 指数), ...) 的元组表示，轴固定为 mass/length/time/temp。
单位分三类：linear（线性，如 m、s、K）、affine（仿射绝对温度 °C/°F）、
delta（温差 Δ°C/Δ°F/ΔK）。精确值一律用 Fraction 表示。
"""
from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction

from .errors import LangError

AXES = ("mass", "length", "time", "temp")
AXIS_SYMBOL = {"mass": "kg", "length": "m", "time": "s", "temp": "K"}


def make_dim(**exps: int) -> tuple:
    return tuple((axis, exps[axis]) for axis in AXES if exps.get(axis))


def dim_combine(a: tuple, b: tuple, sign: int = 1) -> tuple:
    acc = dict(a)
    for axis, exp in b:
        acc[axis] = acc.get(axis, 0) + sign * exp
    return tuple((axis, acc[axis]) for axis in AXES if acc.get(axis))


def dim_scale(dim: tuple, n: int) -> tuple:
    return tuple((axis, exp * n) for axis, exp in dim if exp * n)


def dim_name(dim: tuple) -> str:
    """由基本单位符号拼出量纲的规范名称，如 kg*m/s^2。"""
    if not dim:
        return ""
    pos, neg = [], []
    for axis, exp in dim:
        sym = AXIS_SYMBOL[axis]
        if exp > 0:
            pos.append(sym if exp == 1 else f"{sym}^{exp}")
        else:
            neg.append(sym if exp == -1 else f"{sym}^{-exp}")
    name = "*".join(pos) if pos else "1"
    if neg:
        name += "/" + "*".join(neg)
    return name


@dataclass(frozen=True)
class Unit:
    factor: Fraction  # 1 单位 = factor 个基本单位（仿射单位另加 offset）
    offset: Fraction
    dim: tuple
    kind: str  # 'linear' | 'affine' | 'delta'
    name: str


def role_of(unit: Unit) -> str:
    return {"affine": "absolute", "delta": "delta"}.get(unit.kind, "none")


DIMENSIONLESS = Unit(Fraction(1), Fraction(0), (), "linear", "")
TEMP_DIM = make_dim(temp=1)

UNITS: dict[str, Unit] = {}


def _register(unit: Unit) -> None:
    UNITS[unit.name] = unit


def _linear(name: str, factor, dim: tuple) -> None:
    _register(Unit(Fraction(factor), Fraction(0), dim, "linear", name))


_LENGTH = make_dim(length=1)
_TIME = make_dim(time=1)
_MASS = make_dim(mass=1)

_linear("m", 1, _LENGTH)
_linear("km", 1000, _LENGTH)
_linear("cm", Fraction(1, 100), _LENGTH)
_linear("mm", Fraction(1, 1000), _LENGTH)
_linear("um", Fraction(1, 10**6), _LENGTH)
_linear("nm", Fraction(1, 10**9), _LENGTH)
_linear("in", Fraction(127, 5000), _LENGTH)      # 0.0254 m（精确）
_linear("ft", Fraction(381, 1250), _LENGTH)      # 0.3048 m（精确）
_linear("yd", Fraction(1143, 1250), _LENGTH)     # 0.9144 m（精确）
_linear("mi", Fraction(201168, 125), _LENGTH)    # 1609.344 m（精确）

_linear("s", 1, _TIME)
_linear("ms", Fraction(1, 1000), _TIME)
_linear("us", Fraction(1, 10**6), _TIME)
_linear("min", 60, _TIME)
_linear("h", 3600, _TIME)
_linear("day", 86400, _TIME)

_linear("kg", 1, _MASS)
_linear("g", Fraction(1, 1000), _MASS)
_linear("mg", Fraction(1, 10**6), _MASS)
_linear("t", 1000, _MASS)
_linear("lb", Fraction(45359237, 10**8), _MASS)  # 0.45359237 kg（精确）

_linear("L", Fraction(1, 1000), make_dim(length=3))
_linear("mL", Fraction(1, 10**6), make_dim(length=3))

_linear("N", 1, make_dim(mass=1, length=1, time=-2))
_linear("J", 1, make_dim(mass=1, length=2, time=-2))
_linear("W", 1, make_dim(mass=1, length=2, time=-3))
_linear("Pa", 1, make_dim(mass=1, length=-1, time=-2))

_linear("K", 1, TEMP_DIM)


def _affine(name: str, factor, offset) -> None:
    _register(Unit(Fraction(factor), Fraction(offset), TEMP_DIM, "affine", name))


def _delta(name: str, factor) -> None:
    _register(Unit(Fraction(factor), Fraction(0), TEMP_DIM, "delta", name))


# 仿射（绝对）温度：K = 读数 * factor + offset
_affine("°C", 1, Fraction(5463, 20))                 # 273.15
_affine("degC", 1, Fraction(5463, 20))
_affine("°F", Fraction(5, 9), Fraction(45967, 180))  # 273.15 - 32*5/9
_affine("degF", Fraction(5, 9), Fraction(45967, 180))

# 温差（线性，无偏移）
_delta("Δ°C", 1)
_delta("ddegC", 1)
_delta("Δ°F", Fraction(5, 9))
_delta("ddegF", Fraction(5, 9))
_delta("ΔK", 1)
_delta("dK", 1)

# 绝对温度相减时，结果单位取左操作数对应的温差单位
DELTA_OF = {"°C": "Δ°C", "degC": "ddegC", "°F": "Δ°F", "degF": "ddegF"}


def resolve_unit_expr(terms) -> Unit:
    """把解析出的单位表达式（UnitTerm 列表）解析为 Unit。"""
    if len(terms) == 1 and terms[0].exp == 1:
        return UNITS[terms[0].name]
    factor = Fraction(1)
    dim: tuple = ()
    pos, neg = [], []
    for term in terms:
        unit = UNITS[term.name]
        if unit.kind != "linear":
            kind_zh = "仿射温度" if unit.kind == "affine" else "温差"
            raise LangError(
                f"{kind_zh}单位 {term.name} 不能带指数或用于复合单位",
                term.start,
                term.end,
            )
        factor *= unit.factor ** term.exp
        dim = dim_combine(dim, dim_scale(unit.dim, term.exp))
        text = term.name if abs(term.exp) == 1 else f"{term.name}^{abs(term.exp)}"
        (pos if term.exp > 0 else neg).append(text)
    name = "*".join(pos) if pos else "1"
    if neg:
        name += "/" + "*".join(neg)
    return Unit(factor, Fraction(0), dim, "linear", name)


def convert_mag(mag: Fraction, from_unit: Unit, to_unit: Unit, as_delta: bool = False) -> Fraction:
    """把数值从 from_unit 换算到 to_unit；as_delta 时忽略仿射偏移。"""
    if as_delta:
        return mag * from_unit.factor / to_unit.factor
    return (mag * from_unit.factor + from_unit.offset - to_unit.offset) / to_unit.factor
