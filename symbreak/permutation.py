"""变量置换：构造、验证双射、复合、求逆、作用在赋值上。"""
from __future__ import annotations

from typing import Any, Iterable


class Permutation:
    """变量名集合上的双射。

    mapping 可以只给出非不动点的映射，未出现的变量视为不动。
    构造时强制验证确为双射，非法映射直接抛出 ValueError。
    """

    def __init__(self, mapping: dict[str, str], all_names: Iterable[str]):
        names = tuple(all_names)
        known = set(names)
        extra = set(mapping) - known
        if extra:
            raise ValueError(f"置换包含未知变量: {sorted(extra)}")
        full = {n: mapping.get(n, n) for n in names}
        bad_targets = set(full.values()) - known
        if bad_targets:
            raise ValueError(f"置换的像包含未知变量: {sorted(bad_targets)}")
        if len(set(full.values())) != len(full):
            raise ValueError("给定映射不是双射，拒绝作为对称置换")
        self._names = names
        self._map = full
        self._key = tuple(full[n] for n in names)

    @staticmethod
    def identity(names: Iterable[str]) -> "Permutation":
        return Permutation({}, names)

    @staticmethod
    def transposition(names: Iterable[str], a: str, b: str) -> "Permutation":
        return Permutation({a: b, b: a}, names)

    @property
    def names(self) -> tuple[str, ...]:
        return self._names

    def image(self, name: str) -> str:
        return self._map[name]

    def moved(self) -> frozenset[str]:
        return frozenset(n for n in self._names if self._map[n] != n)

    def inverse(self) -> "Permutation":
        inv = {v: k for k, v in self._map.items()}
        return Permutation(inv, self._names)

    def compose(self, other: "Permutation") -> "Permutation":
        """self ∘ other：先作用 other，再作用 self。"""
        if set(self._names) != set(other._names):
            raise ValueError("两个置换作用的变量集合不同，无法复合")
        return Permutation(
            {n: self._map[other._map[n]] for n in self._names}, self._names
        )

    def apply(self, assignment: dict[str, Any]) -> dict[str, Any]:
        """置换作用在赋值上：(p·a)[p(v)] = a[v]。

        允许部分赋值：未在置换表中出现的变量名保持不变。
        """
        return {self._map.get(k, k): v for k, v in assignment.items()}

    def __eq__(self, other: object) -> bool:
        return (
            isinstance(other, Permutation)
            and self._names == other._names
            and self._key == other._key
        )

    def __hash__(self) -> int:
        return hash((self._names, self._key))

    def __repr__(self) -> str:
        moved = {n: self._map[n] for n in sorted(self.moved())}
        if not moved:
            return "Perm(id)"
        body = ", ".join(f"{k}->{v}" for k, v in moved.items())
        return f"Perm({body})"
