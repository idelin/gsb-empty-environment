"""加权编辑成本模型。

所有成本以 Unicode 码点（Python ``str`` 的字符）为单位。
``transpose`` 表示相邻换位（Damerau 风格），默认 ``math.inf`` 即禁用。
``min_insert`` / ``min_delete`` 是成本模型声明的全局下界，
用于可证明安全的长度差下界剪枝（见 distance.length_lower_bound）。
"""
from __future__ import annotations

import math
from dataclasses import dataclass
from typing import Callable, Dict, Optional, Tuple

INF = math.inf


@dataclass(frozen=True)
class CostModel:
    insert: Callable[[str], float]
    delete: Callable[[str], float]
    substitute: Callable[[str, str], float]
    transpose: Callable[[str, str], float]
    min_insert: float = 0.0
    min_delete: float = 0.0

    @staticmethod
    def uniform(
        ins: float = 1.0,
        delete: Optional[float] = None,
        sub: float = 1.0,
        trans: float = INF,
    ) -> "CostModel":
        """均一成本。``delete`` 缺省等于 ``ins``；``trans`` 缺省禁用。"""
        if delete is None:
            delete = ins
        return CostModel(
            insert=lambda c: ins,
            delete=lambda c: delete,
            substitute=lambda a, b: 0.0 if a == b else sub,
            transpose=lambda a, b: trans,
            min_insert=ins,
            min_delete=delete,
        )

    @staticmethod
    def from_tables(
        ins: Dict[str, float],
        delete: Dict[str, float],
        sub: Dict[Tuple[str, str], float],
        trans: Optional[Dict[Tuple[str, str], float]] = None,
        *,
        default_ins: float,
        default_delete: float,
        default_sub: float,
        default_trans: float = INF,
    ) -> "CostModel":
        """表驱动成本；缺项回落到 default_*。min_* 取表值与默认值的最小值。"""
        trans = trans or {}

        def _sub(a: str, b: str) -> float:
            if a == b:
                return 0.0
            if (a, b) in sub:
                return sub[(a, b)]
            return sub.get((b, a), default_sub)

        def _trans(a: str, b: str) -> float:
            if (a, b) in trans:
                return trans[(a, b)]
            return trans.get((b, a), default_trans)

        return CostModel(
            insert=lambda c: ins.get(c, default_ins),
            delete=lambda c: delete.get(c, default_delete),
            substitute=_sub,
            transpose=_trans,
            min_insert=min([default_ins, *ins.values()]),
            min_delete=min([default_delete, *delete.values()]),
        )
