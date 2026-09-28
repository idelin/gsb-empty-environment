"""Constraint re-checkers for candidate schedules.

Each checker returns a list of human-readable violation strings; an
empty list means the constraint holds. ``validate_schedule`` runs all
checkers and aggregates the violations.

The per-user rolling-window rule is *not* a fixed-bucket count: for
window length L and limit K, a user's sorted send times must satisfy
``times[i + K] - times[i] >= L`` for every ``i``. This is exactly the
condition that every half-open interval ``[t, t + L)`` contains at most
K sends. With L=3, K=1: sends at 0 and 2 conflict, sends at 0 and 3
are allowed, and sends at 2 and 4 conflict even though they fall into
different length-3 buckets ``[0,3)`` and ``[3,6)``.
"""

from __future__ import annotations

from collections import defaultdict
from typing import Dict, Hashable, List, Tuple

from .model import Instance, Notification, Send


def _known_notifications(
    instance: Instance, sends: List[Send], violations: List[str]
) -> Dict[Hashable, Notification]:
    by_id = instance.by_id()
    for s in sends:
        if s.notification_id not in by_id:
            violations.append(f"unknown notification id {s.notification_id!r}")
    return by_id


def check_send_once_and_window(instance: Instance, sends: List[Send]) -> List[str]:
    """Each notification is sent at most once, inside its time window,
    and (when a horizon is set) inside ``[0, time_horizon)``."""
    violations: List[str] = []
    by_id = _known_notifications(instance, sends, violations)
    seen: Dict[Hashable, int] = {}
    for s in sends:
        n = by_id.get(s.notification_id)
        if n is None:
            continue
        if s.notification_id in seen:
            violations.append(
                f"notification {s.notification_id!r} sent more than once"
            )
            continue
        seen[s.notification_id] = s.time
        if not (n.start <= s.time <= n.end):
            violations.append(
                f"notification {s.notification_id!r} sent at t={s.time} outside "
                f"its window [{n.start}, {n.end}]"
            )
        if instance.time_horizon is not None and not (
            0 <= s.time < instance.time_horizon
        ):
            violations.append(
                f"notification {s.notification_id!r} sent at t={s.time} outside "
                f"time horizon [0, {instance.time_horizon})"
            )
    return violations


def check_user_rolling_window(instance: Instance, sends: List[Send]) -> List[str]:
    """Per-user rolling half-open window limit.

    For every user and every integer t, the half-open interval
    ``[t, t + L)`` contains at most K sends (L = window_length,
    K = window_max_sends). Checked exactly via sorted send times.
    """
    violations: List[str] = []
    by_id = instance.by_id()
    L, K = instance.window_length, instance.window_max_sends
    per_user: Dict[str, List[Tuple[int, Hashable]]] = defaultdict(list)
    for s in sends:
        n = by_id.get(s.notification_id)
        if n is not None:
            per_user[n.user].append((s.time, s.notification_id))
    for user, entries in per_user.items():
        entries.sort()
        times = [t for t, _ in entries]
        for i in range(len(times) - K):
            j = i + K
            if times[j] - times[i] < L:
                violations.append(
                    f"user {user!r}: {K + 1} sends within half-open window "
                    f"[{times[i]}, {times[i] + L}) "
                    f"(e.g. notifications {entries[i][1]!r} at t={times[i]} and "
                    f"{entries[j][1]!r} at t={times[j]}); limit is {K} per "
                    f"length-{L} window"
                )
    return violations


def check_channel_capacity(instance: Instance, sends: List[Send]) -> List[str]:
    """Global per-channel per-time-slot capacity."""
    violations: List[str] = []
    by_id = instance.by_id()
    usage: Dict[Tuple[str, int], int] = defaultdict(int)
    for s in sends:
        n = by_id.get(s.notification_id)
        if n is not None:
            usage[(n.channel, s.time)] += 1
    for (channel, t), count in sorted(usage.items()):
        cap = instance.channel_capacity.get(channel)
        if cap is not None and count > cap:
            violations.append(
                f"channel {channel!r} at t={t}: {count} sends exceed "
                f"capacity {cap}"
            )
    return violations


def check_mutex(instance: Instance, sends: List[Send]) -> List[str]:
    """At most one send per mutually exclusive campaign (mutex group)."""
    violations: List[str] = []
    by_id = instance.by_id()
    per_group: Dict[str, List[Hashable]] = defaultdict(list)
    for s in sends:
        n = by_id.get(s.notification_id)
        if n is not None and n.mutex_group is not None:
            per_group[n.mutex_group].append(s.notification_id)
    for group, ids in sorted(per_group.items()):
        if len(ids) > 1:
            violations.append(
                f"mutex group {group!r}: {len(ids)} sends {sorted(ids)!r}; "
                f"at most one allowed"
            )
    return violations


def check_budget(instance: Instance, sends: List[Send]) -> List[str]:
    """Total cost of scheduled sends must not exceed the budget."""
    violations: List[str] = []
    if instance.budget is None:
        return violations
    by_id = instance.by_id()
    total = 0.0
    for s in sends:
        n = by_id.get(s.notification_id)
        if n is not None:
            total += n.cost
    if total > instance.budget + 1e-9:
        violations.append(
            f"total cost {total:g} exceeds budget {instance.budget:g}"
        )
    return violations


ALL_CHECKERS = (
    check_send_once_and_window,
    check_user_rolling_window,
    check_channel_capacity,
    check_mutex,
    check_budget,
)


def validate_schedule(instance: Instance, sends: List[Send]) -> List[str]:
    """Run every constraint checker and return all violations."""
    violations: List[str] = []
    for checker in ALL_CHECKERS:
        violations.extend(checker(instance, sends))
    return violations


def is_feasible(instance: Instance, sends: List[Send]) -> bool:
    return not validate_schedule(instance, sends)
