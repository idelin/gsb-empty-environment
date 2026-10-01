"""Flat deficit round-robin (DRR) scheduler with tenant weights and deficit caps.

Python 3.11, standard library only. All example data is synthetic.

Algorithm (explicit DRR):
  * Each round, every non-paused, backlogged tenant is credited
    ``weight * quantum`` deficit, capped at its deficit cap.
  * Only the head task of a tenant queue may run, and only while its
    integer cost does not exceed the tenant's current deficit.
  * Each executed task deducts its cost from the tenant deficit.
  * A tenant whose queue is empty has its deficit reset to 0 (no banking
    of credit while idle), and a paused tenant is frozen (no credit).

Starvation freedom: any task with ``cost <= deficit_cap`` is feasible and
executes within ``ceil(cost / (weight * quantum))`` rounds of reaching the
head of its tenant queue, because the deficit grows by ``weight * quantum``
every round until the cap is reached. Tasks with ``cost > deficit_cap`` are
rejected explicitly at enqueue time (they could never be served).
"""
from __future__ import annotations

from collections import deque
from dataclasses import dataclass


@dataclass(frozen=True)
class Task:
    task_id: str
    cost: int


class InfeasibleTaskError(ValueError):
    """Raised when a task can never be served because cost > deficit cap."""


class FlatTenant:
    __slots__ = ("name", "weight", "cap", "paused", "deficit", "queue")

    def __init__(self, name: str, weight: int, cap: int):
        self.name = name
        self.weight = weight
        self.cap = cap
        self.paused = False
        self.deficit = 0
        self.queue: deque[Task] = deque()


class FlatDRR:
    """Deficit round-robin over a flat set of weighted tenants."""

    def __init__(self, quantum: int, deficit_cap: int):
        if quantum <= 0:
            raise ValueError("quantum must be a positive integer")
        if deficit_cap <= 0:
            raise ValueError("deficit_cap must be a positive integer")
        self.quantum = quantum
        self.default_cap = deficit_cap
        self.tenants: dict[str, FlatTenant] = {}
        self.order: list[str] = []
        self.cursor = 0
        self.round = 0

    def add_tenant(self, name: str, weight: int = 1, cap: int | None = None) -> FlatTenant:
        if name in self.tenants:
            raise ValueError(f"duplicate tenant {name!r}")
        if weight <= 0:
            raise ValueError("weight must be a positive integer")
        cap = self.default_cap if cap is None else cap
        if cap <= 0:
            raise ValueError("cap must be a positive integer")
        tenant = FlatTenant(name, weight, cap)
        self.tenants[name] = tenant
        self.order.append(name)
        return tenant

    def try_enqueue(self, tenant: str, task_id: str, cost: int) -> dict:
        """Enqueue a task, or return an explicit rejection event."""
        t = self.tenants[tenant]
        if cost <= 0:
            raise ValueError("cost must be a positive integer")
        if cost > t.cap:
            return {
                "event": "reject",
                "tenant": tenant,
                "task": task_id,
                "cost": cost,
                "reason": "cost_exceeds_deficit_cap",
                "cap": t.cap,
            }
        t.queue.append(Task(task_id, cost))
        return {
            "event": "enqueue",
            "tenant": tenant,
            "task": task_id,
            "cost": cost,
            "queue_depth": len(t.queue),
        }

    def enqueue(self, tenant: str, task_id: str, cost: int) -> dict:
        ev = self.try_enqueue(tenant, task_id, cost)
        if ev["event"] == "reject":
            raise InfeasibleTaskError(
                f"task {task_id!r} cost {cost} exceeds deficit cap {ev['cap']} "
                f"of tenant {tenant!r}; task can never be served"
            )
        return ev

    def set_weight(self, tenant: str, weight: int) -> dict:
        if weight <= 0:
            raise ValueError("weight must be a positive integer")
        t = self.tenants[tenant]
        old = t.weight
        t.weight = weight
        return {"event": "set_weight", "tenant": tenant, "old_weight": old, "weight": weight}

    def pause(self, tenant: str) -> dict:
        self.tenants[tenant].paused = True
        return {"event": "pause", "tenant": tenant}

    def resume(self, tenant: str) -> dict:
        self.tenants[tenant].paused = False
        return {"event": "resume", "tenant": tenant}

    def has_work(self) -> bool:
        return any(not t.paused and t.queue for t in self.tenants.values())

    def run_round(self) -> list[dict]:
        """Run one DRR round; return per-step deficit/dequeue evidence."""
        self.round += 1
        events: list[dict] = []
        n = len(self.order)
        if n == 0:
            return events
        start = self.cursor % n
        last_idx: int | None = None
        for i in range(n):
            idx = (start + i) % n
            name = self.order[idx]
            t = self.tenants[name]
            if t.paused:
                events.append({"event": "skip", "round": self.round, "tenant": name, "reason": "paused"})
                continue
            if not t.queue:
                if t.deficit:
                    events.append({
                        "event": "reset", "round": self.round, "tenant": name,
                        "deficit_before": t.deficit, "reason": "idle",
                    })
                    t.deficit = 0
                continue
            before = t.deficit
            credit = t.weight * self.quantum
            t.deficit = min(before + credit, t.cap)
            events.append({
                "event": "credit", "round": self.round, "tenant": name,
                "weight": t.weight, "quantum": self.quantum, "credit": credit,
                "deficit_before": before, "deficit_after": t.deficit, "cap": t.cap,
            })
            while t.queue and t.queue[0].cost <= t.deficit:
                task = t.queue.popleft()
                db = t.deficit
                t.deficit -= task.cost
                events.append({
                    "event": "dequeue", "round": self.round, "tenant": name,
                    "task": task.task_id, "cost": task.cost,
                    "deficit_before": db, "deficit_after": t.deficit,
                })
            if t.queue:
                events.append({
                    "event": "head_blocked", "round": self.round, "tenant": name,
                    "head_task": t.queue[0].task_id, "head_cost": t.queue[0].cost,
                    "deficit": t.deficit,
                })
            last_idx = idx
        if last_idx is not None:
            self.cursor = (last_idx + 1) % n
        return events

    def run_until_idle(self, max_rounds: int = 1000) -> list[dict]:
        events: list[dict] = []
        rounds = 0
        while self.has_work() and rounds < max_rounds:
            events.extend(self.run_round())
            rounds += 1
        if self.has_work():
            events.append({
                "event": "incomplete", "reason": "max_rounds_reached", "max_rounds": max_rounds,
            })
        return events

    def max_rounds_to_serve(self, tenant: str, cost: int) -> int:
        """Worst-case rounds for a feasible head task to become servable."""
        t = self.tenants[tenant]
        if cost > t.cap:
            raise InfeasibleTaskError(f"cost {cost} exceeds cap {t.cap}")
        credit = t.weight * self.quantum
        rounds = 0
        deficit = t.deficit
        while deficit < cost:
            deficit = min(deficit + credit, t.cap)
            rounds += 1
        return rounds
