"""切割库存问题（cutting stock）的数据模型与切缝定义。

切缝（锯缝）损耗定义：
    一根原料上切出 k 段，若切完后仍有余料，需要 k 次切割（最后一段要与余料分离）；
    若恰好用尽（无余料），最后一段无需再切，需要 k-1 次切割。
    即可行条件为：
        sum(pieces) + kerf*k   <= L           （有余料，余料 = L - sum - kerf*k）
        sum(pieces) + kerf*(k-1) == L         （恰好用尽，余料 = 0）
    n 段只按上述对应的切割次数计入切缝损耗。

所有长度、成本、切缝一律用 fractions.Fraction 表示，支持合成整数或有理数，全程精确运算。
"""

from __future__ import annotations

from dataclasses import dataclass
from enum import Enum
from fractions import Fraction


def to_fraction(value) -> Fraction:
    """把 int / float / str / Fraction 统一转成 Fraction（精确有理数）。"""
    if isinstance(value, Fraction):
        return value
    if isinstance(value, bool):
        raise TypeError("布尔值不是合法的长度/成本")
    if isinstance(value, int):
        return Fraction(value)
    if isinstance(value, float):
        return Fraction(str(value))
    if isinstance(value, str):
        return Fraction(value)
    raise TypeError(f"无法转换为有理数: {value!r}")


@dataclass(frozen=True)
class StockSpec:
    """原料规格。

    length: 原料长度；cost: 单根成本；count: 可用根数，None 表示不限量。
    余料同样用 StockSpec 表示（cost=0、count 有限），从而天然支持余料复用。
    """

    length: Fraction
    cost: Fraction = Fraction(0)
    count: int | None = None

    @staticmethod
    def of(length, cost=0, count=None) -> "StockSpec":
        if count is not None:
            count = int(count)
            if count < 0:
                raise ValueError("库存数量不能为负")
        return StockSpec(to_fraction(length), to_fraction(cost), count)


@dataclass(frozen=True)
class Demand:
    """需求段：长度 + 数量。"""

    length: Fraction
    quantity: int

    @staticmethod
    def of(length, quantity) -> "Demand":
        quantity = int(quantity)
        if quantity < 0:
            raise ValueError("需求数量不能为负")
        return Demand(to_fraction(length), quantity)


def cut_metrics(stock_length: Fraction, pieces, kerf: Fraction):
    """按切缝定义计算 (切割次数, 余料长度)；不可行时抛 ValueError。"""
    pieces = tuple(pieces)
    k = len(pieces)
    if k == 0:
        return 0, stock_length
    base = sum(pieces, Fraction(0))
    if base + kerf * k <= stock_length:
        return k, stock_length - base - kerf * k
    if base + kerf * (k - 1) == stock_length:
        return k - 1, Fraction(0)
    raise ValueError(
        f"切割不可行: {k} 段共 {base}，切缝 {kerf}，超出原料长度 {stock_length}"
    )


def piece_fits(piece: Fraction, stock_length: Fraction, kerf: Fraction) -> bool:
    """单段是否可能放入某规格原料（含恰好等长、零次切割的特例）。"""
    return piece == stock_length or piece + kerf <= stock_length


@dataclass(frozen=True)
class BarCut:
    """单根原料的切割结果。"""

    stock_length: Fraction
    cost: Fraction
    pieces: tuple
    cuts: int
    leftover: Fraction

    @staticmethod
    def build(stock_length, cost, pieces, kerf) -> "BarCut":
        stock_length = to_fraction(stock_length)
        kerf = to_fraction(kerf)
        cuts, leftover = cut_metrics(stock_length, tuple(pieces), kerf)
        return BarCut(stock_length, to_fraction(cost), tuple(pieces), cuts, leftover)


class Status(Enum):
    """求解状态：最优 / 可行未证最优（预算耗尽）/ 已证不可行 / 预算耗尽且无可行解。"""

    OPTIMAL = "optimal"
    FEASIBLE = "feasible"
    INFEASIBLE = "infeasible"
    UNKNOWN = "unknown"


@dataclass
class SolveResult:
    """单次求解结果。

    waste:             毛损耗 = 消耗原料总长 - 需求总长（含切缝与全部余料）
    net_waste:         净损耗 = 毛损耗 - 回库的可复用余料
    lower_bound_waste: 损耗下界（松弛：库存不限量、忽略切缝与整段不可分性）
    """

    status: Status
    bars: list
    waste: Fraction | None
    cost: Fraction | None
    consumed: Fraction | None
    reusable_remnants: tuple
    net_waste: Fraction | None
    lower_bound_waste: Fraction
    nodes: int
    budget_exhausted: bool

    @property
    def proven_optimal(self) -> bool:
        return self.status is Status.OPTIMAL
