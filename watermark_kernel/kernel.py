from __future__ import annotations

from dataclasses import asdict, dataclass
from typing import Any, Callable

from .errors import (
    InvalidStateError,
    ProtocolViolationError,
    SimulatedProcessCrash,
    StaleEpochError,
    UnknownSourceError,
)
from .messages import DataMessage, Decision, ProgressMessage
from .model import ChildParticipant, HandoverState, SourceState
from .storage import WalLog, load_checkpoint, save_checkpoint

FaultHook = Callable[[str, dict], None]


@dataclass
class PendingProposal:
    source_id: str
    epoch: str
    floor: int


@dataclass
class EnqueueResult:
    kind: str
    event_time: int | None = None


class WatermarkKernel:
    def __init__(
        self,
        directory: str,
        *,
        checkpoint_every: int = 16,
        fault_hook: FaultHook | None = None,
        publisher: Callable[[Decision], None] | None = None,
    ):
        self.directory = directory
        self.checkpoint_every = checkpoint_every
        self.fault_hook = fault_hook
        self.publisher = publisher
        self._log = WalLog(directory)
        self.global_boundary = 0
        self.sources: dict[str, SourceState] = {}
        self.epoch_owner: dict[str, str] = {}
        self.used_epochs: set[str] = set()
        self.handovers: dict[str, HandoverState] = {}
        self.parent_handover: dict[str, str] = {}
        self.archived: dict[str, dict] = {}
        self.late: dict[str, list[dict]] = {}
        self.decision_count = 0
        self.unconfirmed_decisions: set[str] = set()
        self.stable_decisions: list[Decision] = []
        self.proposals: dict[str, PendingProposal] = {}
        self._events_since_checkpoint = 0
        self._replay_log()
        self._events_since_checkpoint = 0
        self._after_recovery()

    @classmethod
    def open(cls, directory: str, **kwargs: Any) -> "WatermarkKernel":
        return cls(directory, **kwargs)

    def _replay_log(self) -> None:
        checkpoint = load_checkpoint(self.directory)
        start = 0
        if checkpoint is not None:
            self._load_state(checkpoint["state"])
            start = checkpoint["log_position"]
            self._events_since_checkpoint = 0
        for _, event in self._log.records(start):
            self._apply_event(event)
            self._events_since_checkpoint += 1

    def checkpoint(self) -> int:
        position = self._log.position
        save_checkpoint(self.directory, self._state_dict(), position)
        self._events_since_checkpoint = 0
        return position

    def proposal(self, source_id: str, epoch: str) -> PendingProposal:
        self._require_new_source(source_id)
        if not self._epoch_free(epoch):
            raise ProtocolViolationError("epoch cannot be reused")
        proposal = PendingProposal(source_id, epoch, self.global_boundary)
        self.proposals[source_id] = proposal
        return proposal

    def accept_source(self, source_id: str, epoch: str, accepted_floor: int) -> None:
        if source_id in self.sources or source_id in self.archived:
            raise InvalidStateError("source already participates")
        proposal = self.proposals.pop(source_id, None)
        own_proposal = proposal is not None and proposal.epoch == epoch
        if not self._epoch_free(epoch) and not own_proposal:
            if proposal is not None:
                self.proposals[source_id] = proposal
            raise ProtocolViolationError("epoch cannot be reused")
        if proposal is not None and (proposal.epoch != epoch or proposal.floor != accepted_floor):
            raise ProtocolViolationError("source must accept current global boundary")
        if proposal is None and accepted_floor != self.global_boundary:
            raise ProtocolViolationError("source must accept current global boundary")
        event = {"type": "source_accepted", "source": source_id, "epoch": epoch, "floor": accepted_floor}
        self._mutate(event, refresh=source_id)

    def data(self, message: DataMessage) -> EnqueueResult:
        source = self._source_for_epoch(message.source, message.epoch)
        if not isinstance(message.seq, int) or message.seq < 1:
            raise ProtocolViolationError("seq must start at one")
        if not isinstance(message.event_time, int) or message.event_time < 0:
            raise ProtocolViolationError("event_time must be a non-negative integer")
        if message.seq <= source.rx_prefix or message.seq in source.pending_received:
            return EnqueueResult("duplicate")
        handover_id = self.parent_handover.get(source.source_id)
        handover = self.handovers.get(handover_id) if handover_id else None
        if handover is not None and handover.status == "pending":
            if message.seq > handover.terminal_cut:
                raise ProtocolViolationError("data cannot follow parent terminal cut")
            if message.event_time < handover.boundary_h:
                raise ProtocolViolationError("parent terminal data cannot predate H")
        if message.event_time < self.global_boundary:
            if any(event["epoch"] == message.epoch and event["seq"] == message.seq for event in self.late.get(source.source_id, [])):
                return EnqueueResult("duplicate")
            event = {
                "type": "late_data",
                "source": source.source_id,
                "epoch": message.epoch,
                "seq": message.seq,
                "event_time": message.event_time,
            }
            self._mutate(event)
            return EnqueueResult("late", message.event_time)
        event = {
            "type": "data",
            "source": source.source_id,
            "epoch": source.epoch,
            "seq": message.seq,
            "event_time": message.event_time,
        }
        self._mutate(event, refresh=source.source_id)
        return EnqueueResult("applied", message.event_time)

    def progress(self, message: ProgressMessage) -> None:
        source = self._source_for_epoch(message.source, message.epoch)
        if message.cut_seq < 0 or message.frontier < 0:
            raise ProtocolViolationError("cut and frontier must be non-negative")
        if message.frontier < source.floor:
            raise ProtocolViolationError("first frontier cannot be below accepted floor")
        handover_id = self.parent_handover.get(source.source_id)
        handover = self.handovers.get(handover_id) if handover_id else None
        if handover is not None and handover.status == "pending":
            if message.cut_seq > handover.terminal_cut:
                raise ProtocolViolationError("progress cannot follow parent terminal cut")
            if message.cut_seq == handover.terminal_cut and message.frontier < handover.boundary_h:
                raise ProtocolViolationError("terminal frontier must reach H")
        event = {
            "type": "progress",
            "source": source.source_id,
            "epoch": source.epoch,
            "cut_seq": message.cut_seq,
            "frontier": message.frontier,
        }
        self._mutate(event, refresh=source.source_id)

    def ack(self, source_id: str, epoch: str, seq: int) -> str:
        source = self._source_for_epoch(source_id, epoch)
        if seq <= source.ack_prefix:
            return "duplicate"
        if seq not in source.unacked:
            return "unknown"
        event = {"type": "ack", "source": source.source_id, "epoch": source.epoch, "seq": seq}
        self._mutate(event, refresh=source.source_id)
        return "applied"

    def mark_idle(self, source_id: str) -> None:
        source = self._require_source(source_id)
        if not source.active:
            return
        event = {"type": "source_idle", "source": source.source_id}
        self._mutate(event, refresh=source.source_id)

    def begin_handover(
        self,
        handover_id: str,
        parent_id: str,
        parent_epoch: str,
        boundary_h: int,
        terminal_cut: int,
        child_specs: list[tuple[str, str]],
    ) -> None:
        if handover_id in self.handovers:
            raise InvalidStateError("handover participant registration is not repeatable")
        if boundary_h < self.global_boundary:
            raise ProtocolViolationError("handover boundary cannot be below current global boundary")
        if terminal_cut < 0 or not child_specs:
            raise ProtocolViolationError("invalid handover")
        parent = self._source_for_epoch(parent_id, parent_epoch)
        if parent.blocked:
            raise InvalidStateError("parent must be active and currently unblocked")
        if parent.highest_rx_seq > terminal_cut or parent.highest_claim_cut > terminal_cut:
            raise ProtocolViolationError("parent already has input beyond terminal cut")
        seen: set[str] = set()
        children = []
        for child_id, child_epoch in child_specs:
            if child_id in seen or child_id in self.sources or child_id in self.archived:
                raise ProtocolViolationError("duplicate child participant")
            if not self._epoch_free(child_epoch):
                raise ProtocolViolationError("epoch cannot be reused")
            seen.add(child_id)
            children.append({"source": child_id, "epoch": child_epoch})
        event = {
            "type": "handover_begun",
            "handover_id": handover_id,
            "parent": parent_id,
            "parent_epoch": parent_epoch,
            "boundary_h": boundary_h,
            "terminal_cut": terminal_cut,
            "children": children,
        }
        parent.active = True
        self._mutate(event, refresh=parent_id)

    def child_progress(self, handover_id: str, message: ProgressMessage) -> None:
        handover, child = self._provisional_child(handover_id, message.source, message.epoch)
        if message.frontier < handover.boundary_h:
            raise ProtocolViolationError("child first frontier cannot be below H")
        if message.cut_seq < 0:
            raise ProtocolViolationError("cut must be non-negative")
        event = {
            "type": "child_progress",
            "handover_id": handover_id,
            "source": message.source,
            "epoch": message.epoch,
            "cut_seq": message.cut_seq,
            "frontier": message.frontier,
        }
        self._mutate(event)

    def child_data(self, handover_id: str, message: DataMessage) -> str:
        handover, child = self._provisional_child(handover_id, message.source, message.epoch)
        state = child.state
        if message.event_time < handover.boundary_h:
            raise ProtocolViolationError("child event cannot predate H")
        if message.seq < 1 or message.event_time < 0:
            raise ProtocolViolationError("invalid child data")
        if state is not None and (
            message.seq <= state.rx_prefix or message.seq in state.pending_received
        ):
            return "duplicate"
        event = {
            "type": "child_data",
            "handover_id": handover_id,
            "source": message.source,
            "epoch": message.epoch,
            "seq": message.seq,
            "event_time": message.event_time,
        }
        self._mutate(event)
        return "applied"

    def child_ack(self, handover_id: str, source_id: str, epoch: str, seq: int) -> str:
        _, child = self._provisional_child(handover_id, source_id, epoch)
        if child.state is None:
            return "unknown"
        state = child.state
        if seq <= state.ack_prefix:
            return "duplicate"
        if seq not in state.unacked:
            return "unknown"
        event = {
            "type": "child_ack",
            "handover_id": handover_id,
            "source": source_id,
            "epoch": epoch,
            "seq": seq,
        }
        self._mutate(event)
        return "applied"

    def confirm_child(self, handover_id: str, source_id: str, epoch: str) -> None:
        _, child = self._provisional_child(handover_id, source_id, epoch)
        if child.confirmed:
            return
        event = {
            "type": "child_confirmed",
            "handover_id": handover_id,
            "source": source_id,
            "epoch": epoch,
        }
        self._mutate(event)

    def cancel_handover(self, handover_id: str) -> None:
        handover = self.handovers.get(handover_id)
        if handover is None:
            raise UnknownSourceError("unknown handover")
        if handover.status != "pending":
            raise InvalidStateError("completed handover cannot be cancelled")
        event = {"type": "handover_cancelled", "handover_id": handover_id}
        self._mutate(event, refresh=handover.parent_id)

    def ack_decision(self, decision_id: str) -> None:
        if decision_id not in {decision.decision_id for decision in self.stable_decisions}:
            raise UnknownSourceError("unknown decision")
        if decision_id not in self.unconfirmed_decisions:
            return
        event = {"type": "decision_acked", "decision_id": decision_id}
        self._mutate(event)

    def recover_pending_decisions(self, publisher: Callable[[Decision], None]) -> None:
        for decision in self.stable_decisions:
            if decision.decision_id in self.unconfirmed_decisions:
                publisher(decision)

    @property
    def boundary(self) -> int:
        return self.global_boundary

    def pending_gap(self, source_id: str) -> list[int]:
        source = self._require_source(source_id)
        end = max(source.rx_prefix, source.highest_rx_seq, source.highest_claim_cut)
        return sorted(seq for seq in range(1, end + 1) if seq not in source.pending_received and seq > source.rx_prefix)

    def late_events(self, source_id: str) -> tuple[dict, ...]:
        return tuple(self.late.get(source_id, ()))

    def _fault(self, phase: str, event: dict) -> None:
        if self.fault_hook is not None:
            self.fault_hook(phase, event)

    def _require_source(self, source_id: str) -> SourceState:
        source = self.sources.get(source_id)
        if source is None:
            raise UnknownSourceError(f"unknown source: {source_id}")
        return source

    def _require_new_source(self, source_id: str) -> None:
        if source_id in self.sources or source_id in self.archived:
            raise InvalidStateError("source already exists")
        for handover in self.handovers.values():
            if handover.status == "pending" and source_id in handover.children:
                raise InvalidStateError("source already exists")

    def _epoch_free(self, epoch: str) -> bool:
        return epoch not in self.used_epochs and epoch not in {
            proposal.epoch for proposal in self.proposals.values()
        }

    def _source_for_epoch(self, source_id: str, epoch: str) -> SourceState:
        if source_id in self.archived:
            raise StaleEpochError("source has been archived by a completed handover")
        source = self.sources.get(source_id)
        if source is None:
            handover_id = self.parent_handover.get(source_id)
            if handover_id and self.handovers[handover_id].status == "pending":
                raise StaleEpochError("parent epoch is no longer accepted after handover completion")
            raise UnknownSourceError(f"unknown source: {source_id}")
        if source.epoch != epoch:
            raise StaleEpochError("old or mismatched epoch")
        owner = self.epoch_owner.get(epoch)
        if owner is not None and owner != source_id:
            raise StaleEpochError("epoch belongs to another participant")
        return source

    def _provisional_child(
        self, handover_id: str, source_id: str, epoch: str
    ) -> tuple[HandoverState, ChildParticipant]:
        handover = self.handovers.get(handover_id)
        if handover is None:
            raise UnknownSourceError("unknown handover")
        if handover.status != "pending":
            raise InvalidStateError("handover is not pending")
        child = handover.children.get(source_id)
        if child is None or child.epoch != epoch:
            raise StaleEpochError("message is not from a registered child epoch")
        return handover, child

    def _mutate(self, event: dict, *, refresh: str | None = None) -> None:
        self._log.append(event)
        self._fault("event_post_append", event)
        self._apply_event(event)
        self._after_input()
        self._events_since_checkpoint += 1
        self._maybe_checkpoint()

    def _after_input(self) -> None:
        completed = self._ready_handovers()
        for handover in completed:
            self._commit_handover(handover)
        self._settle_global_boundary()

    def _after_recovery(self) -> None:
        self._after_input()
        if self.publisher is not None:
            self.recover_pending_decisions(self.publisher)

    def _maybe_checkpoint(self) -> None:
        if self.checkpoint_every and self._events_since_checkpoint >= self.checkpoint_every:
            self.checkpoint()

    def _ready_handovers(self) -> list[HandoverState]:
        ready = []
        for handover in self.handovers.values():
            if handover.status != "pending":
                continue
            parent = self.sources.get(handover.parent_id)
            if parent is None or parent.epoch != handover.parent_epoch:
                continue
            if not all(child.confirmed for child in handover.children.values()):
                continue
            if parent.ack_prefix < handover.terminal_cut or parent.rx_prefix < handover.terminal_cut:
                continue
            if handover.terminal_cut > 0 and handover.terminal_frontier is None:
                continue
            if parent.watermark < handover.boundary_h or parent.unacked:
                continue
            ready.append(handover)
        return sorted(ready, key=lambda item: item.handover_id)

    def _commit_handover(self, handover: HandoverState) -> None:
        event = {"type": "handover_committed", "handover_id": handover.handover_id}
        self._fault("handover_pre_commit_record", event)
        self._log.append(event)
        self._fault("handover_post_commit_record", event)
        self._apply_event(event)
        self._events_since_checkpoint += 1
        self._settle_global_boundary()
        self._fault("handover_post_commit_state", event)
        self._maybe_checkpoint()

    def _settle_global_boundary(self) -> None:
        target = self._target_boundary()
        if target <= self.global_boundary:
            return
        self._publish_decision(target)

    def _target_boundary(self) -> int:
        candidates = []
        for source in self.sources.values():
            source.drain_claims()
            if source.blocked:
                return self.global_boundary
            if not source.active:
                continue
            candidates.append(source.watermark)
        if not candidates:
            return self.global_boundary
        return max(self.global_boundary, min(candidates))

    def _publish_decision(self, boundary: int) -> None:
        decision_number = self.decision_count + 1
        decision_id = f"decision-{decision_number:012d}"
        event = {"type": "decision_published", "decision_id": decision_id, "boundary": boundary}
        decision = Decision(decision_id, boundary)
        self._fault("decision_pre_append", event)
        self._log.append(event)
        self._fault("decision_post_append_pre_publish", event)
        self._apply_event(event)
        self._events_since_checkpoint += 1
        if self.publisher is not None:
            self.publisher(decision)
        self._fault("decision_post_publish_pre_ack", event)
        self._maybe_checkpoint()

    def _apply_event(self, event: dict) -> None:
        event_type = event["type"]
        method = getattr(self, f"_apply_{event_type}")
        method(event)

    def _apply_source_accepted(self, event: dict) -> None:
        source_id = event["source"]
        epoch = event["epoch"]
        floor = event["floor"]
        if source_id not in self.sources:
            self.sources[source_id] = SourceState(source_id, epoch, floor, floor)
            self.epoch_owner[epoch] = source_id
            self.used_epochs.add(epoch)

    def _apply_data(self, event: dict) -> None:
        source = self.sources[event["source"]]
        source.receive_data(event["seq"], event["event_time"])
        source.active = True

    def _apply_late_data(self, event: dict) -> None:
        self.late.setdefault(event["source"], []).append(
            {"epoch": event["epoch"], "seq": event["seq"], "event_time": event["event_time"]}
        )

    def _apply_progress(self, event: dict) -> None:
        source = self.sources[event["source"]]
        source.add_progress(event["cut_seq"], event["frontier"])
        source.active = True
        handover_id = self.parent_handover.get(source.source_id)
        if handover_id:
            handover = self.handovers[handover_id]
            if event["cut_seq"] == handover.terminal_cut:
                handover.terminal_frontier = max(
                    handover.terminal_frontier or 0, event["frontier"]
                )

    def _apply_ack(self, event: dict) -> None:
        self.sources[event["source"]].ack(event["seq"])

    def _apply_source_idle(self, event: dict) -> None:
        source = self.sources[event["source"]]
        source.active = False

    def _apply_handover_begun(self, event: dict) -> None:
        handover_id = event["handover_id"]
        if handover_id in self.handovers:
            return
        handover = HandoverState(
            handover_id=handover_id,
            parent_id=event["parent"],
            parent_epoch=event["parent_epoch"],
            boundary_h=event["boundary_h"],
            terminal_cut=event["terminal_cut"],
        )
        for child in event["children"]:
            handover.children[child["source"]] = ChildParticipant(
                source_id=child["source"], epoch=child["epoch"]
            )
            self.used_epochs.add(child["epoch"])
        self.handovers[handover_id] = handover
        self.parent_handover[event["parent"]] = handover_id
        parent = self.sources[event["parent"]]
        parent.active = True
        required_prefix = max(0, handover.terminal_cut)
        if parent.ack_prefix >= required_prefix and parent.watermark >= handover.boundary_h:
            handover.terminal_frontier = max(parent.watermark, handover.boundary_h)

    def _apply_child_progress(self, event: dict) -> None:
        handover = self.handovers[event["handover_id"]]
        child = handover.children[event["source"]]
        if child.state is None:
            child.state = SourceState(event["source"], event["epoch"], handover.boundary_h, handover.boundary_h)
        child.state.add_progress(event["cut_seq"], event["frontier"])

    def _apply_child_data(self, event: dict) -> None:
        handover = self.handovers[event["handover_id"]]
        child = handover.children[event["source"]]
        if child.state is None:
            child.state = SourceState(event["source"], event["epoch"], handover.boundary_h, handover.boundary_h)
        child.state.receive_data(event["seq"], event["event_time"])

    def _apply_child_ack(self, event: dict) -> None:
        handover = self.handovers[event["handover_id"]]
        handover.children[event["source"]].state.ack(event["seq"])

    def _apply_child_confirmed(self, event: dict) -> None:
        handover = self.handovers[event["handover_id"]]
        handover.children[event["source"]].confirmed = True

    def _apply_handover_cancelled(self, event: dict) -> None:
        handover = self.handovers[event["handover_id"]]
        handover.status = "cancelled"
        self.parent_handover.pop(handover.parent_id, None)

    def _apply_handover_committed(self, event: dict) -> None:
        handover = self.handovers[event["handover_id"]]
        if handover.status == "committed":
            return
        parent = self.sources.pop(handover.parent_id)
        self.archived[handover.parent_id] = {"epoch": handover.parent_epoch, "handover_id": handover.handover_id}
        self.parent_handover.pop(handover.parent_id, None)
        for child in handover.children.values():
            state = child.state
            if state is None:
                state = SourceState(child.source_id, child.epoch, handover.boundary_h, handover.boundary_h)
            state.source_id = child.source_id
            state.epoch = child.epoch
            state.floor = handover.boundary_h
            state.watermark = max(state.watermark, handover.boundary_h)
            self.sources[child.source_id] = state
            self.epoch_owner[child.epoch] = child.source_id
        handover.status = "committed"

    def _apply_decision_published(self, event: dict) -> None:
        decision = Decision(event["decision_id"], event["boundary"])
        number = int(event["decision_id"].split("-")[1])
        if number <= self.decision_count:
            return
        self.decision_count = number
        self.global_boundary = max(self.global_boundary, event["boundary"])
        self.stable_decisions.append(decision)
        self.unconfirmed_decisions.add(event["decision_id"])

    def _apply_decision_acked(self, event: dict) -> None:
        self.unconfirmed_decisions.discard(event["decision_id"])

    def _state_dict(self) -> dict:
        return {
            "global_boundary": self.global_boundary,
            "sources": [source.to_dict() for source in self.sources.values()],
            "epoch_owner": self.epoch_owner,
            "used_epochs": sorted(self.used_epochs),
            "handovers": [handover.to_dict() for handover in self.handovers.values()],
            "parent_handover": self.parent_handover,
            "archived": self.archived,
            "late": self.late,
            "decision_count": self.decision_count,
            "unconfirmed_decisions": sorted(self.unconfirmed_decisions),
            "stable_decisions": [asdict(decision) for decision in self.stable_decisions],
        }

    def _load_state(self, state: dict) -> None:
        self.global_boundary = state["global_boundary"]
        self.sources = {
            source_data["source_id"]: SourceState.from_dict(source_data)
            for source_data in state["sources"]
        }
        self.epoch_owner = dict(state["epoch_owner"])
        self.used_epochs = set(state.get("used_epochs", self.epoch_owner))
        self.handovers = {
            handover_data["handover_id"]: HandoverState.from_dict(handover_data)
            for handover_data in state["handovers"]
        }
        self.parent_handover = dict(state["parent_handover"])
        self.archived = dict(state["archived"])
        self.late = {key: list(value) for key, value in state.get("late", {}).items()}
        self.decision_count = state["decision_count"]
        self.unconfirmed_decisions = set(state["unconfirmed_decisions"])
        self.stable_decisions = [Decision(**decision) for decision in state["stable_decisions"]]
        self.proposals = {}
