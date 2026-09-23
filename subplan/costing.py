"""物化 / 重算决策的代价模型。

对同一个公共子计划，比较三种方案的总代价：

- 重算（recompute）           ：consumers × 计算代价
- 内存物化（materialize_memory）：计算 + 内存写入 + consumers × 内存读取
                                + 内存占用代价（要求估计占用 ≤ 内存预算）
- 溢写物化（materialize_spill）：计算 + 落盘写入 + consumers × 磁盘读取
                                （占用超预算时的兜底方案）

规则：仅 1 个消费者时不物化（一次使用不无故物化）。
"""

from dataclasses import dataclass

RECOMPUTE = 'recompute'
MAT_MEMORY = 'materialize_memory'
MAT_SPILL = 'materialize_spill'

_STRATEGY_ZH = {
    RECOMPUTE: '重算',
    MAT_MEMORY: '内存物化',
    MAT_SPILL: '溢写物化',
}


@dataclass(frozen=True)
class CostModel:
    scan_row: float = 1.0
    filter_row: float = 1.0
    project_row: float = 0.5
    join_row: float = 2.0
    mem_write_byte: float = 0.02
    mem_read_byte: float = 0.01
    mem_hold_byte: float = 0.005
    spill_write_byte: float = 0.2
    spill_read_byte: float = 0.05


@dataclass
class Decision:
    strategy: str
    consumers: int
    est_rows: int
    footprint_bytes: int
    compute_once: float
    recompute_cost: float
    memory_cost: float | None
    spill_cost: float
    fits_in_memory: bool
    reason: str

    def rationale(self):
        return self.reason


def decide(node, consumers, memory_budget, model=None):
    """为公共子计划 ``node`` 选择执行方案，并给出人类可读的依据。"""
    model = model or CostModel()
    rows = node.estimate_rows()
    footprint = rows * node.row_width()
    compute_once = node.compute_cost(model)

    recompute_total = consumers * compute_once
    spill_total = (compute_once
                   + footprint * model.spill_write_byte
                   + consumers * footprint * model.spill_read_byte)
    fits = footprint <= memory_budget
    memory_total = None
    if fits:
        memory_total = (compute_once
                        + footprint * model.mem_write_byte
                        + consumers * footprint * model.mem_read_byte
                        + footprint * model.mem_hold_byte)

    lines = [
        f"公共子计划估计 {rows} 行 / 约 {footprint} 字节，"
        f"消费者 {consumers} 个，单次计算代价 {compute_once:.1f}。",
        f"  重算      ：{consumers} × {compute_once:.1f}"
        f" = {recompute_total:.1f}",
    ]
    if memory_total is not None:
        lines.append(
            f"  内存物化  ：{compute_once:.1f} 计算"
            f" + {footprint * model.mem_write_byte:.1f} 写入"
            f" + {consumers} × {footprint * model.mem_read_byte:.1f} 读取"
            f" + {footprint * model.mem_hold_byte:.1f} 内存占用"
            f" = {memory_total:.1f}"
            f"（占用 {footprint}B ≤ 预算 {memory_budget}B）")
    else:
        lines.append(
            f"  内存物化  ：不可行"
            f"（占用 {footprint}B > 预算 {memory_budget}B，需溢写）")
    lines.append(
        f"  溢写物化  ：{compute_once:.1f} 计算"
        f" + {footprint * model.spill_write_byte:.1f} 写入"
        f" + {consumers} × {footprint * model.spill_read_byte:.1f} 读取"
        f" = {spill_total:.1f}")

    if consumers <= 1:
        strategy = RECOMPUTE
        lines.append("→ 选择重算：仅 1 次读取，物化没有复用收益，不无故物化。")
    else:
        candidates = [(recompute_total, RECOMPUTE),
                      (spill_total, MAT_SPILL)]
        if memory_total is not None:
            candidates.append((memory_total, MAT_MEMORY))
        best_cost, strategy = min(candidates, key=lambda item: item[0])
        lines.append(
            f"→ 选择{_STRATEGY_ZH[strategy]}：总代价最低（{best_cost:.1f}）。")

    return Decision(
        strategy=strategy,
        consumers=consumers,
        est_rows=rows,
        footprint_bytes=footprint,
        compute_once=compute_once,
        recompute_cost=recompute_total,
        memory_cost=memory_total,
        spill_cost=spill_total,
        fits_in_memory=fits,
        reason="\n".join(lines),
    )
