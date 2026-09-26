"""共享操作预算。

一次 search 调用只创建一个 Budget，并传入整棵查询树的执行过程；
任何子查询都只 charge，不允许重置，保证“多个子查询不能各自重置预算”。
"""

from dataclasses import dataclass


class BudgetExhausted(Exception):
    """预算耗尽时由 Budget.charge 抛出，执行器捕获后返回未完成结果。"""


@dataclass
class Budget:
    max_ops: int
    used: int = 0

    def charge(self, n: int = 1) -> None:
        if n < 0:
            raise ValueError("charge 不能为负")
        self.used += n
        if self.used > self.max_ops:
            raise BudgetExhausted(
                f"预算耗尽: 已用 {self.used} / 上限 {self.max_ops}"
            )

    @property
    def remaining(self) -> int:
        return max(0, self.max_ops - self.used)
