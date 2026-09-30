"""联合优化：泛化层向量 + 抑制预算下满足 k 的最低损失方案。

方法
----
- 精确枚举所有泛化层向量（各 QI 层级的笛卡尔积），按泛化损失下界
  升序评估；对每个向量用最少抑制使其可行（见 core.evaluate）。
- 因为 NCP 损失随层级单调不减，按 gen_loss 下界升序枚举时，一旦评估
  中断，第一个未评估向量的 gen_loss 即为所有未评估解的有效下界。
- 预算中断（max_evals 耗尽）时返回：
  ``upper_bound`` = 当前最优可行解损失（上界），
  ``lower_bound`` = 未评估向量的最小可能损失（下界）。
- 只评价声明的指标（NCP 泛化损失 + 抑制惩罚），不宣称等价于完整
  隐私保障，也不宣称绝对匿名。
"""
from __future__ import annotations

import itertools
import math
from dataclasses import dataclass, field

from .core import Evaluation, Record, evaluate, generalized_key, record_loss
from .hierarchy import Hierarchy


@dataclass(frozen=True)
class Evidence:
    """单条记录的归属证据。"""

    rid: str
    subject: str
    action: str  # "kept" | "suppressed"
    class_key: tuple[str, ...] | None
    class_count: int | None  # 按 count_mode 的类计数
    class_size: int | None   # 类中记录条数
    k_satisfied: bool | None


@dataclass(frozen=True)
class Solution:
    levels: tuple[int, ...]
    loss: float
    gen_loss: float
    suppression_loss: float
    suppressed_rids: frozenset[str]
    evidence: tuple[Evidence, ...]


@dataclass
class SearchResult:
    best: Solution | None
    lower_bound: float
    upper_bound: float  # math.inf 表示尚未找到可行解
    optimal: bool       # True 表示搜索完整，best 为精确最优
    evaluated: int
    total_vectors: int
    interrupted: bool


def _all_level_vectors(hierarchies: tuple[Hierarchy, ...]) -> list[tuple[int, ...]]:
    return list(itertools.product(*(range(h.max_level + 1) for h in hierarchies)))


def _gen_loss_lower_bound(
    records: list[Record],
    levels: tuple[int, ...],
    hierarchies: tuple[Hierarchy, ...],
) -> float:
    """层向量的损失下界：假设零抑制时的泛化损失（抑制只会增加损失）。"""
    return sum(record_loss(rec, levels, hierarchies) for rec in records)


def _build_solution(
    records: list[Record],
    ev: Evaluation,
    hierarchies: tuple[Hierarchy, ...],
    k: int,
    count_mode: str,
    suppression_penalty: float,
) -> Solution:
    suppressed = {
        rid for key in ev.deficient_keys for rid in ev.classes[key].member_rids
    }
    evidence = []
    for rec in records:
        key = generalized_key(rec, ev.levels, hierarchies)
        ec = ev.classes[key]
        if rec.rid in suppressed:
            evidence.append(
                Evidence(rec.rid, rec.subject, "suppressed", key, ec.count,
                         ec.size, False)
            )
        else:
            evidence.append(
                Evidence(rec.rid, rec.subject, "kept", key, ec.count,
                         ec.size, ec.count >= k)
            )
    return Solution(
        levels=ev.levels,
        loss=ev.total_loss,
        gen_loss=ev.gen_loss,
        suppression_loss=ev.suppression_loss,
        suppressed_rids=frozenset(suppressed),
        evidence=tuple(evidence),
    )


def optimize(
    records: list[Record],
    hierarchies: tuple[Hierarchy, ...],
    k: int,
    budget: int,
    count_mode: str = "record",
    suppression_penalty: float = 1.0,
    max_evals: int | None = None,
) -> SearchResult:
    """求满足 k-匿名且抑制不超过 budget 的最低损失方案。

    max_evals 为评估预算；耗尽时中断并返回上下界。
    """
    vectors = _all_level_vectors(hierarchies)
    vectors.sort(key=lambda v: _gen_loss_lower_bound(records, v, hierarchies))

    best: Solution | None = None
    evaluated = 0
    interrupted = False
    for vec in vectors:
        if max_evals is not None and evaluated >= max_evals:
            interrupted = True
            break
        lb = _gen_loss_lower_bound(records, vec, hierarchies)
        if best is not None and lb >= best.loss:
            # 单调性剪枝：该向量及之后所有向量都不可能更优
            break
        ev = evaluate(records, vec, hierarchies, k, count_mode, suppression_penalty)
        evaluated += 1
        if ev.feasible(budget) and (best is None or ev.total_loss < best.loss):
            best = _build_solution(
                records, ev, hierarchies, k, count_mode, suppression_penalty
            )

    if not interrupted:
        # 完整枚举或被单调性剪枝终止：结果精确（best 为 None 表示无可行解）
        bound = best.loss if best is not None else math.inf
        return SearchResult(best, bound, bound, True, evaluated,
                            len(vectors), False)

    upper = best.loss if best is not None else math.inf
    if evaluated < len(vectors):
        next_lb = _gen_loss_lower_bound(records, vectors[evaluated], hierarchies)
    else:
        next_lb = math.inf
    lower = min(upper, next_lb)
    return SearchResult(best, lower, upper, False, evaluated, len(vectors),
                        interrupted)
