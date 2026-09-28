"""Independent constraint re-checkers for a push schedule.

A *schedule* is a mapping ``notification_id -> send_slot`` containing only
notifications that are sent.  Each checker is independent and returns a list
of :class:`Violation`; an empty list means that particular constraint holds.
"""

from __future__ import annotations

from collections import defaultdict
from dataclasses import dataclass

from .model import Instance, Notification

EPS = 1e-9


@dataclass(frozen=True)
class Violation:
    constraint: str
    detail: str
    notification_ids: tuple[str, ...] = ()
    slot: int | None = None
    channel: str | None = None


def _sent_by_id(instance: Instance, schedule: Mapping[str, int]):
    notifications = instance.by_id
    unknown = [nid for nid in schedule if nid not in notifications]
    return notifications, unknown


def check_identity(instance: Instance, schedule: Mapping[str, int]) -> list[Violation]:
    """Every sent id exists and maps to one integer slot."""
    violations = [
        Violation("identity", f"unknown notification id {nid!r}", (nid,))
        for nid in schedule
        if nid not in instance.by_id
    ]
    for nid, slot in schedule.items():
        if isinstance(slot, bool) or not isinstance(slot, int):
            violations.append(
                Violation("identity", f"send slot for {nid!r} must be an integer", (nid,))
            )
    return violations


def check_time_window(instance: Instance, schedule: Mapping[str, int]) -> list[Violation]:
    """Send slots must lie in [0, horizon) and the notification window."""
    violations: list[Violation] = []
    for nid, slot in schedule.items():
        note = instance.by_id.get(nid)
        if note is None or isinstance(slot, bool) or not isinstance(slot, int):
            continue
        if slot < 0 or slot >= instance.horizon:
            violations.append(
                Violation(
                    "time_window",
                    f"{nid!r} sent at slot {slot} outside horizon 0..{instance.horizon - 1}",
                    (nid,),
                    slot,
                )
            )
        elif slot < note.release or slot > note.deadline:
            violations.append(
                Violation(
                    "time_window",
                    f"{nid!r} sent at slot {slot} outside window "
                    f"[{note.release}, {note.deadline}]",
                    (nid,),
                    slot,
                )
            )
    return violations


def _user_sent_slots(
    instance: Instance, schedule: Mapping[str, int]
) -> dict[str, list[tuple[int, str]]]:
    by_user: dict[str, list[tuple[int, str]]] = defaultdict(list)
    for nid, slot in schedule.items():
        note = instance.by_id.get(nid)
        if note is None or not isinstance(slot, int) or isinstance(slot, bool):
            continue
        if slot < 0 or slot >= instance.horizon:
            continue
        by_user[note.user].append((slot, nid))
    for slots in by_user.values():
        slots.sort()
    return by_user


def check_user_rolling_limit(
    instance: Instance, schedule: Mapping[str, int]
) -> list[Violation]:
    """Per-user rolling half-open window limit.

    For every half-open window ``[s, s + L)`` that overlaps the horizon
    (s = 0 .. horizon-1), the number of pushes to the same user inside it
    must be <= ``max_pushes_per_window``.  With the acceptance settings
    (L=3, limit=1), slots 0 and 2 clash (both fall in [0,3)) while 0 and 3
    are allowed (3 is excluded from [0,3)).
    """
    violations: list[Violation] = []
    length = instance.window_length
    limit = instance.max_pushes_per_window
    for user, items in _user_sent_slots(instance, schedule).items():
        slots = [slot for slot, _ in items]
        offending: set[str] = set()
        bad_starts: list[int] = []
        for start in range(instance.horizon):
            end = start + length
            inside = [
                nid for slot, nid in items if start <= slot < end
            ]
            if len(inside) > limit:
                bad_starts.append(start)
                offending.update(inside)
        if bad_starts:
            span = bad_starts[0] if len(bad_starts) == 1 else f"{bad_starts[0]}..{bad_starts[-1]}"
            violations.append(
                Violation(
                    "user_rolling_limit",
                    f"user {user!r}: slots {slots} exceed {limit} push(es) in any "
                    f"half-open window of length {length} (e.g. starting at {span})",
                    tuple(sorted(offending)),
                )
            )
    return violations


def check_channel_capacity(
    instance: Instance, schedule: Mapping[str, int]
) -> list[Violation]:
    """Global per-channel, per-slot cost capacity: sum(cost) <= capacity."""
    violations: list[Violation] = []
    usage: dict[tuple[str, int], list[tuple[str, float]]] = defaultdict(list)
    for nid, slot in schedule.items():
        note = instance.by_id.get(nid)
        if note is None or not isinstance(slot, int) or isinstance(slot, bool):
            continue
        if slot < 0 or slot >= instance.horizon:
            continue
        usage[(note.channel, slot)].append((nid, note.cost))

    for (channel, slot), items in sorted(usage.items()):
        total = sum(cost for _, cost in items)
        capacity = instance.channel_capacity.get(channel)
        if capacity is None:
            violations.append(
                Violation(
                    "channel_capacity",
                    f"channel {channel!r} has no capacity defined "
                    f"(used at slot {slot})",
                    tuple(sorted(nid for nid, _ in items)),
                    slot,
                    channel,
                )
            )
        elif total > capacity + EPS:
            violations.append(
                Violation(
                    "channel_capacity",
                    f"channel {channel!r} slot {slot}: total cost {total:g} "
                    f"exceeds capacity {capacity:g}",
                    tuple(sorted(nid for nid, _ in items)),
                    slot,
                    channel,
                )
            )
    return violations


def check_mutex(instance: Instance, schedule: Mapping[str, int]) -> list[Violation]:
    """At most one sent notification per mutually-exclusive activity id."""
    groups: dict[str, list[tuple[str, int]]] = defaultdict(list)
    for nid, slot in schedule.items():
        note = instance.by_id.get(nid)
        if note is not None and note.activity_id is not None:
            groups[note.activity_id].append((nid, slot))
    violations: list[Violation] = []
    for activity_id, items in sorted(groups.items()):
        if len(items) > 1:
            detail = ", ".join(f"{nid}@{slot}" for nid, slot in sorted(items))
            violations.append(
                Violation(
                    "mutex",
                    f"activity {activity_id!r} sent {len(items)} times: {detail}",
                    tuple(nid for nid, _ in sorted(items)),
                )
            )
    return violations


CHECKERS = (
    check_identity,
    check_time_window,
    check_user_rolling_limit,
    check_channel_capacity,
    check_mutex,
)


def verify_schedule(
    instance: Instance, schedule: Mapping[str, int]
) -> list[Violation]:
    """Run every constraint checker and concatenate all violations."""
    violations: list[Violation] = []
    for checker in CHECKERS:
        violations.extend(checker(instance, schedule))
    return violations
