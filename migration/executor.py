"""Phased plan executor.

Per plan, phases run in order across all moves: RESERVE (target slot +
budget), COPY, SWITCH (ownership), RELEASE (reservation). Failure rolls
back metadata step by step in reverse order. Invariants:

- no double ownership: a partition is owned by exactly one slot at all times
- no budget leakage: rolled-back reservations refund the budget exactly
- stale acks: completion acks from older plan generations are rejected
- replay: re-executing the same plan is a no-op, never double-reserving
"""
from __future__ import annotations

from dataclasses import dataclass, field
from enum import Enum

from .solver import Plan


class Phase(Enum):
    RESERVE = "reserve"
    COPY = "copy"
    SWITCH = "switch"
    RELEASE = "release"


PHASE_ORDER = (Phase.RESERVE, Phase.COPY, Phase.SWITCH, Phase.RELEASE)


class ExecError(Exception):
    pass


class StaleAckError(ExecError):
    pass


@dataclass
class _SlotState:
    capacity: int
    owned: dict = field(default_factory=dict)  # pid -> weight
    reserved: dict = field(default_factory=dict)  # (plan_id, pid) -> weight

    def free(self) -> int:
        return self.capacity - sum(self.owned.values()) - sum(self.reserved.values())


@dataclass(frozen=True)
class ExecReport:
    plan_id: str
    success: bool
    rolled_back: bool
    steps: tuple
    budget_spent: int
    budget_remaining: int


class Executor:
    def __init__(self, capacities: dict, initial: dict, budget: int,
                 generation: int = 0):
        """initial: pid -> (sid, weight)."""
        self._slots = {sid: _SlotState(cap) for sid, cap in capacities.items()}
        self._owner = {}
        for pid, (sid, weight) in initial.items():
            if sid not in self._slots:
                raise ValueError(f"unknown slot {sid!r}")
            self._slots[sid].owned[pid] = weight
            self._owner[pid] = sid
        for sid, st in self._slots.items():
            if st.free() < 0:
                raise ValueError(f"initial state overflows slot {sid}")
        self._budget = budget
        self._spent = 0
        self._generation = generation
        self._copied = {}  # pid -> plan_id whose copy completed

    @property
    def generation(self) -> int:
        return self._generation

    @property
    def budget_remaining(self) -> int:
        return self._budget

    @property
    def budget_spent(self) -> int:
        return self._spent

    def owner_of(self, pid: str) -> str:
        return self._owner[pid]

    def owned_loads(self) -> dict:
        return {sid: sum(st.owned.values()) for sid, st in self._slots.items()}

    def reserved_bytes(self, sid: str) -> int:
        return sum(self._slots[sid].reserved.values())

    def owners_snapshot(self) -> dict:
        return dict(self._owner)

    def ack(self, pid: str, generation: int) -> bool:
        """Completion ack from a partition's previous home; stale ones rejected."""
        if generation != self._generation:
            raise StaleAckError(
                f"stale ack for {pid}: generation {generation} != {self._generation}")
        return True

    def execute(self, plan: Plan, fail_at: tuple | None = None) -> ExecReport:
        steps = []
        done = []  # completed undoable steps, for rollback
        try:
            for phase in PHASE_ORDER:
                for mv in plan.moves:
                    if fail_at is not None and fail_at == (phase, mv.pid):
                        raise ExecError(
                            f"injected failure at {phase.value} for {mv.pid}")
                    self._step(plan, phase, mv, steps, done)
        except ExecError:
            self._rollback(done, steps)
            return ExecReport(plan.plan_id, False, True, tuple(steps),
                              self._spent, self._budget)
        self._generation = plan.generation
        return ExecReport(plan.plan_id, True, False, tuple(steps),
                          self._spent, self._budget)

    def _step(self, plan: Plan, phase: Phase, mv, steps: list, done: list) -> None:
        if phase is Phase.RESERVE:
            self._reserve(plan, mv, steps, done)
        elif phase is Phase.COPY:
            self._copy(plan, mv, steps, done)
        elif phase is Phase.SWITCH:
            self._switch(plan, mv, steps, done)
        else:
            self._release(plan, mv, steps, done)

    def _reserve(self, plan, mv, steps, done):
        st = self._slots[mv.dst]
        key = (plan.plan_id, mv.pid)
        if st.reserved.get(key) == mv.weight or self._copied.get(mv.pid) == plan.plan_id:
            steps.append(f"reserve {mv.pid} on {mv.dst}: replay, skipped")
            return
        if mv.weight > self._budget:
            raise ExecError(
                f"move budget exceeded: need {mv.weight}, have {self._budget}")
        if mv.weight > st.free():
            raise ExecError(f"slot {mv.dst} cannot reserve {mv.weight} bytes")
        self._budget -= mv.weight
        self._spent += mv.weight
        st.reserved[key] = mv.weight
        steps.append(f"reserve {mv.pid} on {mv.dst}: {mv.weight} bytes")
        done.append(("unreserve", plan, mv))

    def _copy(self, plan, mv, steps, done):
        if self._copied.get(mv.pid) == plan.plan_id:
            steps.append(f"copy {mv.pid}: replay, skipped")
            return
        key = (plan.plan_id, mv.pid)
        if self._slots[mv.dst].reserved.get(key) != mv.weight:
            raise ExecError(f"copy of {mv.pid} without reservation")
        self._copied[mv.pid] = plan.plan_id
        steps.append(f"copy {mv.pid} -> {mv.dst}: done")
        done.append(("uncopy", plan, mv))

    def _switch(self, plan, mv, steps, done):
        dst_st = self._slots[mv.dst]
        if self._owner.get(mv.pid) == mv.dst and mv.pid in dst_st.owned:
            steps.append(f"switch {mv.pid}: replay, skipped")
            return
        src_st = self._slots[mv.src]
        if self._owner.get(mv.pid) != mv.src or mv.pid not in src_st.owned:
            raise ExecError(f"ownership mismatch for {mv.pid}")
        if mv.pid in dst_st.owned:
            raise ExecError(f"double ownership of {mv.pid}")
        weight = src_st.owned.pop(mv.pid)
        if weight != mv.weight:
            raise ExecError(f"weight mismatch for {mv.pid}")
        dst_st.owned[mv.pid] = weight
        self._owner[mv.pid] = mv.dst
        steps.append(f"switch {mv.pid}: {mv.src} -> {mv.dst}")
        done.append(("unswitch", plan, mv))

    def _release(self, plan, mv, steps, done):
        st = self._slots[mv.dst]
        key = (plan.plan_id, mv.pid)
        if key not in st.reserved:
            steps.append(f"release {mv.pid} on {mv.dst}: replay, skipped")
            return
        del st.reserved[key]
        steps.append(f"release reservation of {mv.pid} on {mv.dst}")
        done.append(("unrelease", plan, mv))

    def _rollback(self, done: list, steps: list) -> None:
        for op, plan, mv in reversed(done):
            if op == "unrelease":
                st = self._slots[mv.dst]
                key = (plan.plan_id, mv.pid)
                if key not in st.reserved:
                    st.reserved[key] = mv.weight
                    steps.append(f"rollback: restore reservation of {mv.pid}")
            elif op == "unswitch":
                dst_st = self._slots[mv.dst]
                if mv.pid in dst_st.owned:
                    weight = dst_st.owned.pop(mv.pid)
                    self._slots[mv.src].owned[mv.pid] = weight
                    self._owner[mv.pid] = mv.src
                    steps.append(f"rollback: switch {mv.pid} back to {mv.src}")
            elif op == "uncopy":
                if self._copied.get(mv.pid) == plan.plan_id:
                    del self._copied[mv.pid]
                    steps.append(f"rollback: drop copy of {mv.pid}")
            elif op == "unreserve":
                st = self._slots[mv.dst]
                key = (plan.plan_id, mv.pid)
                if key in st.reserved:
                    del st.reserved[key]
                    self._budget += mv.weight
                    self._spent -= mv.weight
                    steps.append(f"rollback: refund reservation of {mv.pid}")
