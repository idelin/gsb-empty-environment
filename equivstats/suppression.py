"""最少抑制记录搜索：删除最少的记录，使所有等价类满足不同数与熵阈值。

说明：
- 删除记录不会增加任何等价类的不同数，因此不同数不达标的类只能整类抑制；
  熵不达标的类可通过删除主导值记录使分布更均衡。
- exact_min_suppression 按删除数从小到大枚举子集，保证找到最少解；
  仅适用于小域（记录数少），超出节点上限返回 None。
- greedy_suppression 为大数据集的启发式，不保证最优；可用 exact 在小域上校验。
- 判定中熵为 UNDECIDED 的类按不达标处理（保守策略）。
"""

from __future__ import annotations

from collections import Counter, defaultdict
from itertools import combinations

from .core import NullPolicy, Record
from .metrics import Verdict, decide_entropy, distinct_count


def _decide(counter, null_policy, base, threshold, precision):
    # 抑制搜索的判定使用固定精度（不升级），UNDECIDED 按不达标处理（保守）。
    _, verdict = decide_entropy(counter, null_policy, base, threshold,
                                start_precision=precision, max_precision=precision)
    return verdict


def _count_by_class(records: list[Record]) -> dict[tuple, Counter]:
    counts: dict[tuple, Counter] = defaultdict(Counter)
    for record in records:
        counts[record.qid][record.sensitive] += 1
    return counts


def failing_classes(records: list[Record], min_distinct: int, min_entropy,
                    null_policy: NullPolicy, base: float,
                    precision: int = 40) -> dict[tuple, dict]:
    failing = {}
    for qid, counter in _count_by_class(records).items():
        distinct = distinct_count(counter, null_policy)
        verdict = _decide(counter, null_policy, base, min_entropy, precision)
        if distinct < min_distinct or verdict is not Verdict.PASS:
            failing[qid] = {"distinct": distinct, "entropy_verdict": verdict}
    return failing


def all_classes_pass(records: list[Record], min_distinct: int, min_entropy,
                     null_policy: NullPolicy, base: float,
                     precision: int = 40) -> bool:
    return not failing_classes(records, min_distinct, min_entropy,
                               null_policy, base, precision)


def exact_min_suppression(records: list[Record], min_distinct: int, min_entropy,
                          null_policy: NullPolicy, base: float,
                          max_nodes: int = 500_000) -> list[int] | None:
    """小域枚举：按子集大小递增枚举，返回最少删除的记录下标；超限返回 None。"""
    n = len(records)
    nodes = 0
    for size in range(0, n + 1):
        for combo in combinations(range(n), size):
            nodes += 1
            if nodes > max_nodes:
                return None
            removed = set(combo)
            remaining = [r for i, r in enumerate(records) if i not in removed]
            if all_classes_pass(remaining, min_distinct, min_entropy, null_policy, base):
                return list(combo)
    return []


def greedy_suppression(records: list[Record], min_distinct: int, min_entropy,
                       null_policy: NullPolicy, base: float,
                       max_steps: int = 10_000) -> list[int]:
    """启发式：不同数失败则整类抑制（选最小失败类）；熵失败则删主导值记录。"""
    remaining = list(enumerate(records))
    removed: list[int] = []
    for _ in range(max_steps):
        current = [record for _, record in remaining]
        failing = failing_classes(current, min_distinct, min_entropy, null_policy, base)
        if not failing:
            return removed
        distinct_failures = {
            qid for qid, info in failing.items() if info["distinct"] < min_distinct
        }
        if distinct_failures:
            target = min(
                distinct_failures,
                key=lambda qid: sum(1 for r in current if r.qid == qid),
            )
            dropped = [i for i, r in remaining if r.qid == target]
            removed.extend(dropped)
            remaining = [(i, r) for i, r in remaining if r.qid != target]
            continue
        counts = _count_by_class(current)
        worst_qid = next(iter(failing))
        modal_value = counts[worst_qid].most_common(1)[0][0]
        for pos, (index, record) in enumerate(remaining):
            if record.qid == worst_qid and record.sensitive == modal_value:
                removed.append(index)
                del remaining[pos]
                break
    raise RuntimeError("greedy suppression did not converge")
