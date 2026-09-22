"""执行器：驱动算子树，统一处理预算中断 / 取消 / 正常结束，并释放临时资源。"""
from __future__ import annotations

from dataclasses import dataclass

from .core import (
    Budget,
    BudgetExhausted,
    ExecutionContext,
    QueryCancelled,
    ResourceRegistry,
    TerminationReason,
)


@dataclass
class Stats:
    """执行统计（部分结果同样带完整统计，越界的那次操作也已记账）。"""

    rows_processed: int   # 行操作总数（所有算子、含重复子计划）
    steps: int            # 步骤总数
    peak_memory: int      # 内存峰值（字节）
    temp_files_created: int


@dataclass
class QueryResult:
    rows: list
    complete: bool                 # False 表示被预算/取消打断，结果是部分的
    termination: TerminationReason
    partial_aggregates: bool       # True 表示聚合值是中间结果，不能当最终值
    stats: Stats


def execute(plan, budget: Budget | None = None, registry: ResourceRegistry | None = None) -> QueryResult:
    budget = budget if budget is not None else Budget()
    registry = registry if registry is not None else ResourceRegistry()
    ctx = ExecutionContext(budget, registry)
    rows = []
    iterator = plan.execute(ctx)
    try:
        for row in iterator:
            rows.append(row)
    except BudgetExhausted as exc:
        ctx.termination = exc.reason
    except QueryCancelled:
        ctx.termination = TerminationReason.CANCELLED
    finally:
        iterator.close()
        registry.release_all()  # 取消/中断/正常结束都释放临时资源
    complete = ctx.termination in (TerminationReason.EXHAUSTED, TerminationReason.LIMIT)
    return QueryResult(
        rows=rows,
        complete=complete,
        termination=ctx.termination,
        partial_aggregates=ctx.partial_aggregates,
        stats=Stats(
            rows_processed=budget.used_rows,
            steps=budget.used_steps,
            peak_memory=budget.peak_memory,
            temp_files_created=registry.temp_files_created,
        ),
    )
