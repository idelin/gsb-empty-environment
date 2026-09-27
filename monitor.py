"""增量监控器：数据撤回时维护等价类与总体分布、距离与阈值证据。

语义约定：
* 每次增删记录都会同时更新所属等价类与总体分布，并使总体基线版本号 +1。
* 每次评估记录当时的人口版本号；版本不匹配即视为过期（stale），
  因为总体基线变化会使所有类的距离失效，必须重新评估。
* 空等价类（全部撤回）不产生距离：distance 为 None，绝不除零。
* 阈值证据：distance <= threshold 的精确有理数判定及余量 margin。
"""

from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction
from typing import Callable, Dict, Hashable, List, Optional, Tuple

from distances import (
    FlowEdge,
    TvContribution,
    general_transport,
    ordered_transport,
    tv_contributions,
)
from histogram import Histogram

METRICS = ("tv", "ordered", "general")


@dataclass(frozen=True)
class Evaluation:
    class_id: Hashable
    population_version: int
    metric: str
    class_size: int
    population_size: int
    distance: Optional[object]      # None：空类或空总体，距离未定义
    contributions: Optional[tuple]  # TV 逐桶 / 有序逐区间 / 一般模式为 None
    flow: Optional[Tuple[FlowEdge, ...]]
    threshold: Optional[Fraction]
    within_threshold: Optional[bool]
    margin: Optional[Fraction]      # threshold - distance；>= 0 即达标


class DistributionMonitor:
    def __init__(
        self,
        metric: str = "tv",
        threshold=None,
        ordered_values=None,
        cost_matrix: Optional[Dict] = None,
        cost_fn: Optional[Callable] = None,
    ):
        if metric not in METRICS:
            raise ValueError(f"metric must be one of {METRICS}")
        self.metric = metric
        self.threshold = Fraction(threshold) if threshold is not None else None
        self.ordered_values = ordered_values
        self.cost_matrix = cost_matrix
        self.cost_fn = cost_fn
        self.population = Histogram()
        self.population_version = 0
        self._classes: Dict[Hashable, Histogram] = {}
        self._evaluations: Dict[Hashable, Evaluation] = {}

    # ------------------------------------------------------------ 数据变更

    def add(self, class_id: Hashable, value: Hashable, n: int = 1) -> None:
        self._classes.setdefault(class_id, Histogram()).add(value, n)
        self.population.add(value, n)
        self.population_version += 1

    def remove(self, class_id: Hashable, value: Hashable, n: int = 1) -> None:
        """数据撤回：同步更新类与总体，并推进总体基线版本。"""
        if class_id not in self._classes:
            raise KeyError(f"unknown class {class_id!r}")
        self._classes[class_id].remove(value, n)
        self.population.remove(value, n)
        self.population_version += 1

    # ------------------------------------------------------------ 评估

    def class_size(self, class_id: Hashable) -> int:
        hist = self._classes.get(class_id)
        return hist.total if hist else 0

    def evaluate(self, class_id: Hashable) -> Evaluation:
        if class_id not in self._classes:
            raise KeyError(f"unknown class {class_id!r}")
        hist = self._classes[class_id]
        distance = contributions = flow = None
        if not hist.is_empty and not self.population.is_empty:
            if self.metric == "tv":
                contributions = tv_contributions(hist, self.population)
                distance = sum((c.contribution for c in contributions), Fraction(0))
            elif self.metric == "ordered":
                res = ordered_transport(hist, self.population, self.ordered_values)
                distance, contributions, flow = res.distance, res.intervals, res.flow
            else:
                res = general_transport(hist, self.population, self.cost_matrix, self.cost_fn)
                distance, flow = res.distance, res.flow
        within = margin = None
        if distance is not None and self.threshold is not None:
            margin = self.threshold - distance
            within = margin >= 0
        ev = Evaluation(
            class_id=class_id,
            population_version=self.population_version,
            metric=self.metric,
            class_size=hist.total,
            population_size=self.population.total,
            distance=distance,
            contributions=contributions,
            flow=flow,
            threshold=self.threshold,
            within_threshold=within,
            margin=margin,
        )
        self._evaluations[class_id] = ev
        return ev

    def evaluate_all(self) -> Dict[Hashable, Evaluation]:
        return {cid: self.evaluate(cid) for cid in self._classes}

    # ------------------------------------------------------------ 版本与过期

    def stale_classes(self) -> List[Hashable]:
        """已评估但评估所基于的总体版本已过期的类。"""
        return [
            cid
            for cid, ev in self._evaluations.items()
            if ev.population_version != self.population_version
        ]

    def pending_classes(self) -> List[Hashable]:
        """从未评估过的类。"""
        return [cid for cid in self._classes if cid not in self._evaluations]

    def evaluation(self, class_id: Hashable) -> Optional[Evaluation]:
        return self._evaluations.get(class_id)
