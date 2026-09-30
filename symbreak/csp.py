"""CSP 问题建模：有限域变量、谓词约束、可选目标函数。

只使用标准库；变量域必须有限。求解器不调用任何 SAT/SMT 库，
采用普通回溯枚举（面向小规模问题）。
"""
from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any, Callable, Optional


@dataclass(frozen=True)
class Variable:
    """有限域变量。

    special=True 表示"特殊角色"变量：任何对称置换都必须保持它不动。
    """

    name: str
    domain: tuple[Any, ...]
    special: bool = False

    def __post_init__(self) -> None:
        if not self.domain:
            raise ValueError(f"变量 {self.name} 的域不能为空")
        if len(set(self.domain)) != len(self.domain):
            raise ValueError(f"变量 {self.name} 的域含有重复值")


@dataclass(frozen=True)
class Constraint:
    """谓词约束：predicate 按 scope 顺序接收各变量的取值，返回 bool。"""

    name: str
    scope: tuple[str, ...]
    predicate: Callable[..., bool]

    def satisfied(self, assignment: dict[str, Any]) -> bool:
        """assignment 必须覆盖 scope 中所有变量。"""
        return bool(self.predicate(*(assignment[v] for v in self.scope)))


@dataclass
class Problem:
    """有限域 CSP。

    constraints     : 原始约束（自同构验证、独立检查器只针对它们）。
    objective/sense : 可选目标函数，自同构必须保持其取值不变。
    lex_constraints : 验证通过后追加的对称破缺（字典序代表）约束，
                      求解时使用，但不属于"原始问题"。
    """

    variables: tuple[Variable, ...]
    constraints: tuple[Constraint, ...]
    objective: Optional[Callable[[dict[str, Any]], float]] = None
    sense: str = "min"
    lex_constraints: list[Constraint] = field(default_factory=list)

    def __post_init__(self) -> None:
        names = [v.name for v in self.variables]
        if len(set(names)) != len(names):
            raise ValueError("变量名必须互不相同")
        known = set(names)
        for c in self.constraints:
            unknown = set(c.scope) - known
            if unknown:
                raise ValueError(f"约束 {c.name} 引用了未知变量: {sorted(unknown)}")
        if self.sense not in ("min", "max"):
            raise ValueError("sense 必须是 'min' 或 'max'")

    @property
    def names(self) -> tuple[str, ...]:
        return tuple(v.name for v in self.variables)

    @property
    def domains(self) -> dict[str, tuple[Any, ...]]:
        return {v.name: v.domain for v in self.variables}

    def active_constraints(self, with_lex: bool = True) -> list[Constraint]:
        """求解时生效的约束；with_lex=False 表示只求解原始问题。"""
        if with_lex:
            return list(self.constraints) + list(self.lex_constraints)
        return list(self.constraints)
