"""精确同余求解（中国剩余定理，模数不必两两互素）。

全部在整数 tick 上运算，不使用浮点。无解时给出结构化的算术证据：
哪两条同余冲突、gcd 是多少、余数差为何不能被 gcd 整除。
"""

from __future__ import annotations

from dataclasses import dataclass
from math import gcd
from typing import Iterable, Tuple


@dataclass(frozen=True)
class Congruence:
    """t ≡ residue (mod modulus)，modulus 必须为正整数（tick 数）。"""

    residue: int
    modulus: int

    def __post_init__(self):
        if self.modulus <= 0:
            raise ValueError("模数必须为正")

    def normalized(self) -> "Congruence":
        return Congruence(self.residue % self.modulus, self.modulus)


@dataclass(frozen=True)
class Conflict:
    """两条同余互相排斥的算术证据。"""

    first: Congruence
    second: Congruence

    @property
    def divisor(self) -> int:
        return gcd(self.first.modulus, self.second.modulus)

    @property
    def residue_gap(self) -> int:
        return self.second.residue - self.first.residue

    def explain(self) -> str:
        a, b = self.first, self.second
        return (
            f"同余冲突: t ≡ {a.residue} (mod {a.modulus}) 与 "
            f"t ≡ {b.residue} (mod {b.modulus})；"
            f"gcd({a.modulus}, {b.modulus}) = {self.divisor} 不能整除余数差 "
            f"{self.residue_gap}（{self.residue_gap} % {self.divisor} = "
            f"{self.residue_gap % self.divisor}），故不存在同时满足的整数时刻"
        )


class NoCommonSolution(ArithmeticError):
    """同余方程组无解，携带全部冲突证据。"""

    def __init__(self, conflicts: Iterable[Conflict]):
        self.conflicts: Tuple[Conflict, ...] = tuple(conflicts)
        super().__init__(self.explain())

    def explain(self) -> str:
        return "；".join(c.explain() for c in self.conflicts)


def crt_pair(first: Congruence, second: Congruence) -> Congruence:
    """合并两条同余；无解时抛出 NoCommonSolution。"""
    a, b = first.normalized(), second.normalized()
    g = gcd(a.modulus, b.modulus)
    diff = b.residue - a.residue
    if diff % g != 0:
        raise NoCommonSolution([Conflict(first, second)])
    m1, m2 = a.modulus // g, b.modulus // g
    if m2 == 1:
        k = 0
    else:
        k = (diff // g) * pow(m1 % m2, -1, m2) % m2
    modulus = a.modulus * m2  # lcm(a.modulus, b.modulus)
    return Congruence((a.residue + a.modulus * k) % modulus, modulus)


def crt_all(congruences: Iterable[Congruence]) -> Congruence:
    """折叠合并一组同余，返回合并后的 (residue, lcm 模数)。"""
    items = list(congruences)
    if not items:
        raise ValueError("至少需要一条同余")
    acc = items[0].normalized()
    for cong in items[1:]:
        acc = crt_pair(acc, cong)
    return acc
