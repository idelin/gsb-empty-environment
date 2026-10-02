"""Single-level Deficit Round Robin (DRR) scheduler.

Design rules (deterministic):
  * One global round = one pass over tenants in a fixed order.
  * A tenant is *active* in a round iff it is not paused and its queue is
    non-empty.  Only active tenants receive a deficit credit.
  * Credit per visit: deficit = min(deficit + weight * quantum, cap).
    The cap bounds deficit accumulation; a task whose cost exceeds the cap
    can NEVER be served and is explicitly rejected when it reaches the head.
  * While the head task's cost <= deficit, it is dequeued and the cost is
    subtracted from the deficit (this is the dequeue evidence).
  * Tenants may be paused/resumed and re-weighted between rounds via events.

Starvation freedom: any head task with cost <= cap is eventually served,
because the deficit grows by weight*quantum (>= 1) every active round until
it reaches the cap.
"""
from __future__ import annotations

from collections import deque
from dataclasses import dataclass, field
from typing import Any


@dataclass
class Task:
    cost: int
    tag: str = ""

    def to_dict(self) -> dict:
        return {"tag": self.tag, "cost": self.cost}


@dataclass
class Tenant:
    tid: str
    weight: int
    quantum: int
    cap: int
    paused: bool = False
    deficit: int = 0
    queue: deque = field(default_factory=deque)      # deque[Task]
    rejected: list = field(default_factory=list)     # list[dict] evidence

    def active(self) -> bool:
        return not self.paused and len(self.queue) > 0


class DRRScheduler:
    """Single-level deficit round robin over a fixed tenant order."""

    def __init__(self, tenants: list[Tenant]):
        if not tenants:
            raise ValueError("need at least one tenant")
        self.tenants: dict[str, Tenant] = {t.tid: t for t in tenants}
        if len(self.tenants) != len(tenants):
            raise ValueError("duplicate tenant id")
        self.order: list[str] = [t.tid for t in tenants]
        self.round_no = 0
        self.trace: list[dict] = []

    # ---- dynamic operations (applied between rounds) ----
    def set_weight(self, tid: str, weight: int) -> None:
        if weight < 0:
            raise ValueError("weight must be >= 0")
        self.tenants[tid].weight = weight

    def pause(self, tid: str) -> None:
        self.tenants[tid].paused = True

    def resume(self, tid: str) -> None:
        self.tenants[tid].paused = False

    def enqueue(self, tid: str, cost: int, tag: str = "") -> None:
        self.tenants[tid].queue.append(Task(cost=cost, tag=tag or f"{tid}#{len(self.tenants[tid].queue)}"))

    # ---- core round ----
    def run_round(self) -> dict:
        self.round_no += 1
        record: dict[str, Any] = {"round": self.round_no, "visits": []}
        for tid in self.order:
            t = self.tenants[tid]
            visit: dict[str, Any] = {"tenant": tid}
            if not t.active():
                visit.update(skipped=True, paused=t.paused,
                             reason="paused" if t.paused else "empty queue",
                             deficit=t.deficit)
                record["visits"].append(visit)
                continue
            before = t.deficit
            credit = t.weight * t.quantum
            t.deficit = min(t.deficit + credit, t.cap)
            visit.update(deficit_before_credit=before, credit=credit,
                         deficit_after_credit=t.deficit, cap=t.cap, served=[])
            while t.queue:
                head = t.queue[0]
                if head.cost > t.cap:
                    # Permanently infeasible: explicit, evidence-backed rejection.
                    t.queue.popleft()
                    rej = {"task": head.tag, "cost": head.cost, "rejected": True,
                           "reason": f"cost {head.cost} > deficit cap {t.cap}: can never be served"}
                    t.rejected.append(rej)
                    visit["served"].append(rej)
                    continue
                if head.cost <= t.deficit:
                    t.queue.popleft()
                    t.deficit -= head.cost
                    visit["served"].append({"task": head.tag, "cost": head.cost,
                                            "deficit_after_dequeue": t.deficit})
                else:
                    visit["head_blocked"] = {"task": head.tag, "cost": head.cost,
                                             "deficit": t.deficit,
                                             "reason": "head cost > current deficit; waiting for more credit"}
                    break
            visit["deficit"] = t.deficit
            record["visits"].append(visit)
        self.trace.append(record)
        return record

    def done(self) -> bool:
        return all(not t.queue for t in self.tenants.values())

    # ---- config (de)serialisation ----
    @classmethod
    def from_config(cls, cfg: dict) -> "DRRScheduler":
        tenants = []
        for i, tc in enumerate(cfg["tenants"]):
            t = Tenant(tid=tc["id"], weight=tc.get("weight", 1),
                       quantum=tc.get("quantum", cfg.get("quantum_default", 1)),
                       cap=tc["cap"])
            for j, task in enumerate(tc.get("tasks", [])):
                cost = task["cost"] if isinstance(task, dict) else int(task)
                tag = task.get("tag", f"{t.tid}#{j}") if isinstance(task, dict) else f"{t.tid}#{j}"
                t.queue.append(Task(cost=cost, tag=tag))
            tenants.append(t)
        return cls(tenants)

    def apply_event(self, ev: dict) -> dict:
        op = ev["op"]
        if op == "pause":
            self.pause(ev["tenant"])
        elif op == "resume":
            self.resume(ev["tenant"])
        elif op == "set_weight":
            self.set_weight(ev["tenant"], int(ev["weight"]))
        elif op == "enqueue":
            self.enqueue(ev["tenant"], int(ev["cost"]), ev.get("tag", ""))
        else:
            raise ValueError(f"unknown event op: {op}")
        return {"applied": ev}

    def run(self, rounds: int, events: list[dict] | None = None) -> list[dict]:
        events = sorted(events or [], key=lambda e: e["round"])
        out = []
        for _ in range(rounds):
            nxt = self.round_no + 1
            for ev in [e for e in events if e["round"] == nxt]:
                out.append({"event": self.apply_event(ev), "before_round": nxt})
            out.append(self.run_round())
            if self.done() and not any(e["round"] > self.round_no for e in events):
                break
        return out
