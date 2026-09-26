"""容量不足时的诊断输出。"""
from __future__ import annotations

from dataclasses import dataclass

from .model import Node, ObjectSpec


@dataclass(frozen=True)
class RackReport:
    """单个机架的容量账目。"""

    rack: str
    total_capacity: int
    available_nodes: tuple[str, ...]
    exhausted_nodes: tuple[str, ...]

    @property
    def selectable(self) -> bool:
        return len(self.available_nodes) > 0


@dataclass(frozen=True)
class CapacityDiagnostics:
    """对象级容量诊断：缺多少、缺在哪个机架。"""

    object_id: str
    required_replicas: int
    racks_total: int
    racks_with_capacity: int
    rack_reports: tuple[RackReport, ...]

    @property
    def deficit(self) -> int:
        """因机架隔离约束而无法满足的副本份数。"""
        return max(0, self.required_replicas - self.racks_with_capacity)

    def render(self) -> str:
        lines = [
            f"对象 {self.object_id} 容量诊断:",
            f"  需求副本数: {self.required_replicas}",
            f"  机架总数: {self.racks_total}, 有可用容量的机架: {self.racks_with_capacity}",
            f"  缺口: {self.deficit} 个副本无法放置（机架隔离要求每副本独占一个机架）",
        ]
        for report in self.rack_reports:
            if report.selectable:
                members = ", ".join(report.available_nodes)
                lines.append(f"  机架 {report.rack}: 可用节点 [{members}] (总容量 {report.total_capacity})")
            else:
                members = ", ".join(report.exhausted_nodes) or "无节点"
                lines.append(f"  机架 {report.rack}: 无可用容量，节点 [{members}] 已满/下线")
        return "\n".join(lines)


def build_diagnostics(nodes: tuple[Node, ...], spec: ObjectSpec) -> CapacityDiagnostics:
    """按机架汇总容量，生成诊断报告。"""
    racks: dict[str, list[Node]] = {}
    for node in nodes:
        racks.setdefault(node.rack, []).append(node)
    reports = []
    for rack in sorted(racks):
        members = racks[rack]
        reports.append(
            RackReport(
                rack=rack,
                total_capacity=sum(m.capacity for m in members),
                available_nodes=tuple(sorted(m.node_id for m in members if m.capacity >= 1)),
                exhausted_nodes=tuple(sorted(m.node_id for m in members if m.capacity == 0)),
            )
        )
    return CapacityDiagnostics(
        object_id=spec.object_id,
        required_replicas=spec.replicas,
        racks_total=len(reports),
        racks_with_capacity=sum(1 for r in reports if r.selectable),
        rack_reports=tuple(reports),
    )
