"""Deterministic migration plan builder.

Turns a start assignment and a target assignment into ordered moves
packed into rounds whose total bytes never exceed the per-round move
budget. A single partition heavier than the budget cannot be moved at
all and is reported separately rather than force-scheduled.

Plan identity is a hash of its canonical content, so the same inputs
always yield the same plan_id, and replaying that id is idempotent.
"""

from __future__ import annotations

import hashlib
from dataclasses import dataclass, field


@dataclass(frozen=True)
class Move:
    pid: str
    src: str
    dst: str
    nbytes: int


@dataclass(frozen=True)
class Round:
    index: int
    moves: tuple  # tuple[Move, ...]

    @property
    def nbytes(self) -> int:
        return sum(m.nbytes for m in self.moves)


@dataclass(frozen=True)
class Plan:
    plan_id: str
    generation: int
    budget_per_round: int
    rounds: tuple          # tuple[Round, ...]
    unschedulable: tuple   # tuple[Move, ...] -- heavier than the budget

    def steps(self):
        """Flat (round_index, move) iteration in execution order."""
        for rnd in self.rounds:
            for move in rnd.moves:
                yield rnd.index, move


def build_plan(cluster, target_assignment: dict, budget_per_round: int,
               generation: int) -> Plan:
    if budget_per_round <= 0:
        raise ValueError("budget_per_round must be positive")

    moves = []
    for pid in sorted(cluster.partitions):
        src = cluster.current.get(pid)
        dst = target_assignment.get(pid)
        if dst is None or src == dst:
            continue
        if src is None:
            raise ValueError(f"partition {pid} has no current placement; cannot migrate")
        moves.append(Move(pid, src, dst, cluster.partitions[pid].weight))

    # Deterministic order: heaviest first, then pid.
    moves.sort(key=lambda m: (-m.nbytes, m.pid))

    schedulable, unschedulable = [], []
    for move in moves:
        if move.nbytes > budget_per_round:
            unschedulable.append(move)
        else:
            schedulable.append(move)

    # First-fit decreasing packing into budget-bounded rounds.
    rounds: list[list[Move]] = []
    used: list[int] = []
    for move in schedulable:
        placed = False
        for idx in range(len(rounds)):
            if used[idx] + move.nbytes <= budget_per_round:
                rounds[idx].append(move)
                used[idx] += move.nbytes
                placed = True
                break
        if not placed:
            rounds.append([move])
            used.append(move.nbytes)

    canonical_parts = [str(budget_per_round), str(generation)]
    round_objs = []
    for idx, moves_in_round in enumerate(rounds):
        round_objs.append(Round(idx, tuple(moves_in_round)))
        canonical_parts.append(
            f"R{idx}:" + ",".join(f"{m.pid}:{m.src}>{m.dst}:{m.nbytes}"
                                  for m in moves_in_round)
        )
    for m in unschedulable:
        canonical_parts.append(f"X:{m.pid}:{m.src}>{m.dst}:{m.nbytes}")
    plan_id = hashlib.sha256("|".join(canonical_parts).encode("utf-8")).hexdigest()[:16]

    return Plan(
        plan_id=plan_id,
        generation=generation,
        budget_per_round=budget_per_round,
        rounds=tuple(round_objs),
        unschedulable=tuple(unschedulable),
    )
