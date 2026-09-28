"""Exact maximization of total push revenue by exhaustive enumeration.

The solver performs a depth-first search over assignments of notifications
(sorted by id) to either ``UNSENT`` or a concrete send slot.  Feasibility is
checked incrementally and an optimistic upper bound prunes only branches that
cannot strictly beat the incumbent, so optimality is guaranteed whenever the
search completes (``proven_optimal=True``).

Tie rule among equal-revenue schedules: lexicographically compare, in
notification-id order, the sequence of send slots where *not sent* precedes
every concrete slot (encoded as -1).  Smaller sequence wins.
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Mapping

from .checkers import EPS
from .model import UNSENT, Instance, Notification

DEFAULT_NODE_LIMIT = 2_000_000


@dataclass
class SolveResult:
    schedule: dict[str, int]
    revenue: float
    feasible: bool
    proven_optimal: bool
    nodes_explored: int
    nodes_pruned: int
    node_limit: int
    reason: str = ""


def objective_value(instance: Instance, schedule: Mapping[str, int]) -> float:
    return sum(instance.by_id[nid].revenue for nid in schedule)


def tie_key(instance: Instance, schedule: Mapping[str, int]) -> tuple[int, ...]:
    """Deterministic ordering key: per-id slots, UNSENT (-1) before any slot."""
    return tuple(schedule.get(note.nid, UNSENT) for note in instance.notifications)


def _slot_conflicts_rolling(
    user_slots: dict[str, list[int]],
    user: str,
    slot: int,
    window_length: int,
    limit: int,
) -> bool:
    """Whether placing *slot* for *user* violates the rolling window rule."""
    lo = slot - window_length + 1
    hi = slot + window_length - 1
    nearby = [
        other
        for other in user_slots.get(user, ())
        if lo <= other <= hi and abs(slot - other) < window_length
    ]
    if not nearby:
        return False
    # Limit > 1: recount every window containing the new slot explicitly.
    existing = user_slots.get(user, ())
    for start in range(max(0, slot - window_length + 1), slot + 1):
        end = start + window_length
        count = 1 + sum(1 for other in existing if start <= other < end)
        if count > limit:
            return True
    return False


def solve(instance: Instance, node_limit: int = DEFAULT_NODE_LIMIT) -> SolveResult:
    """Find a revenue-maximal feasible schedule by exhaustive DFS."""
    notifications = list(instance.notifications)
    capacities = instance.channel_capacity
    length = instance.window_length
    push_limit = instance.max_pushes_per_window

    # Explore high-revenue notifications first to find a good incumbent early;
    # tie keys are always compared in id order afterwards, so order is neutral.
    order = sorted(
        notifications,
        key=lambda n: (-n.revenue, n.nid),
    )
    remaining_ub: list[float] = [0.0] * (len(order) + 1)
    for idx in range(len(order) - 1, -1, -1):
        remaining_ub[idx] = remaining_ub[idx + 1] + max(0.0, order[idx].revenue)

    user_slots: dict[str, list[int]] = {}
    channel_used: dict[tuple[str, int], float] = {}
    activity_used: dict[str, int] = {}
    assignment: dict[str, int] = {}

    best_schedule: dict[str, int] = {}
    best_revenue = 0.0
    best_key: tuple[int, ...] | None = None
    current_revenue = 0.0
    nodes = 0
    pruned = 0
    stopped = False
    stop_reason = ""

    def feasible_placement(note: Notification, slot: int) -> bool:
        if _slot_conflicts_rolling(user_slots, note.user, slot, length, push_limit):
            return False
        key = (note.channel, slot)
        if channel_used.get(key, 0.0) + note.cost > capacities.get(note.channel, -1.0) + EPS:
            return False
        if note.activity_id is not None and activity_used.get(note.activity_id, 0) >= 1:
            return False
        return True

    def accept_leaf() -> None:
        nonlocal best_schedule, best_revenue, best_key
        key = tuple(assignment.get(note.nid, UNSENT) for note in notifications)
        better = (
            current_revenue > best_revenue + EPS
            or (
                abs(current_revenue - best_revenue) <= EPS
                and (best_key is None or key < best_key)
            )
        )
        if better:
            best_revenue = current_revenue
            best_key = key
            best_schedule = dict(assignment)

    def search(index: int) -> None:
        nonlocal nodes, pruned, stopped, current_revenue, stop_reason
        if stopped:
            return
        nodes += 1
        if nodes > node_limit:
            stopped = True
            stop_reason = f"node limit {node_limit} reached; optimality not proven"
            return
        # Optimistic bound: even taking every remaining positive-revenue item
        # cannot strictly improve on the incumbent -> prune (ties still need
        # exploration for the lexicographic rule, hence the strict compare).
        if current_revenue + remaining_ub[index] < best_revenue - EPS:
            pruned += 1
            return
        if index == len(order):
            accept_leaf()
            return

        note = order[index]

        # Concrete slots first (in ascending slot order), UNSENT last.
        for slot in note.options(instance.horizon):
            if feasible_placement(note, slot):
                assignment[note.nid] = slot
                user_slots.setdefault(note.user, []).append(slot)
                channel_used[(note.channel, slot)] = (
                    channel_used.get((note.channel, slot), 0.0) + note.cost
                )
                if note.activity_id is not None:
                    activity_used[note.activity_id] = (
                        activity_used.get(note.activity_id, 0) + 1
                    )
                current_revenue += note.revenue

                search(index + 1)

                current_revenue -= note.revenue
                if note.activity_id is not None:
                    activity_used[note.activity_id] -= 1
                channel_used[(note.channel, slot)] -= note.cost
                user_slots[note.user].remove(slot)
                del assignment[note.nid]
                if stopped:
                    return

        # Do not send this notification.
        search(index + 1)

    search(0)

    return SolveResult(
        schedule={nid: best_schedule[nid] for nid in sorted(best_schedule)},
        revenue=best_revenue,
        feasible=not stopped or bool(best_schedule) or len(notifications) == 0,
        proven_optimal=not stopped,
        nodes_explored=nodes,
        nodes_pruned=pruned,
        node_limit=node_limit,
        reason=stop_reason,
    )


def enumerate_schedules(
    instance: Instance,
    only_feasible: bool = True,
    node_limit: int = DEFAULT_NODE_LIMIT,
) -> list[dict[str, int]]:
    """Enumerate schedules in canonical order (id order, ascending slots).

    Returns partial assignments containing only sent notifications.  When
    ``only_feasible`` is False every assignment (including infeasible ones) is
    returned, which is mainly useful for tests.
    """
    notifications = list(instance.notifications)
    capacities = instance.channel_capacity
    length = instance.window_length
    push_limit = instance.max_pushes_per_window

    results: list[dict[str, int]] = []
    user_slots: dict[str, list[int]] = {}
    channel_used: dict[tuple[str, int], float] = {}
    activity_used: dict[str, int] = {}
    assignment: dict[str, int] = {}
    nodes = 0

    def feasible_placement(note: Notification, slot: int) -> bool:
        if _slot_conflicts_rolling(user_slots, note.user, slot, length, push_limit):
            return False
        if channel_used.get((note.channel, slot), 0.0) + note.cost > capacities.get(
            note.channel, -1.0
        ) + EPS:
            return False
        if note.activity_id is not None and activity_used.get(note.activity_id, 0) >= 1:
            return False
        return True

    def search(index: int) -> None:
        nonlocal nodes
        nodes += 1
        if nodes > node_limit:
            raise OverflowError(f"node limit {node_limit} reached during enumeration")
        if index == len(notifications):
            results.append(dict(assignment))
            return

        note = notifications[index]
        # UNSENT branch first so canonical output lists suppressed-first tuples.
        search(index + 1)
        for slot in note.options(instance.horizon):
            if only_feasible and not feasible_placement(note, slot):
                continue
            assignment[note.nid] = slot
            user_slots.setdefault(note.user, []).append(slot)
            channel_used[(note.channel, slot)] = (
                channel_used.get((note.channel, slot), 0.0) + note.cost
            )
            if note.activity_id is not None:
                activity_used[note.activity_id] = activity_used.get(note.activity_id, 0) + 1

            search(index + 1)

            if note.activity_id is not None:
                activity_used[note.activity_id] -= 1
            channel_used[(note.channel, slot)] -= note.cost
            user_slots[note.user].remove(slot)
            del assignment[note.nid]

    search(0)
    return results

