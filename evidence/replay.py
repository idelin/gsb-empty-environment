"""证据重放：由事件日志确定性重建仓库并重放求解。"""
from __future__ import annotations

from .model import EvidenceStore, Event, FactVersion, MutexRuleVersion


def apply_event(store: EvidenceStore, event: Event) -> None:
    if event.kind == "declare-fact":
        store.add_fact_version(FactVersion(**event.payload))
    elif event.kind == "declare-rule":
        payload = dict(event.payload)
        payload["sources"] = frozenset(payload["sources"])
        store.add_rule_version(MutexRuleVersion(**payload))
    else:
        raise ValueError(f"未知事件类型: {event.kind}")


def replay(events) -> EvidenceStore:
    store = EvidenceStore(record_events=False)
    for event in events:
        apply_event(store, event)
    return store
