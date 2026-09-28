"""Exact solver: branch-and-bound over (notification, send-time) choices.

The solver maximizes total profit. Among all schedules attaining the
maximum profit it picks the lexicographically smallest tie-break key,
where the key of a schedule is the tuple of its ``(notification_id,
time)`` pairs sorted by notification id first, then send time. This
makes the optimum deterministic.

The algorithm is exact (it explores every choice not pruned by a strict
profit upper bound), so the returned schedule is provably optimal for
the given instance. Runtime is exponential in the worst case; it is
intended for small to medium instances.
"""

from __future__ import annotations

import bisect
from dataclasses import dataclass, field
from typing import Dict, Hashable, List, Optional, Tuple

from .model import Instance, Notification, Send

ScheduleKey = Tuple[Tuple[Hashable, int], ...]


def schedule_key(sends: List[Send]) -> ScheduleKey:
    """Tie-break key: (id, time) pairs sorted by id, then time."""
    return tuple(sorted((s.notification_id, s.time) for s in sends))


@dataclass
class SolveResult:
    profit: float
    sends: List[Send]  # sorted by (notification_id, time)
    nodes_explored: int = 0
    optimal: bool = True  # exact solver: always provably optimal

    @property
    def key(self) -> ScheduleKey:
        return schedule_key(self.sends)


@dataclass
class _State:
    profit: float = 0.0
    cost: float = 0.0
    sends: List[Send] = field(default_factory=list)
    user_times: Dict[str, List[int]] = field(default_factory=dict)  # sorted
    channel_usage: Dict[Tuple[str, int], int] = field(default_factory=dict)
    used_groups: set = field(default_factory=set)


class Solver:
    """Branch-and-bound exact solver for one instance."""

    def __init__(self, instance: Instance):
        self.instance = instance
        # Deterministic notification order: by id.
        self._notifications = sorted(instance.notifications, key=lambda n: n.id)
        # Suffix sums of positive profits: optimistic upper bound.
        positives = [max(0.0, n.profit) for n in self._notifications]
        self._suffix = [0.0] * (len(positives) + 1)
        for i in range(len(positives) - 1, -1, -1):
            self._suffix[i] = self._suffix[i + 1] + positives[i]
        self._best_profit = 0.0  # empty schedule is always feasible
        self._best_key: ScheduleKey = ()
        self._best_sends: List[Send] = []
        self.nodes_explored = 0

    # -- incremental feasibility ----------------------------------------

    def _user_window_ok(self, times: List[int], t: int) -> bool:
        """Check the rolling-window rule after inserting ``t`` into the
        user's sorted send-time list (without mutating it)."""
        K = self.instance.window_max_sends
        L = self.instance.window_length
        i = bisect.bisect_left(times, t)
        # Only windows covering the new point can be violated; those
        # start at indices j in [i - K, i] and end at j + K.
        for j in range(max(0, i - K), i + 1):
            lo = times[j] if j < i else t
            hi_index = j + K
            if hi_index < i:
                hi = times[hi_index]
            elif hi_index == i:
                hi = t
            elif hi_index < len(times) + 1:
                hi = times[hi_index - 1]
            else:
                continue
            if hi - lo < L:
                return False
        return True

    def _can_place(self, n: Notification, t: int, state: _State) -> bool:
        inst = self.instance
        if inst.budget is not None and state.cost + n.cost > inst.budget + 1e-9:
            return False
        if n.mutex_group is not None and n.mutex_group in state.used_groups:
            return False
        cap = inst.channel_capacity.get(n.channel)
        if cap is not None and state.channel_usage.get((n.channel, t), 0) >= cap:
            return False
        times = state.user_times.get(n.user, [])
        return self._user_window_ok(times, t)

    def _place(self, n: Notification, t: int, state: _State) -> None:
        state.profit += n.profit
        state.cost += n.cost
        state.sends.append(Send(n.id, t))
        times = state.user_times.setdefault(n.user, [])
        bisect.insort(times, t)
        key = (n.channel, t)
        state.channel_usage[key] = state.channel_usage.get(key, 0) + 1
        if n.mutex_group is not None:
            state.used_groups.add(n.mutex_group)

    def _unplace(self, n: Notification, t: int, state: _State) -> None:
        state.profit -= n.profit
        state.cost -= n.cost
        state.sends.pop()
        times = state.user_times[n.user]
        times.pop(bisect.bisect_left(times, t))
        key = (n.channel, t)
        state.channel_usage[key] -= 1
        if n.mutex_group is not None:
            state.used_groups.discard(n.mutex_group)

    # -- search ----------------------------------------------------------

    def _consider(self, state: _State) -> None:
        key = schedule_key(state.sends)
        if state.profit > self._best_profit + 1e-12 or (
            abs(state.profit - self._best_profit) <= 1e-12 and key < self._best_key
        ):
            self._best_profit = state.profit
            self._best_key = key
            self._best_sends = list(state.sends)

    def _search(self, index: int, state: _State) -> None:
        self.nodes_explored += 1
        if index == len(self._notifications):
            self._consider(state)
            return
        # Prune only when even the optimistic bound cannot reach the
        # incumbent profit; ties must still be explored for tie-breaking.
        if state.profit + self._suffix[index] < self._best_profit - 1e-12:
            return
        n = self._notifications[index]
        # Option 1: send n at each feasible time (ascending time order).
        for t in n.times():
            if self._can_place(n, t, state):
                self._place(n, t, state)
                self._search(index + 1, state)
                self._unplace(n, t, state)
        # Option 2: skip n.
        self._search(index + 1, state)

    def solve(self) -> SolveResult:
        self._search(0, _State())
        sends = sorted(self._best_sends, key=lambda s: (s.notification_id, s.time))
        return SolveResult(
            profit=self._best_profit,
            sends=sends,
            nodes_explored=self.nodes_explored,
            optimal=True,
        )


def solve(instance: Instance) -> SolveResult:
    return Solver(instance).solve()
