"""Local discrete-event simulator.

Everything the kernel interacts with lives here: nodes (sources, processor,
coordinator, downstream), messages, the virtual clock and faults.  No real
I/O, no randomness unless seeded by the caller; runs are deterministic, so a
fault-injection run can be compared exactly against a fault-free run.

Event ordering is deterministic: events are popped by ``(time, seq)`` where
``seq`` is the insertion order.
"""
from __future__ import annotations

import heapq
from dataclasses import dataclass, field
from typing import Any, Callable

from .journal import BaseLog
from .kernel import CrashNow, Kernel
from .model import Command, SubmitResult


class Simulator:
    def __init__(self) -> None:
        self.time = 0
        self._queue: list[tuple[int, int, Callable[[], None]]] = []
        self._seq = 0
        self.nodes: dict[str, Any] = {}

    def schedule(self, delay: int, action: Callable[[], None]) -> None:
        self.schedule_at(self.time + delay, action)

    def schedule_at(self, time: int, action: Callable[[], None]) -> None:
        self._seq += 1
        heapq.heappush(self._queue, (time, self._seq, action))

    def send(self, src: str, dst: str, payload: Any, delay: int = 1) -> None:
        def deliver() -> None:
            node = self.nodes.get(dst)
            if node is not None:
                node.receive(src, payload)

        self.schedule(delay, deliver)

    def run(self, until: int | None = None) -> None:
        while self._queue:
            time, _, action = self._queue[0]
            if until is not None and time > until:
                break
            heapq.heappop(self._queue)
            self.time = max(self.time, time)
            action()

    # ------------------------------------------------------------- faults

    def crash(self, node: str) -> None:
        victim = self.nodes[node]
        victim.crash()

    def restart(self, node: str) -> None:
        victim = self.nodes[node]
        victim.restart()


# ---------------------------------------------------------------------------
# Coordinator node: owns the kernel, publishes durable decisions downstream.
# ---------------------------------------------------------------------------


class CoordinatorNode:
    """Wraps a :class:`Kernel` with crash/restart and publish plumbing.

    Crash hooks are armed by name; when the kernel reaches an armed point it
    raises :class:`CrashNow`, which the node converts into process death
    (in-memory kernel discarded).  ``restart`` reopens the kernel from the
    same log and re-publishes every unconfirmed decision with its stable ID.
    """

    def __init__(
        self,
        sim: Simulator,
        log_factory: Callable[[], BaseLog],
        checkpoint_path: str | None = None,
        name: str = "coordinator",
    ) -> None:
        self.sim = sim
        self.name = name
        self._log_factory = log_factory
        self.checkpoint_path = checkpoint_path
        self.armed: set[str] = set()
        self.alive = False
        self.kernel: Kernel | None = None
        self.log: BaseLog | None = None
        self.received: list[SubmitResult] = []
        sim.nodes[name] = self
        self.restart()

    # -- lifecycle ---------------------------------------------------------

    def _hook(self, point: str) -> None:
        if point in self.armed:
            self.armed.discard(point)
            raise CrashNow()

    def restart(self) -> None:
        self.log = self._log_factory()
        self.kernel = Kernel.open(
            self.log, self.checkpoint_path, crash_hook=self._hook
        )
        self.alive = True
        # Re-publish every decision that is durable but unconfirmed, with the
        # same stable ID (downstream de-duplicates).
        for did, boundary, _status in self.kernel.pending_decisions():
            self._publish(did, boundary)

    def crash(self) -> None:
        self.alive = False
        self.kernel = None
        self.log = None

    # -- messaging ---------------------------------------------------------

    def receive(self, src: str, payload: Any) -> None:
        if not self.alive:
            return  # message lost while the process is down
        kind = payload[0]
        if kind == "command":
            self.submit(payload[1])
        elif kind == "confirm":
            self.kernel.confirm(payload[1])
        else:  # pragma: no cover
            raise ValueError(f"unknown payload {payload!r}")

    def submit(self, command: Command) -> SubmitResult | None:
        if not self.alive:
            return None
        try:
            result = self.kernel.submit(command)
        except CrashNow:
            self.crash()
            return None
        self.received.append(result)
        if result.decision_id is not None:
            self._publish(result.decision_id, result.decision_boundary)
        return result

    def _publish(self, decision_id: str, boundary: int) -> None:
        try:
            self.kernel.publish(decision_id, boundary)
        except CrashNow:
            self.crash()
            return
        self.sim.send(
            self.name, "downstream", ("decision", decision_id, boundary)
        )


# ---------------------------------------------------------------------------
# Downstream: de-duplicates closure decisions by stable ID and confirms them.
# ---------------------------------------------------------------------------


class DownstreamNode:
    def __init__(self, sim: Simulator, name: str = "downstream") -> None:
        self.sim = sim
        self.name = name
        self.seen: set[str] = set()
        self.closed: list[tuple[str, int]] = []  # deduped closure sequence
        self.duplicates = 0
        sim.nodes[name] = self

    def receive(self, src: str, payload: Any) -> None:
        kind, did, boundary = payload
        assert kind == "decision"
        if did in self.seen:
            self.duplicates += 1
        else:
            self.seen.add(did)
            self.closed.append((did, boundary))
        self.sim.send(self.name, src, ("confirm", did))


# ---------------------------------------------------------------------------
# Source / processor nodes: scripted message injection.
# ---------------------------------------------------------------------------


class Driver:
    """Convenience script runner: inject commands at virtual times."""

    def __init__(self, sim: Simulator, coordinator: str = "coordinator") -> None:
        self.sim = sim
        self.coordinator = coordinator

    def at(self, time: int, command: Command) -> None:
        def inject() -> None:
            self.sim.send("driver", self.coordinator, ("command", command), 0)

        self.sim.schedule_at(time, inject)

    def crash_at(self, time: int, node: str = "coordinator") -> None:
        self.sim.schedule_at(time, lambda: self.sim.crash(node))

    def restart_at(self, time: int, node: str = "coordinator") -> None:
        self.sim.schedule_at(time, lambda: self.sim.restart(node))

    def arm_at(self, time: int, point: str, node: str = "coordinator") -> None:
        def arm() -> None:
            self.sim.nodes[node].armed.add(point)

        self.sim.schedule_at(time, arm)
