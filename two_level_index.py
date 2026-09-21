"""两层时间聚合索引（细窗口 + 粗窗口），支持写入、更正与撤回。

设计要点：
- 事件时间为整数，桶编号一律用向下取整（floor division），负时间同样适用。
- 细层按 fine_width 分桶保存聚合值，粗层按 coarse_width 分桶保存聚合值，
  原始事件本身也保留一份（用于残窗回读与撤回）。
- 范围查询 [lo, hi)：完整覆盖的粗桶读粗聚合；否则下钻到细桶，
  完整覆盖的细桶读细聚合；两端残窗回扫原始事件。桶互不重叠，
  因此同一条事件只贡献一次。
- add/update/retract 在同一把锁内一次性维护两层聚合与原始记录，
  查询也持锁执行，读不到"一层新一层旧"的中间状态。
"""

from __future__ import annotations

import threading
from dataclasses import dataclass


@dataclass(frozen=True)
class QueryResult:
    """查询结果：聚合总和 + 读取统计。"""

    total: float          # [lo, hi) 内事件值之和
    aggregate_nodes: int  # 读取的聚合节点数（粗/细桶聚合各算一个）
    edge_events: int      # 两端残窗回扫到的原始事件数


class TwoLevelTimeIndex:
    def __init__(self, fine_width: int = 5, coarse_width: int = 20) -> None:
        if fine_width <= 0 or coarse_width <= 0:
            raise ValueError("窗口宽度必须为正整数")
        if coarse_width % fine_width != 0:
            raise ValueError("粗窗宽度必须是细窗宽度的整数倍")
        self.fine_width = fine_width
        self.coarse_width = coarse_width
        self._events: dict[int, tuple[int, float]] = {}   # id -> (time, value)
        self._fine: dict[int, float] = {}                 # 细桶号 -> 聚合值
        self._coarse: dict[int, float] = {}               # 粗桶号 -> 聚合值
        self._fine_members: dict[int, set[int]] = {}      # 细桶号 -> 事件 id 集合
        self._lock = threading.Lock()

    # ---- 写入路径：add / update / retract 都整体持锁，两层同步维护 ----

    def add(self, event_id: int, time: int, value: float) -> None:
        with self._lock:
            if event_id in self._events:
                raise KeyError(f"事件 {event_id} 已存在")
            self._insert(event_id, time, value)

    def update(self, event_id: int, time: int, value: float) -> None:
        """更正事件（可改时间与值），两层聚合与原始记录一起修订。"""
        with self._lock:
            if event_id not in self._events:
                raise KeyError(f"事件 {event_id} 不存在")
            self._remove(event_id)
            self._insert(event_id, time, value)

    def retract(self, event_id: int) -> None:
        """撤回事件，两层聚合与原始记录一起撤销。"""
        with self._lock:
            if event_id not in self._events:
                raise KeyError(f"事件 {event_id} 不存在")
            self._remove(event_id)

    def _insert(self, event_id: int, time: int, value: float) -> None:
        self._events[event_id] = (time, value)
        fine_bucket = time // self.fine_width
        coarse_bucket = time // self.coarse_width
        self._fine[fine_bucket] = self._fine.get(fine_bucket, 0) + value
        self._coarse[coarse_bucket] = self._coarse.get(coarse_bucket, 0) + value
        self._fine_members.setdefault(fine_bucket, set()).add(event_id)

    def _remove(self, event_id: int) -> None:
        time, value = self._events.pop(event_id)
        fine_bucket = time // self.fine_width
        coarse_bucket = time // self.coarse_width
        fine_sum = self._fine[fine_bucket] - value
        if fine_sum == 0:
            del self._fine[fine_bucket]
        else:
            self._fine[fine_bucket] = fine_sum
        coarse_sum = self._coarse[coarse_bucket] - value
        if coarse_sum == 0:
            del self._coarse[coarse_bucket]
        else:
            self._coarse[coarse_bucket] = coarse_sum
        members = self._fine_members[fine_bucket]
        members.discard(event_id)
        if not members:
            del self._fine_members[fine_bucket]

    # ---- 查询路径：内部整窗用聚合，两端残窗回扫原始事件 ----

    def query(self, lo: int, hi: int) -> QueryResult:
        with self._lock:
            if lo >= hi:
                return QueryResult(0, 0, 0)
            total = 0
            nodes = 0
            edge = 0
            first_coarse = lo // self.coarse_width
            last_coarse = (hi - 1) // self.coarse_width
            for coarse_bucket in range(first_coarse, last_coarse + 1):
                coarse_lo = coarse_bucket * self.coarse_width
                coarse_hi = coarse_lo + self.coarse_width
                if coarse_lo >= lo and coarse_hi <= hi:
                    # 完整粗窗：直接读粗聚合（空桶不计入读取读取节点）
                    value = self._coarse.get(coarse_bucket)
                    if value is not None:
                        total += value
                        nodes += 1
                    continue
                # 残粗窗：下钻细层
                seg_lo = max(lo, coarse_lo)
                seg_hi = min(hi, coarse_hi)
                first_fine = seg_lo // self.fine_width
                last_fine = (seg_hi - 1) // self.fine_width
                for fine_bucket in range(first_fine, last_fine + 1):
                    fine_lo = fine_bucket * self.fine_width
                    fine_hi = fine_lo + self.fine_width
                    if fine_lo >= lo and fine_hi <= hi:
                        # 完整细窗：读细聚合（空桶不计入读取节点）
                        value = self._fine.get(fine_bucket)
                        if value is not None:
                            total += value
                            nodes += 1
                    else:
                        # 边缘残窗：回扫该细桶内的原始事件
                        for event_id in self._fine_members.get(fine_bucket, ()):
                            time, value = self._events[event_id]
                            if lo <= time < hi:
                                total += value
                                edge += 1
            return QueryResult(total, nodes, edge)

    def __len__(self) -> int:
        return len(self._events)


def _demo() -> None:
    """跨很多完整桶的查询示例：报告聚合节点数与边缘原始事件数。"""
    index = TwoLevelTimeIndex(fine_width=5, coarse_width=20)
    for t in range(1000):
        index.add(event_id=t, time=t, value=t)
    result = index.query(7, 993)
    print(
        f"查询 [7, 993)：总和={result.total}，"
        f"读取聚合节点={result.aggregate_nodes} 个，"
        f"边缘原始事件={result.edge_events} 条"
    )


if __name__ == "__main__":
    _demo()
