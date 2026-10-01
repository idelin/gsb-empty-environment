"""可暂停的 Fisher-Yates 洗牌（显式随机源，状态可检查点化）。

不变式：frontier 为下一个待处理下标（自 n-1 递减至 1）；
大于 frontier 的位置为已发布位置，不再参与后续交换。
检查点同时保存交换前沿、随机源状态与已发布位置。
"""
from __future__ import annotations

from .prng import randbelow


def state_to_json(state):
    if isinstance(state, tuple):
        return [state_to_json(s) for s in state]
    return state


def state_from_json(obj):
    if isinstance(obj, list):
        return tuple(state_from_json(x) for x in obj)
    return obj


def restore_source(source, ckpt: dict) -> None:
    """从检查点恢复随机源状态、样本量计数与 seed 记录。"""
    source.setstate(state_from_json(ckpt["rng_state"]))
    if hasattr(source, "draws") and ckpt.get("draws") is not None:
        source.draws = ckpt["draws"]
        source.bits = ckpt["bits"]
    if ckpt.get("seed") is not None and hasattr(source, "seed"):
        source.seed = ckpt["seed"]


class ShuffleState:
    """Fisher-Yates 交换前沿状态，可逐步执行、可序列化为检查点。"""

    def __init__(self, items, source, frontier=None):
        self.items = list(items)
        self.source = source
        self.frontier = len(self.items) - 1 if frontier is None else frontier

    @property
    def done(self) -> bool:
        return self.frontier < 1

    @property
    def published(self) -> list:
        """已最终确定（发布）的位置下标。"""
        if self.done:
            return list(range(len(self.items)))
        return list(range(self.frontier + 1, len(self.items)))

    def step(self) -> bool:
        """执行一步交换：j 取自 [0, frontier]，范围正确且无偏。"""
        if self.done:
            return False
        i = self.frontier
        j = randbelow(self.source, i + 1)
        self.items[i], self.items[j] = self.items[j], self.items[i]
        self.frontier = i - 1
        return True

    def run(self) -> list:
        while self.step():
            pass
        return self.items

    def checkpoint(self) -> dict:
        """检查点：交换前沿 + 随机状态 + 已发布位置 + seed/样本量。"""
        return {
            "items": list(self.items),
            "frontier": self.frontier,
            "rng_state": state_to_json(self.source.getstate()),
            "seed": getattr(self.source, "seed", None),
            "draws": getattr(self.source, "draws", None),
            "bits": getattr(self.source, "bits", None),
            "published": self.published,
        }

    @classmethod
    def restore(cls, ckpt: dict, source) -> "ShuffleState":
        restore_source(source, ckpt)
        return cls(ckpt["items"], source, frontier=ckpt["frontier"])
