"""核心数据模型：事实、规则、步骤均带稳定 ID 与版本。"""

from __future__ import annotations

import hashlib
from dataclasses import dataclass


def stable_hash(parts) -> str:
    """对任意可 str 化的部分序列生成稳定短哈希（跨进程、跨运行一致）。"""
    h = hashlib.sha256()
    for part in parts:
        h.update(str(part).encode("utf-8"))
        h.update(b"\x00")
    return h.hexdigest()[:12]


def _items(payload: dict) -> tuple:
    return tuple(sorted(payload.items()))


@dataclass(frozen=True)
class Fact:
    """断言事实：稳定 id + 显式版本 + 不可变载荷。"""

    id: str
    version: int
    payload: tuple = ()

    @staticmethod
    def make(fid: str, version: int, **payload) -> "Fact":
        return Fact(fid, version, _items(payload))

    def get(self, key, default=None):
        return dict(self.payload).get(key, default)


@dataclass(frozen=True, order=True)
class FactRef:
    """对某个事实特定版本的引用。"""

    id: str
    version: int


_OPS = {
    "eq": lambda a, e: a == e,
    "ne": lambda a, e: a != e,
    "gt": lambda a, e: a is not None and a > e,
    "ge": lambda a, e: a is not None and a >= e,
    "lt": lambda a, e: a is not None and a < e,
    "le": lambda a, e: a is not None and a <= e,
}


@dataclass(frozen=True)
class Cond:
    """规则条件：对某事实（按 id）的某个键做比较，或仅要求事实存在。"""

    fact_id: str
    key: str = ""
    op: str = "present"
    value: object = None

    @staticmethod
    def present(fact_id: str) -> "Cond":
        return Cond(fact_id)

    def matches(self, facts: dict) -> bool:
        fact = facts.get(self.fact_id)
        if fact is None:
            return False
        if self.op == "present":
            return True
        return _OPS[self.op](fact.get(self.key), self.value)


@dataclass(frozen=True)
class Rule:
    """规则：稳定 id + 版本。同 id 内容变更必须通过不同 version 区分。"""

    id: str
    version: int
    conditions: tuple
    conclusion_id: str
    conclusion_payload: tuple = ()

    @staticmethod
    def make(rid, version, conditions, conclusion_id, **payload) -> "Rule":
        return Rule(rid, version, tuple(conditions), conclusion_id, _items(payload))


@dataclass(frozen=True)
class Step:
    """推导步骤：id 为 (规则id, 规则版本, 输入事实引用, 输出) 的内容哈希，稳定可重放。"""

    id: str
    rule_id: str
    rule_version: int
    inputs: tuple  # tuple[FactRef]
    output: FactRef


class Snapshot:
    """某一观察时刻的全部输入：断言事实集 + 规则集。"""

    def __init__(self, label: str, facts, rules):
        self.label = label
        self.facts = {f.id: f for f in facts}
        self.rules = {r.id: r for r in rules}

    def derive(self):
        from .engine import derive

        return derive(self)
