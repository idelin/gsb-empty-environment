"""执行引擎：对公共子计划做物化/重算决策，并向各消费者提供行迭代器。

用法：``run_shared(node, consumers)`` 返回 ``(decision, iterators)``，
每个消费者拿到一个独立迭代器；决策依据可通过 ``decision.rationale()``
获取。物化失败时异常向上抛出，存储中不会留下任何可见条目。
"""

from .costing import MAT_SPILL, RECOMPUTE, CostModel, decide
from .store import MaterializedStore


class Engine:
    def __init__(self, memory_budget=1 << 20, model=None, store=None):
        self.model = model or CostModel()
        self.memory_budget = memory_budget
        self.store = store or MaterializedStore()
        self.decisions = []

    def run_shared(self, node, consumers):
        decision = decide(node, consumers, self.memory_budget, self.model)
        self.decisions.append(decision)
        if decision.strategy == RECOMPUTE:
            return decision, [node.iter_rows() for _ in range(consumers)]
        signature = node.signature()
        self.store.put(signature, node.iter_rows(),
                       spill=(decision.strategy == MAT_SPILL))
        return decision, [self.store.get(signature)
                          for _ in range(consumers)]
