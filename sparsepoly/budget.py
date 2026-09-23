"""计算预算：内存项数额度 + 单次求值步数额度。

- ``max_live_terms``：内存额度，限制乘积累加器的项数膨胀。超出时：
    * ``allow_external=True``  -> 溢写临时运行段，最后做外存 k 路归并；
    * ``allow_external=False`` -> 返回可继续计算的表达式（不截断、不丢项）。
- ``max_steps``：单次 ``evaluate`` 允许的基本乘加步数，耗尽同样返回
  可继续表达式，从而支持任意断点的暂停/恢复。

``peak_live_terms`` 记录整个计算过程中的峰值在内存项数。
"""

from __future__ import annotations


class StepsExhausted(Exception):
    """内部信号：步数额度耗尽，由惰性层捕获并生成续算节点。"""


class Budget:
    def __init__(self, max_live_terms: int = 1_000_000,
                 max_steps: int | None = None,
                 allow_external: bool = True,
                 spill_dir: str | None = None):
        if max_live_terms < 1:
            raise ValueError("max_live_terms 至少为 1")
        self.max_live_terms = max_live_terms
        self.max_steps = max_steps
        self.allow_external = allow_external
        self.spill_dir = spill_dir
        self.steps_used = 0
        self.live_terms = 0
        self.peak_live_terms = 0
        self.spilled_runs = 0

    def account_step(self) -> None:
        """计一个基本乘加步；步数耗尽时抛 StepsExhausted。"""
        if self.max_steps is not None and self.steps_used >= self.max_steps:
            raise StepsExhausted
        self.steps_used += 1

    def track_terms(self, n: int) -> None:
        """更新当前在内存项数并记录峰值。"""
        self.live_terms = n
        if n > self.peak_live_terms:
            self.peak_live_terms = n
