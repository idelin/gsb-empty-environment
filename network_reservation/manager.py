"""容量预留管理器。

核心语义：
- 固定需求列表按 (优先级降序, 需求ID升序) 依次预留。
- 容量按时间区间核算：仅统计与需求区间 [start, end) 重叠的预留。
- 边故障时仅重路由经过该边的需求；重路由失败则回滚到原预留。
- 替代路径逐边分配，中途失败时释放已占用的边（原子性）。
- 边恢复幂等：未故障的边恢复是空操作，不会重复增加可用容量。
"""

from __future__ import annotations

from collections import deque
from collections.abc import Iterable

from .model import Demand, Graph, Interval, Reservation


class ReservationManager:
    def __init__(self, graph: Graph) -> None:
        self.graph = graph
        self._reservations: dict[str, Reservation] = {}
        # edge_id -> [(demand_id, interval, bandwidth), ...]
        self._edge_usage: dict[str, list[tuple[str, Interval, float]]] = {
            edge_id: [] for edge_id in graph.edges
        }

    # ---------- 容量核算 ----------

    def used_capacity(self, edge_id: str, interval: Interval) -> float:
        return sum(
            bandwidth
            for _, iv, bandwidth in self._edge_usage[edge_id]
            if iv.overlaps(interval)
        )

    def available_capacity(self, edge_id: str, interval: Interval) -> float:
        return self.graph.edges[edge_id].capacity - self.used_capacity(edge_id, interval)

    # ---------- 占用 / 释放（原子分配） ----------

    def _allocate(self, demand: Demand, path: list[str]) -> bool:
        """逐边占用；中途容量不足时释放已占用的边并返回 False。"""
        allocated: list[str] = []
        for edge_id in path:
            if self.available_capacity(edge_id, demand.interval) < demand.bandwidth:
                for done in allocated:
                    self._release_edge(done, demand.demand_id)
                return False
            self._edge_usage[edge_id].append(
                (demand.demand_id, demand.interval, demand.bandwidth)
            )
            allocated.append(edge_id)
        return True

    def _release_edge(self, edge_id: str, demand_id: str) -> None:
        self._edge_usage[edge_id] = [
            entry for entry in self._edge_usage[edge_id] if entry[0] != demand_id
        ]

    def _release(self, reservation: Reservation) -> None:
        for edge_id in reservation.path:
            self._release_edge(edge_id, reservation.demand.demand_id)

    # ---------- 寻路 ----------

    def find_path(
        self,
        source: str,
        target: str,
        bandwidth: float,
        interval: Interval,
        forbidden_edges: Iterable[str] = (),
    ) -> list[str] | None:
        """BFS 找一条所有边剩余容量都足够的路径；跳过故障边与禁用边。"""
        if source not in self.graph.vertices or target not in self.graph.vertices:
            raise KeyError(f"源或目的顶点不存在：{source!r}, {target!r}")
        if source == target:
            return []
        forbidden = set(forbidden_edges)
        visited = {source}
        queue: deque[tuple[str, list[str]]] = deque([(source, [])])
        while queue:
            node, path = queue.popleft()
            for edge_id, nxt in self.graph.neighbors(node):
                edge = self.graph.edges[edge_id]
                if edge.failed or edge_id in forbidden:
                    continue
                if self.available_capacity(edge_id, interval) < bandwidth:
                    continue
                if nxt in visited:
                    continue
                new_path = path + [edge_id]
                if nxt == target:
                    return new_path
                visited.add(nxt)
                queue.append((nxt, new_path))
        return None

    # ---------- 预留 ----------

    def reserve(self, demand: Demand) -> bool:
        if demand.demand_id in self._reservations:
            raise ValueError(f"需求 ID 重复：{demand.demand_id!r}")
        path = self.find_path(
            demand.source, demand.target, demand.bandwidth, demand.interval
        )
        if path is None:
            return False
        if not self._allocate(demand, path):
            return False
        self._reservations[demand.demand_id] = Reservation(demand, tuple(path))
        return True

    def reserve_all(self, demands: Iterable[Demand]) -> dict[str, bool]:
        """按 (优先级降序, 需求ID升序) 顺序预留，返回 需求ID -> 是否成功。"""
        ordered = sorted(demands, key=lambda d: (-d.priority, d.demand_id))
        return {d.demand_id: self.reserve(d) for d in ordered}

    def get_reservation(self, demand_id: str) -> Reservation | None:
        return self._reservations.get(demand_id)

    # ---------- 故障与恢复 ----------

    def fail_edge(self, edge_id: str) -> dict[str, list[str]]:
        """故障一条边：仅重路由受影响需求；失败者回滚到原预留。

        返回 {"rerouted": [...], "rolled_back": [...]}。
        """
        edge = self.graph.edges[edge_id]
        result: dict[str, list[str]] = {"rerouted": [], "rolled_back": []}
        if edge.failed:
            return result
        edge.failed = True
        affected = sorted(
            (r for r in self._reservations.values() if edge_id in r.path),
            key=lambda r: (-r.demand.priority, r.demand.demand_id),
        )
        for reservation in affected:
            demand = reservation.demand
            old_path = reservation.path
            self._release(reservation)
            new_path = self.find_path(
                demand.source,
                demand.target,
                demand.bandwidth,
                demand.interval,
                forbidden_edges={edge_id},
            )
            if new_path is not None and self._allocate(demand, new_path):
                reservation.path = tuple(new_path)
                result["rerouted"].append(demand.demand_id)
            else:
                # 回滚：恢复原预留（容量状态与释放前一致，必然可恢复）
                if not self._allocate(demand, list(old_path)):  # pragma: no cover
                    raise RuntimeError(f"回滚失败：需求 {demand.demand_id!r}")
                reservation.path = old_path
                result["rolled_back"].append(demand.demand_id)
        return result

    def recover_edge(self, edge_id: str) -> bool:
        """恢复故障边。幂等：未故障时为空操作，返回 False。"""
        edge = self.graph.edges[edge_id]
        if not edge.failed:
            return False
        edge.failed = False
        return True
