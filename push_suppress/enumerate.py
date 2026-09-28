"""Brute-force enumeration of all feasible schedules.

This is an independent, deliberately simple implementation used for
two purposes: the CLI ``enumerate`` command (small-example enumeration
output), and cross-checking the branch-and-bound solver's optimality
in tests.

It enumerates every combination of (notification -> send time or skip)
and keeps exactly the schedules that pass all constraint checkers.
Only suitable for small instances.
"""

from __future__ import annotations

from typing import Iterator, List, Tuple

from .checkers import is_feasible
from .model import Instance, Send
from .solver import schedule_key


def enumerate_schedules(instance: Instance) -> Iterator[List[Send]]:
    """Yield every feasible schedule (as a list of Send, sorted by
    (notification_id, time))."""
    notifications = sorted(instance.notifications, key=lambda n: n.id)
    current: List[Send] = []

    def rec(index: int) -> Iterator[List[Send]]:
        if index == len(notifications):
            if is_feasible(instance, current):
                yield sorted(current, key=lambda s: (s.notification_id, s.time))
            return
        n = notifications[index]
        for t in n.times():
            current.append(Send(n.id, t))
            yield from rec(index + 1)
            current.pop()
        yield from rec(index + 1)  # skip n

    yield from rec(0)


def schedule_profit(instance: Instance, sends: List[Send]) -> float:
    by_id = instance.by_id()
    return sum(by_id[s.notification_id].profit for s in sends)


def best_by_enumeration(instance: Instance) -> Tuple[float, tuple, List[Send]]:
    """Return (profit, tie-break key, sends) of the best feasible
    schedule found by exhaustive enumeration."""
    best_profit = 0.0
    best_key: tuple = ()
    best_sends: List[Send] = []
    for sends in enumerate_schedules(instance):
        profit = schedule_profit(instance, sends)
        key = schedule_key(sends)
        if profit > best_profit + 1e-12 or (
            abs(profit - best_profit) <= 1e-12 and key < best_key
        ):
            best_profit, best_key, best_sends = profit, key, sends
    return best_profit, best_key, best_sends
