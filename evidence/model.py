"""核心数据模型：多版本事实、来源互斥规则、事件溯源存储。

所有对象都带稳定 ID 与版本号。可见性采用双时态语义：
在观察时刻 T，只有 recorded_at <= T 的版本可见，每个 ID 取最新可见版本。
观察时刻之后录入的证据不得参与历史解释。
"""
from __future__ import annotations

from dataclasses import dataclass


@dataclass(frozen=True)
class FactVersion:
    """一个事实的一个版本。业务有效区间为半开区间 [valid_from, valid_to)。"""

    fact_id: str
    version: int
    source: str
    recorded_at: float
    valid_from: float
    valid_to: float

    def __post_init__(self) -> None:
        if not self.valid_from < self.valid_to:
            raise ValueError(
                f"{self.ref}: 业务区间为空 (valid_from={self.valid_from} >= valid_to={self.valid_to})"
            )

    @property
    def ref(self) -> str:
        return f"{self.fact_id}@v{self.version}"

    def to_payload(self) -> dict:
        return {
            "fact_id": self.fact_id,
            "version": self.version,
            "source": self.source,
            "recorded_at": self.recorded_at,
            "valid_from": self.valid_from,
            "valid_to": self.valid_to,
        }


@dataclass(frozen=True)
class MutexRuleVersion:
    """来源互斥规则的一个版本：sources 中任意两个不同来源的事实不得同时被选中。"""

    rule_id: str
    version: int
    sources: frozenset
    recorded_at: float

    def __post_init__(self) -> None:
        if len(self.sources) < 2:
            raise ValueError(f"{self.ref}: 互斥规则至少需要 2 个来源")

    @property
    def ref(self) -> str:
        return f"{self.rule_id}@v{self.version}"

    def to_payload(self) -> dict:
        return {
            "rule_id": self.rule_id,
            "version": self.version,
            "sources": sorted(self.sources),
            "recorded_at": self.recorded_at,
        }


@dataclass(frozen=True)
class Event:
    """溯源事件：稳定 event_id + 版本，保证证据可重放。"""

    event_id: str
    version: int
    kind: str  # "declare-fact" | "declare-rule"
    payload: dict


def _pick_visible(versions, observation: float):
    candidates = [v for v in versions if v.recorded_at <= observation]
    if not candidates:
        return None
    return max(candidates, key=lambda v: (v.recorded_at, v.version))


class EvidenceStore:
    """事实/规则版本仓库，所有变更记录为事件，支持确定性重放。"""

    def __init__(self, record_events: bool = True) -> None:
        self._facts: dict[str, list[FactVersion]] = {}
        self._rules: dict[str, list[MutexRuleVersion]] = {}
        self.events: list[Event] = []
        self._record = record_events
        self._seq = 0

    def _log(self, kind: str, payload: dict) -> None:
        if not self._record:
            return
        self._seq += 1
        self.events.append(Event(f"evt-{self._seq:04d}", 1, kind, payload))

    def add_fact_version(self, fv: FactVersion) -> None:
        versions = self._facts.setdefault(fv.fact_id, [])
        if any(v.version == fv.version for v in versions):
            raise ValueError(f"重复的事实版本: {fv.ref}")
        versions.append(fv)
        versions.sort(key=lambda v: (v.recorded_at, v.version))
        self._log("declare-fact", fv.to_payload())

    def add_rule_version(self, rv: MutexRuleVersion) -> None:
        versions = self._rules.setdefault(rv.rule_id, [])
        if any(v.version == rv.version for v in versions):
            raise ValueError(f"重复的规则版本: {rv.ref}")
        versions.append(rv)
        versions.sort(key=lambda v: (v.recorded_at, v.version))
        self._log("declare-rule", rv.to_payload())

    def visible_facts(self, observation: float) -> list[FactVersion]:
        out = []
        for fact_id in sorted(self._facts):
            v = _pick_visible(self._facts[fact_id], observation)
            if v is not None:
                out.append(v)
        return out

    def visible_rules(self, observation: float) -> list[MutexRuleVersion]:
        out = []
        for rule_id in sorted(self._rules):
            v = _pick_visible(self._rules[rule_id], observation)
            if v is not None:
                out.append(v)
        return out
