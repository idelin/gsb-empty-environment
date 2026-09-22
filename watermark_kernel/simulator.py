from __future__ import annotations

from dataclasses import dataclass, field
from heapq import heappop, heappush
from itertools import count
from typing import Any, Callable

from .errors import SimulatedProcessCrash
from .kernel import WatermarkKernel
from .messages import DataMessage, Decision, ProgressMessage


@dataclass(frozen=True)
class Message:
    kind: str
    payload: Any


@dataclass
class DownstreamNode:
    received: list[Decision] = field(default_factory=list)
    seen: set[str] = field(default_factory=set)

    def receive(self, decision: Decision) -> str:
        if decision.decision_id in self.seen:
            return "duplicate"
        self.seen.add(decision.decision_id)
        self.received.append(decision)
        return "applied"


@dataclass
class CoordinatorNode:
    directory: str
    downstream: DownstreamNode
    fault_hook: Callable[[str, dict], None] | None = None
    checkpoint_every: int = 1
    kernel: WatermarkKernel | None = None
    restart_count: int = 0
    redelivered: list[Decision] = field(default_factory=list)

    def start(self, simulation: "Simulation") -> None:
        self._open_kernel()

    def _open_kernel(self) -> None:
        self.kernel = WatermarkKernel.open(
            self.directory,
            checkpoint_every=self.checkpoint_every,
            fault_hook=self.fault_hook,
            publisher=self._publish,
        )

    def _publish(self, decision: Decision) -> None:
        result = self.downstream.receive(decision)
        if result == "duplicate":
            self.redelivered.append(decision)

    def crash(self) -> None:
        self.kernel = None

    def restart(self, simulation: "Simulation") -> None:
        self.restart_count += 1
        self._open_kernel()

    def handle(self, simulation: "Simulation", message: Message) -> None:
        if message.kind == "restart":
            self.restart(simulation)
            return
        if self.kernel is None:
            simulation.schedule(0, self, Message("restart", None), priority=-1)
            simulation.schedule(0, self, message, priority=1)
            return
        kind = message.kind
        payload = message.payload
        try:
            if kind == "proposal":
                self.kernel.proposal(**payload)
            elif kind == "accept_source":
                self.kernel.accept_source(**payload)
            elif kind == "data":
                self.kernel.data(DataMessage(**payload))
            elif kind == "progress":
                self.kernel.progress(ProgressMessage(**payload))
            elif kind == "ack":
                self.kernel.ack(**payload)
            elif kind == "idle":
                self.kernel.mark_idle(**payload)
            elif kind == "begin_handover":
                self.kernel.begin_handover(**payload)
            elif kind == "child_data":
                self.kernel.child_data(payload["handover_id"], DataMessage(**payload["message"]))
            elif kind == "child_progress":
                self.kernel.child_progress(payload["handover_id"], ProgressMessage(**payload["message"]))
            elif kind == "child_ack":
                self.kernel.child_ack(**payload)
            elif kind == "confirm_child":
                self.kernel.confirm_child(**payload)
            elif kind == "cancel_handover":
                self.kernel.cancel_handover(**payload)
            elif kind == "ack_decision":
                self.kernel.ack_decision(**payload)
            elif kind == "checkpoint":
                self.kernel.checkpoint()
            else:
                raise ValueError(f"unknown command: {kind}")
        except SimulatedProcessCrash:
            self.crash()
            simulation.schedule(0, self, Message("restart", None), priority=-1)
            simulation.schedule(0, self, message)


class Simulation:
    def __init__(self) -> None:
        self.now = 0
        self._queue: list[tuple[int, int, int, CoordinatorNode, Message]] = []
        self._sequence = count()

    def schedule(
        self,
        delay: int,
        target: CoordinatorNode,
        message: Message,
        *,
        priority: int = 0,
    ) -> None:
        heappush(self._queue, (self.now + delay, priority, next(self._sequence), target, message))

    def run(self, ticks: int = 10_000) -> None:
        steps = 0
        while self._queue and steps < ticks:
            self.now, _, _, target, message = heappop(self._queue)
            try:
                target.handle(self, message)
            except SimulatedProcessCrash:
                target.crash()
                self.schedule(0, target, Message("restart", None), priority=-1)
                if message.kind != "restart":
                    self.schedule(0, target, message, priority=1)
            steps += 1

    def send(
        self,
        target: CoordinatorNode,
        kind: str,
        payload: Any,
        *,
        delay: int = 1,
    ) -> None:
        self.schedule(delay, target, Message(kind, payload))

    def start_coordinator(self, target: CoordinatorNode) -> None:
        target.start(self)
