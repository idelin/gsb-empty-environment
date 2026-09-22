"""核心基础设施：预算、终止原因、执行上下文、临时资源注册表。

预算模型（同一查询的所有算子共享同一个 Budget 实例）：
- 行数预算 max_rows：每次“行操作”计 1（从表或子算子消费一行）。
- 内存预算 max_memory：算子为内部缓冲区显式 mem_alloc / mem_free，
  当前占用超限即中断；同时记录峰值 peak_memory。
- 步骤预算 max_steps：每次行处理或连接探测计 1。

预算是硬上限：越界的那次操作会被正常记账，随后抛出 BudgetExhausted，
因此部分统计不会漏记。取消（cancel）后下一个记账点抛出 QueryCancelled。
"""
from __future__ import annotations

import os
import tempfile
from enum import Enum, auto


class TerminationReason(Enum):
    """查询结束原因。EXHAUSTED 与 LIMIT 属于正常结束（complete=True）。"""

    EXHAUSTED = auto()      # 输入自然耗尽（正常结束）
    LIMIT = auto()          # LIMIT 导致的正常提前结束
    BUDGET_ROWS = auto()    # 行数预算中断
    BUDGET_MEMORY = auto()  # 内存预算中断
    BUDGET_STEPS = auto()   # 步骤预算中断
    CANCELLED = auto()      # 外部取消


class BudgetExhausted(Exception):
    """预算耗尽。reason 指明是哪一类预算。"""

    def __init__(self, reason: TerminationReason):
        super().__init__(reason.name)
        self.reason = reason


class QueryCancelled(Exception):
    """查询被取消。"""


class Budget:
    """行数 / 内存 / 步骤三类预算，被整个算子树（含重复执行的子计划）共享。"""

    def __init__(
        self,
        max_rows: int | None = None,
        max_memory: int | None = None,
        max_steps: int | None = None,
        cancel_after_steps: int | None = None,
    ):
        self.max_rows = max_rows
        self.max_memory = max_memory
        self.max_steps = max_steps
        # 确定性取消点：用于演示与测试“执行到一半被取消”。
        self.cancel_after_steps = cancel_after_steps
        self.used_rows = 0
        self.used_memory = 0      # 当前占用（申请/释放）
        self.peak_memory = 0      # 峰值占用
        self.used_steps = 0
        self.cancelled = False

    def cancel(self) -> None:
        self.cancelled = True

    def check_cancelled(self) -> None:
        if self.cancelled:
            raise QueryCancelled()

    def charge_row(self, n: int = 1) -> None:
        self.check_cancelled()
        self.used_rows += n
        if self.max_rows is not None and self.used_rows > self.max_rows:
            raise BudgetExhausted(TerminationReason.BUDGET_ROWS)

    def charge_step(self, n: int = 1) -> None:
        self.used_steps += n
        if self.cancel_after_steps is not None and self.used_steps >= self.cancel_after_steps:
            self.cancel()
        self.check_cancelled()
        if self.max_steps is not None and self.used_steps > self.max_steps:
            raise BudgetExhausted(TerminationReason.BUDGET_STEPS)

    def mem_alloc(self, n: int) -> None:
        self.check_cancelled()
        if self.max_memory is not None and self.used_memory + n > self.max_memory:
            raise BudgetExhausted(TerminationReason.BUDGET_MEMORY)
        self.used_memory += n
        if self.used_memory > self.peak_memory:
            self.peak_memory = self.used_memory

    def mem_free(self, n: int) -> None:
        self.used_memory = max(0, self.used_memory - n)


class ResourceRegistry:
    """跟踪查询执行期间创建的临时资源（如排序落盘文件），负责统一释放。"""

    def __init__(self) -> None:
        self._temp_files: list[str] = []
        self.temp_files_created = 0

    def new_temp_file(self) -> str:
        fd, path = tempfile.mkstemp(prefix="releng_", suffix=".run")
        os.close(fd)
        self._temp_files.append(path)
        self.temp_files_created += 1
        return path

    def release_all(self) -> None:
        for path in self._temp_files:
            try:
                os.unlink(path)
            except FileNotFoundError:
                pass
        self._temp_files.clear()

    def active_temp_files(self) -> list[str]:
        return [p for p in self._temp_files if os.path.exists(p)]


class ExecutionContext:
    """一次查询执行的共享状态：预算、资源注册表、终止原因、部分聚合标记。"""

    def __init__(self, budget: Budget, registry: ResourceRegistry):
        self.budget = budget
        self.registry = registry
        self.termination = TerminationReason.EXHAUSTED
        # 聚合算子未能消费全部输入时置 True：此时的聚合值是部分结果，
        # 绝不能冒充最终值。
        self.partial_aggregates = False


def estimate_value_size(value) -> int:
    """确定性的行内存估算（字节），仅用于内存预算记账。"""
    if value is None:
        return 8
    if isinstance(value, bool):
        return 8
    if isinstance(value, int):
        return 28
    if isinstance(value, float):
        return 24
    if isinstance(value, str):
        return 49 + len(value)
    return 64


def estimate_row_size(row: dict) -> int:
    return 64 + sum(40 + len(str(k)) + estimate_value_size(v) for k, v in row.items())


# ---- 三值逻辑比较器：任一操作数为 NULL 时结果为 None（UNKNOWN） ----

def sql_eq(a, b):
    if a is None or b is None:
        return None
    return a == b


def sql_lt(a, b):
    if a is None or b is None:
        return None
    return a < b


def sql_gt(a, b):
    if a is None or b is None:
        return None
    return a > b


def sql_le(a, b):
    if a is None or b is None:
        return None
    return a <= b


def sql_ge(a, b):
    if a is None or b is None:
        return None
    return a >= b
