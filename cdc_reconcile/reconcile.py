"""双副本 CDC 对账核心逻辑。

规则：
- 只在共同水位（含）以下比较，水位之上的一方只报"未追平"，不报数据错误；
- 缺事件：水位内一方有而另一方没有的事件，按实体单列；
- 顺序冲突：同一实体水位内事件集合相同，但应用顺序不同；
- 值分歧：水位内同一实体最终值不同；
- 最终值相同但缺事件：单独归类，不算值分歧；
- 共同水位只允许前进，回退抛 CommonWatermarkRollbackError。
"""
from __future__ import annotations

import json
from dataclasses import dataclass, field
from typing import Any, Optional

from .models import ChangeEvent
from .watermark import WatermarkTracker


@dataclass
class ReplicaStream:
    """一个副本已应用的变更流（按应用顺序排列）。"""

    name: str
    events: list[ChangeEvent]

    @property
    def progress(self) -> int:
        return max((e.commit_seq for e in self.events), default=-1)

    def within(self, watermark: int) -> list[ChangeEvent]:
        return [e for e in self.events if e.commit_seq <= watermark]

    def beyond(self, watermark: int) -> list[ChangeEvent]:
        return [e for e in self.events if e.commit_seq > watermark]

    @classmethod
    def from_file(cls, path: str) -> "ReplicaStream":
        with open(path, "r", encoding="utf-8") as fh:
            raw = json.load(fh)
        return cls(
            name=raw.get("replica", path),
            events=[ChangeEvent.from_dict(item) for item in raw["events"]],
        )


@dataclass
class EntitySummary:
    """单个实体在水位内的摘要。"""

    table: str
    pk: str
    final_value: Optional[dict]
    applied_seqs: list[int]  # 按应用顺序


def summarize(events: list[ChangeEvent]) -> dict[tuple[str, str], EntitySummary]:
    summaries: dict[tuple[str, str], EntitySummary] = {}
    for event in events:
        key = event.entity_key
        summary = summaries.get(key)
        if summary is None:
            summary = summaries[key] = EntitySummary(
                table=event.table, pk=event.pk, final_value=None, applied_seqs=[]
            )
        summary.final_value = event.after
        summary.applied_seqs.append(event.commit_seq)
    return summaries


@dataclass
class ReconcileReport:
    common_watermark: int
    lagging: Optional[dict] = None                       # 未追平信息
    missing_events: list[dict] = field(default_factory=list)
    ordering_conflicts: list[dict] = field(default_factory=list)
    value_divergences: list[dict] = field(default_factory=list)
    same_value_missing_events: list[dict] = field(default_factory=list)
    consistent_entities: int = 0

    def to_dict(self) -> dict[str, Any]:
        return {
            "common_watermark": self.common_watermark,
            "lagging": self.lagging,
            "missing_events": self.missing_events,
            "ordering_conflicts": self.ordering_conflicts,
            "value_divergences": self.value_divergences,
            "same_value_missing_events": self.same_value_missing_events,
            "consistent_entities": self.consistent_entities,
        }


def compare(
    stream_a: ReplicaStream,
    stream_b: ReplicaStream,
    tracker: Optional[WatermarkTracker] = None,
) -> ReconcileReport:
    common_watermark = min(stream_a.progress, stream_b.progress)
    if tracker is not None:
        tracker.advance(common_watermark)  # 回退时抛异常

    report = ReconcileReport(common_watermark=common_watermark)

    # 一方落后：只报未追平，水位之上的事件不参与任何数据比较
    if stream_a.progress != stream_b.progress:
        ahead, behind = (stream_a, stream_b) if stream_a.progress > stream_b.progress else (stream_b, stream_a)
        pending = ahead.beyond(common_watermark)
        report.lagging = {
            "lagging_side": behind.name,
            "ahead_side": ahead.name,
            "ahead_progress": ahead.progress,
            "pending_count": len(pending),
            "pending_commit_seqs": [e.commit_seq for e in pending],
        }

    events_a = stream_a.within(common_watermark)
    events_b = stream_b.within(common_watermark)
    summaries_a = summarize(events_a)
    summaries_b = summarize(events_b)

    seqs_a = {e.commit_seq for e in events_a}
    seqs_b = {e.commit_seq for e in events_b}

    for key in sorted(set(summaries_a) | set(summaries_b)):
        sa = summaries_a.get(key)
        sb = summaries_b.get(key)
        entity = {"table": key[0], "pk": key[1]}

        set_a = set(sa.applied_seqs) if sa else set()
        set_b = set(sb.applied_seqs) if sb else set()
        missing_on_b = sorted(set_a - set_b)
        missing_on_a = sorted(set_b - set_a)

        if missing_on_a or missing_on_b:
            entry = dict(entity)
            if missing_on_a:
                entry[f"missing_on_{stream_a.name}"] = missing_on_a
            if missing_on_b:
                entry[f"missing_on_{stream_b.name}"] = missing_on_b
            report.missing_events.append(entry)

        if sa and sb:
            if not missing_on_a and not missing_on_b and sa.applied_seqs != sb.applied_seqs:
                report.ordering_conflicts.append(
                    dict(
                        entity,
                        **{
                            f"order_{stream_a.name}": sa.applied_seqs,
                            f"order_{stream_b.name}": sb.applied_seqs,
                        },
                    )
                )

            if sa.final_value != sb.final_value:
                report.value_divergences.append(
                    dict(
                        entity,
                        **{
                            f"value_{stream_a.name}": sa.final_value,
                            f"value_{stream_b.name}": sb.final_value,
                        },
                    )
                )
            elif missing_on_a or missing_on_b:
                # 最终值相同但事件缺失：单列，不算值分歧
                entry = dict(entity, final_value=sa.final_value)
                if missing_on_a:
                    entry[f"missing_on_{stream_a.name}"] = missing_on_a
                if missing_on_b:
                    entry[f"missing_on_{stream_b.name}"] = missing_on_b
                report.same_value_missing_events.append(entry)
            elif not missing_on_a and not missing_on_b and sa.applied_seqs == sb.applied_seqs:
                report.consistent_entities += 1
        else:
            # 实体只存在于一侧（水位内），本质是缺事件，已记入 missing_events
            pass

    return report
