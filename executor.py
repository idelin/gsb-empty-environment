"""Phased migration executor with metadata rollback.

Every move runs through four phases:
    RESERVE -> COPY -> SWITCH -> RELEASE

Safety properties (asserted by `consistency_report`, covered by tests):
- single ownership: the ownership metadata is a CAS and always names
  exactly one slot that physically holds the partition;
- generation fencing: completion replies belonging to an older plan
  generation are rejected, and a stale SWITCH cannot clobber a newer
  owner;
- no budget leak: a reservation charges the round budget; releasing or
  rolling back refunds exactly that charge;
- idempotent replay: every phase is a no-op when its effect already
  exists, so replaying the same plan_id never double-reserves a slot.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from enum import Enum

from planner import Plan


class Phase(Enum):
    RESERVE = "reserve"
    COPY = "copy"
    SWITCH = "switch"
    RELEASE = "release"


PHASE_ORDER = (Phase.RESERVE, Phase.COPY, Phase.SWITCH, Phase.RELEASE)


class InjectedFault(Exception):
    """Test hook: a simulated phase failure."""


class StaleGenerationError(Exception):
    """A completion reply / switch came from an older plan generation."""


class CapacityExceeded(Exception):
    """Reservation would overflow the target slot or the round budget."""


class OwnershipConflict(Exception):
    """CAS failed: the source no longer owns the partition."""


class MigrationFailed(Exception):
    def __init__(self, move, phase, cause):
        super().__init__(f"migration of {move.pid} failed at {phase.value}: {cause}")
        self.move = move
        self.phase = phase
        self.cause = cause


@dataclass
class Step:
    plan_id: str
    generation: int
    round_index: int
    pid: str
    src: str
    dst: str
    nbytes: int


@dataclass
class ClusterMetadata:
    capacities: dict
    weights: dict
    ownership: dict = field(default_factory=dict)       # pid -> sid
    owner_generation: dict = field(default_factory=dict)  # pid -> generation
    data: dict = field(default_factory=dict)            # sid -> {pid}
    # sid -> {(plan_id, pid): (bytes, round_index)}
    reservations: dict = field(default_factory=dict)
    budget_ledger: dict = field(default_factory=dict)   # round -> bytes
    latest_generation: int = -1

    def __post_init__(self) -> None:
        self.data = {sid: set(pids) for sid, pids in self.data.items()}
        for sid in self.capacities:
            self.data.setdefault(sid, set())
            self.reservations.setdefault(sid, {})
        for pid in self.weights:
            if pid not in self.ownership:
                raise ValueError(f"partition {pid} has no initial owner")

    @classmethod
    def from_initial(cls, cluster) -> "ClusterMetadata":
        capacities = {sid: slot.capacity for sid, slot in cluster.slots.items()}
        weights = {pid: p.weight for pid, p in cluster.partitions.items()}
        data: dict = {sid: set() for sid in cluster.slots}
        ownership = {}
        for pid, sid in cluster.current.items():
            data[sid].add(pid)
            ownership[pid] = sid
        missing = set(weights) - set(ownership)
        if missing:
            raise ValueError(f"partitions without initial placement: {sorted(missing)}")
        return cls(capacities=capacities, weights=weights, ownership=ownership,
                   owner_generation={pid: 0 for pid in weights}, data=data)

    def data_bytes(self, sid: str) -> int:
        return sum(self.weights[pid] for pid in self.data[sid])

    def reserved_bytes(self, sid: str) -> int:
        return sum(nbytes for nbytes, _ in self.reservations[sid].values())


class Executor:
    def __init__(self, meta: ClusterMetadata, budget_per_round: int):
        self.meta = meta
        self.budget_per_round = budget_per_round
        self.event_log: list = []

    # ---- observation ----------------------------------------------------

    def _log(self, step: Step, phase: Phase, outcome: str) -> None:
        self.event_log.append((step.plan_id, step.generation, step.round_index,
                               step.pid, phase.value, outcome))

    def handle_completion_ack(self, pid: str, generation: int) -> str:
        """Process a completion reply; old generations are refused."""
        if generation != self.meta.latest_generation:
            raise StaleGenerationError(
                f"ack for {pid} from generation {generation} rejected; "
                f"current generation is {self.meta.latest_generation}"
            )
        return "accepted"

    def consistency_report(self) -> dict:
        """Recompute all safety invariants from the metadata."""
        meta = self.meta
        errors = []
        # exactly one owner per partition, owner physically holds the data
        for pid, weight in meta.weights.items():
            owner = meta.ownership.get(pid)
            owners = [sid for sid, pids in meta.data.items() if pid in pids]
            if owner is None:
                errors.append(f"{pid}: no owner")
            elif pid not in meta.data.get(owner, ()):
                errors.append(f"{pid}: owner {owner} has no physical copy")
            if len([s for s in owners]) == 0:
                errors.append(f"{pid}: no physical copy anywhere")
        for sid in meta.capacities:
            used = meta.data_bytes(sid) + meta.reserved_bytes(sid)
            if used > meta.capacities[sid]:
                errors.append(
                    f"slot {sid}: used {used} exceeds capacity {meta.capacities[sid]}")
        for rnd, charged in meta.budget_ledger.items():
            if charged < 0:
                errors.append(f"round {rnd}: negative budget {charged}")
            if charged > self.budget_per_round:
                errors.append(
                    f"round {rnd}: budget {charged} > {self.budget_per_round}")
        # ledger must equal the sum of live reservation charges
        live: dict = {}
        for by_slot in meta.reservations.values():
            for _, (nbytes, rnd) in by_slot.items():
                live[rnd] = live.get(rnd, 0) + nbytes
        for rnd in set(live) | set(meta.budget_ledger):
            if live.get(rnd, 0) != meta.budget_ledger.get(rnd, 0):
                errors.append(
                    f"round {rnd}: ledger {meta.budget_ledger.get(rnd, 0)} != "
                    f"live reservations {live.get(rnd, 0)}")
        return {"ok": not errors, "errors": errors}

    # ---- phase primitives (each idempotent) -----------------------------

    def _reserve(self, step: Step) -> str:
        meta = self.meta
        key = (step.plan_id, step.pid)
        existing = meta.reservations[step.dst].get(key)
        if existing is not None:
            nbytes, rnd = existing
            if nbytes != step.nbytes or rnd != step.round_index:
                raise OwnershipConflict(f"conflicting reservation for {step.pid}")
            return "reserve-noop"
        if meta.data_bytes(step.dst) + meta.reserved_bytes(step.dst) + step.nbytes > \
                meta.capacities[step.dst]:
            raise CapacityExceeded(f"slot {step.dst} cannot reserve {step.nbytes}")
        charged = meta.budget_ledger.get(step.round_index, 0)
        if charged + step.nbytes > self.budget_per_round:
            raise CapacityExceeded(
                f"round {step.round_index} budget exceeded by {step.pid}")
        meta.reservations[step.dst][key] = (step.nbytes, step.round_index)
        meta.budget_ledger[step.round_index] = charged + step.nbytes
        return "reserved"

    def _copy(self, step: Step) -> str:
        meta = self.meta
        key = (step.plan_id, step.pid)
        if key not in meta.reservations[step.dst]:
            raise OwnershipConflict(
                f"cannot copy {step.pid}: missing reservation on {step.dst}")
        if step.pid in meta.data[step.dst]:
            return "copy-noop"
        meta.data[step.dst].add(step.pid)
        return "copied"

    def _switch(self, step: Step) -> str:
        meta = self.meta
        if meta.ownership.get(step.pid) == step.dst and \
                meta.owner_generation.get(step.pid) == step.generation:
            return "switch-noop"
        owner_gen = meta.owner_generation.get(step.pid, -1)
        if owner_gen > step.generation:
            raise StaleGenerationError(
                f"stale switch for {step.pid}: gen {step.generation} < {owner_gen}")
        if meta.ownership.get(step.pid) != step.src:
            raise OwnershipConflict(
                f"CAS failed for {step.pid}: expected owner {step.src}, "
                f"found {meta.ownership.get(step.pid)}")
        meta.ownership[step.pid] = step.dst
        meta.owner_generation[step.pid] = step.generation
        return "switched"

    def _release(self, step: Step) -> str:
        meta = self.meta
        outcome = []
        if step.pid in meta.data[step.src]:
            meta.data[step.src].discard(step.pid)
            outcome.append("source-copy-deleted")
        key = (step.plan_id, step.pid)
        if key in meta.reservations[step.dst]:
            del meta.reservations[step.dst][key]
            charged = meta.budget_ledger.get(step.round_index, 0) - step.nbytes
            if charged < 0:
                raise OwnershipConflict(f"budget ledger underflow for round "
                                        f"{step.round_index}")
            meta.budget_ledger[step.round_index] = charged
            outcome.append("reservation-released")
        return ",".join(outcome) if outcome else "release-noop"

    # ---- rollback -------------------------------------------------------

    def _rollback(self, step: Step, done_phases: list) -> None:
        """Undo phases in reverse order; RELEASE means fully committed."""
        meta = self.meta
        for phase in reversed(done_phases):
            if phase is Phase.RELEASE:
                continue  # step already committed; nothing in-flight to undo
            if phase is Phase.SWITCH:
                if meta.ownership.get(step.pid) == step.dst:
                    meta.ownership[step.pid] = step.src
                    meta.owner_generation[step.pid] = step.generation
                self._log(step, phase, "rolled-back-switch")
            elif phase is Phase.COPY:
                meta.data[step.dst].discard(step.pid)
                self._log(step, phase, "rolled-back-copy")
            elif phase is Phase.RESERVE:
                key = (step.plan_id, step.pid)
                if key in meta.reservations[step.dst]:
                    del meta.reservations[step.dst][key]
                    charged = meta.budget_ledger.get(step.round_index, 0) - step.nbytes
                    meta.budget_ledger[step.round_index] = max(charged, 0)
                self._log(step, phase, "rolled-back-reserve")

    # ---- execution ------------------------------------------------------

    def _already_done(self, step: Step) -> bool:
        return self.meta.ownership.get(step.pid) == step.dst and \
            self.meta.owner_generation.get(step.pid) == step.generation

    def execute(self, plan: Plan, fail_at=None):
        """Execute every round. fail_at = (pid, Phase) injects one fault."""
        if plan.budget_per_round != self.budget_per_round:
            raise ValueError("plan budget does not match executor budget")
        if plan.generation < self.meta.latest_generation:
            raise StaleGenerationError(
                f"plan generation {plan.generation} older than "
                f"{self.meta.latest_generation}")
        self.meta.latest_generation = plan.generation

        outcomes = []
        for rnd_index, move in plan.steps():
            step = Step(plan.plan_id, plan.generation, rnd_index,
                        move.pid, move.src, move.dst, move.nbytes)
            if self._already_done(step):
                self._log(step, Phase.RELEASE, "skipped-already-complete")
                outcomes.append((move, "skipped-already-complete"))
                continue
            done_phases: list = []
            try:
                for phase in PHASE_ORDER:
                    if fail_at == (move.pid, phase):
                        raise InjectedFault(f"injected failure at {phase.value}")
                    result = {
                        Phase.RESERVE: self._reserve,
                        Phase.COPY: self._copy,
                        Phase.SWITCH: self._switch,
                        Phase.RELEASE: self._release,
                    }[phase](step)
                    done_phases.append(phase)
                    self._log(step, phase, result)
                outcomes.append((move, "committed"))
            except InjectedFault as exc:
                self._rollback(step, done_phases)
                report = self.consistency_report()
                if not report["ok"]:
                    raise OwnershipConflict(
                        f"post-rollback invariant violation: {report['errors']}") from exc
                raise MigrationFailed(move, phase, exc) from exc
        return outcomes
