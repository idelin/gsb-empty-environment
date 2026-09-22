"""Notification layer: versioned config, reconciliation, audit trail.

``AlertSystem.recompute()`` re-derives the truth timeline from the current
rule/suppression versions and diffs the resulting desired notifications
against the alert registry.  Differences become outbox events:

* OPEN     - a new alert (optionally superseding a corrected one)
* RESOLVED - an open alert recovered
* REVOKED  - a previously sent notification was wrong; references the
             original alert id and the revision that caused the correction

The audit log is append-only: a revoked alert never disappears, it gains a
REVOKED record.
"""

from __future__ import annotations

import math
from dataclasses import dataclass, field

from .engine import (
    compute_states,
    desired_notifications,
    episodes_from_states,
    state_at,
)
from .models import (
    DesiredNotification,
    FaultState,
    NotificationKind,
    RuleVersion,
    Sample,
    SuppressionWindow,
)
from .outbox import Outbox, OutboxEntry


@dataclass
class AlertRecord:
    """Notification-layer state of one alert (what the world was told)."""

    alert_id: str
    rule_id: str
    fire_start: float
    resolve_time: float | None
    revision_opened: int
    open_notified: bool = False
    resolve_notified: bool = False
    revoked: bool = False
    superseded_by: str | None = None
    supersedes: str | None = None


@dataclass
class AuditRecord:
    revision: int
    alert_id: str
    kind: NotificationKind
    payload: dict = field(default_factory=dict)


def _overlap(a_start: float, a_end: float | None, b_start: float, b_end: float | None) -> bool:
    a1 = math.inf if a_end is None else a_end
    b1 = math.inf if b_end is None else b_end
    return a_start < b1 and b_start < a1


class AlertSystem:
    def __init__(self, outbox: Outbox | None = None):
        self.samples: list[Sample] = []
        self._seq = 0
        self._rule_versions: dict[str, list[RuleVersion]] = {}
        self._rule_current: dict[str, int] = {}
        self._sup_versions: dict[str, list[SuppressionWindow]] = {}
        self._sup_current: dict[str, int] = {}
        self.alerts: dict[str, AlertRecord] = {}
        self._alert_seq = 0
        self.revision = 0
        self.outbox = outbox or Outbox()
        self.audit: list[AuditRecord] = []

    # -- data & versioned config ----------------------------------------
    def ingest(self, event_time: float, value: float) -> Sample:
        self._seq += 1
        sample = Sample(event_time, value, self._seq)
        self.samples.append(sample)
        return sample

    def set_rule(self, rule: RuleVersion) -> None:
        versions = self._rule_versions.setdefault(rule.rule_id, [])
        if any(v.version == rule.version for v in versions):
            raise ValueError(f"rule {rule.rule_id} version {rule.version} already exists")
        versions.append(rule)
        self._rule_current[rule.rule_id] = rule.version

    def rollback_rule(self, rule_id: str, version: int) -> None:
        self._require_version(self._rule_versions, rule_id, version)
        self._rule_current[rule_id] = version

    def set_suppression(self, window: SuppressionWindow) -> None:
        versions = self._sup_versions.setdefault(window.sup_id, [])
        if any(v.version == window.version for v in versions):
            raise ValueError(f"suppression {window.sup_id} v{window.version} already exists")
        versions.append(window)
        self._sup_current[window.sup_id] = window.version

    def rollback_suppression(self, sup_id: str, version: int) -> None:
        self._require_version(self._sup_versions, sup_id, version)
        self._sup_current[sup_id] = version

    @staticmethod
    def _require_version(store: dict, key: str, version: int) -> None:
        if not any(v.version == version for v in store.get(key, [])):
            raise KeyError(f"{key} has no version {version}")

    def current_rules(self) -> list[RuleVersion]:
        return [
            next(v for v in versions if v.version == self._rule_current[rid])
            for rid, versions in sorted(self._rule_versions.items())
        ]

    def current_suppressions(self) -> list[SuppressionWindow]:
        return [
            next(v for v in versions if v.version == self._sup_current[sid])
            for sid, versions in sorted(self._sup_versions.items())
        ]

    # -- truth layer view -------------------------------------------------
    def state_at(self, rule_id: str, t: float) -> FaultState:
        rule = next(r for r in self.current_rules() if r.rule_id == rule_id)
        segs = compute_states(self.samples, rule)
        return state_at(segs, self.current_suppressions(), rule_id, t)

    # -- reconciliation ---------------------------------------------------
    def recompute(self) -> list[OutboxEntry]:
        """Recompute truth from current versions; emit correction events."""
        self.revision += 1
        horizon = max((s.event_time for s in self.samples), default=0.0)
        windows = self.current_suppressions()
        emitted: list[OutboxEntry] = []
        for rule in self.current_rules():
            segs = compute_states(self.samples, rule)
            episodes = episodes_from_states(segs, rule.rule_id)
            desired = desired_notifications(episodes, windows, horizon)
            emitted.extend(self._reconcile(rule.rule_id, desired))
        return emitted

    def _reconcile(
        self, rule_id: str, desired: list[DesiredNotification]
    ) -> list[OutboxEntry]:
        emitted: list[OutboxEntry] = []
        candidates = sorted(
            (a for a in self.alerts.values() if a.rule_id == rule_id and not a.revoked),
            key=lambda a: a.fire_start,
        )
        matched: dict[DesiredNotification, AlertRecord] = {}
        unmatched_alerts = {a.alert_id: a for a in candidates}

        for d in sorted(desired, key=lambda d: d.fire_start):
            for a in candidates:
                if a.alert_id not in unmatched_alerts:
                    continue
                if _overlap(a.fire_start, a.resolve_time, d.fire_start, d.resolve_time):
                    matched[d] = a
                    del unmatched_alerts[a.alert_id]
                    break

        for d, a in matched.items():
            if a.fire_start != d.fire_start:
                # Identity of the episode changed: revoke + replace.
                emitted.append(self._emit(a.alert_id, NotificationKind.REVOKED,
                                          reason="corrected", fire_start=a.fire_start))
                a.revoked = True
                new = self._new_alert(d, supersedes=a.alert_id)
                a.superseded_by = new.alert_id
                emitted.append(self._emit_open(new, d))
                if d.resolve_time is not None:
                    emitted.append(self._emit_resolved(new, d))
            elif d.resolve_time is not None and (
                not a.resolve_notified or a.resolve_time != d.resolve_time
            ):
                emitted.append(self._emit_resolved(a, d))

        for a in unmatched_alerts.values():
            emitted.append(self._emit(a.alert_id, NotificationKind.REVOKED,
                                      reason="no longer firing", fire_start=a.fire_start))
            a.revoked = True

        for d in sorted(desired, key=lambda d: d.fire_start):
            if d in matched:
                continue
            alert = self._new_alert(d)
            emitted.append(self._emit_open(alert, d))
            if d.resolve_time is not None:
                emitted.append(self._emit_resolved(alert, d))
        return emitted

    # -- emission helpers -------------------------------------------------
    def _new_alert(self, d: DesiredNotification, supersedes: str | None = None) -> AlertRecord:
        self._alert_seq += 1
        alert = AlertRecord(
            alert_id=f"alert-{d.rule_id}-{self._alert_seq}",
            rule_id=d.rule_id,
            fire_start=d.fire_start,
            resolve_time=d.resolve_time,
            revision_opened=self.revision,
        )
        self.alerts[alert.alert_id] = alert
        alert.supersedes = supersedes
        return alert

    def _emit(self, alert_id: str, kind: NotificationKind, **payload) -> OutboxEntry:
        entry, created = self.outbox.enqueue(alert_id, kind, self.revision, payload)
        if created:
            self.audit.append(AuditRecord(self.revision, alert_id, kind, dict(payload)))
        return entry

    def _emit_open(self, alert: AlertRecord, d: DesiredNotification) -> OutboxEntry:
        alert.open_notified = True
        return self._emit(alert.alert_id, NotificationKind.OPEN,
                          fire_start=d.fire_start, open_time=d.open_time)

    def _emit_resolved(self, alert: AlertRecord, d: DesiredNotification) -> OutboxEntry:
        alert.resolve_notified = True
        alert.resolve_time = d.resolve_time
        return self._emit(alert.alert_id, NotificationKind.RESOLVED,
                          fire_start=d.fire_start, resolve_time=d.resolve_time)
