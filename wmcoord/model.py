"""Data model: messages, commands, results, and exceptions.

Terminology
-----------
* Data messages describe an observed event with a continuous ``seq`` and an
  event time ``ts``.
* Progress messages declare ``(frontier, cut_seq)``: no record with event time
  below ``frontier`` will ever be produced with ``seq > cut_seq``.  The
  frontier is an *exclusive* boundary: data with ``ts == frontier`` is still
  permitted.
* A command is what a local driver submits to the coordinator kernel.  Every
  accepted command is durably journaled before it becomes effective.

Only Python 3.11 standard library features are used.
"""
from __future__ import annotations

from dataclasses import dataclass


class ProtocolError(Exception):
    """Raised when a command violates the coordination protocol.

    Protocol errors reject the command entirely; the journal is not modified.
    """


@dataclass(frozen=True)
class DataMessage:
    source: str
    epoch: int
    seq: int
    ts: int

    def validate(self) -> None:
        if not isinstance(self.source, str) or not self.source:
            raise ProtocolError("data message requires a non-empty source")
        if not isinstance(self.epoch, int) or self.epoch < 1:
            raise ProtocolError("epoch must be a positive integer")
        if not isinstance(self.seq, int) or self.seq < 1:
            raise ProtocolError("seq must start at 1")
        if not isinstance(self.ts, int) or self.ts < 0:
            raise ProtocolError("event time must be a non-negative integer")


@dataclass(frozen=True)
class ProgressMessage:
    source: str
    epoch: int
    frontier: int
    cut_seq: int

    def validate(self) -> None:
        if not isinstance(self.source, str) or not self.source:
            raise ProtocolError("progress requires a non-empty source")
        if not isinstance(self.epoch, int) or self.epoch < 1:
            raise ProtocolError("epoch must be a positive integer")
        if not isinstance(self.frontier, int) or self.frontier < 0:
            raise ProtocolError("frontier must be a non-negative integer")
        if not isinstance(self.cut_seq, int) or self.cut_seq < 0:
            raise ProtocolError("cut_seq must be a non-negative integer")


# ---------------------------------------------------------------------------
# Commands.  ``kind`` is a stable string so records survive serialization.
# ---------------------------------------------------------------------------


@dataclass(frozen=True)
class RegisterSource:
    source: str
    kind: str = "register_source"


@dataclass(frozen=True)
class Data:
    msg: DataMessage
    kind: str = "data"


@dataclass(frozen=True)
class Ack:
    source: str
    epoch: int
    seq: int
    kind: str = "ack"


@dataclass(frozen=True)
class Progress:
    msg: ProgressMessage
    kind: str = "progress"


@dataclass(frozen=True)
class IdleTimeout:
    source: str
    kind: str = "idle_timeout"


@dataclass(frozen=True)
class JoinSource:
    """A newly appearing source explicitly accepts the current global floor."""

    source: str
    accepted_floor: int
    kind: str = "join_source"


@dataclass(frozen=True)
class BeginHandoff:
    parent: str
    handoff_id: str
    boundary: int
    children: tuple[str, ...] = ()
    kind: str = "begin_handoff"


@dataclass(frozen=True)
class ChildTakeover:
    handoff_id: str
    child: str
    epoch: int
    frontier: int
    kind: str = "child_takeover"


@dataclass(frozen=True)
class CancelHandoff:
    handoff_id: str
    kind: str = "cancel_handoff"


@dataclass(frozen=True)
class PublishDecision:
    decision_id: str
    boundary: int
    kind: str = "publish_decision"


@dataclass(frozen=True)
class ConfirmDecision:
    decision_id: str
    kind: str = "confirm_decision"


Command = (
    RegisterSource
    | Data
    | Ack
    | Progress
    | IdleTimeout
    | JoinSource
    | BeginHandoff
    | ChildTakeover
    | CancelHandoff
    | PublishDecision
    | ConfirmDecision
)


def command_to_dict(command: Command) -> dict:
    """Serialize a command to plain JSON-safe data."""
    if isinstance(command, (Data, Progress)):
        payload = vars(command)
        out = {"kind": command.kind}
        msg = command.msg
        out["msg"] = {k: v for k, v in vars(msg).items()}
        return out
    return {k: v for k, v in vars(command).items()}


def command_from_dict(data: dict) -> Command:
    """Rebuild a command from serialized data."""
    kind = data["kind"]
    if kind == "register_source":
        return RegisterSource(source=data["source"])
    if kind == "data":
        return Data(DataMessage(**data["msg"]))
    if kind == "ack":
        return Ack(source=data["source"], epoch=data["epoch"], seq=data["seq"])
    if kind == "progress":
        return Progress(ProgressMessage(**data["msg"]))
    if kind == "idle_timeout":
        return IdleTimeout(source=data["source"])
    if kind == "join_source":
        return JoinSource(source=data["source"], accepted_floor=data["accepted_floor"])
    if kind == "begin_handoff":
        return BeginHandoff(
            parent=data["parent"],
            handoff_id=data["handoff_id"],
            boundary=data["boundary"],
            children=tuple(data.get("children", ())),
        )
    if kind == "child_takeover":
        return ChildTakeover(
            handoff_id=data["handoff_id"],
            child=data["child"],
            epoch=data["epoch"],
            frontier=data["frontier"],
        )
    if kind == "cancel_handoff":
        return CancelHandoff(handoff_id=data["handoff_id"])
    if kind == "publish_decision":
        return PublishDecision(
            decision_id=data["decision_id"], boundary=data["boundary"]
        )
    if kind == "confirm_decision":
        return ConfirmDecision(decision_id=data["decision_id"])
    raise ProtocolError(f"unknown command kind: {kind!r}")


@dataclass(frozen=True)
class SubmitResult:
    """Outcome of applying one input command.

    Attributes:
        disposition: one of ``accepted`` / ``ignored`` / ``late``.
        new_boundary: the global boundary immediately after this command, or
            ``None`` if the command is a delivery/control command whose own
            handling does not carry a boundary snapshot.
        decision_id / decision_boundary: set when the command durably sealed a
            new global boundary.
        handoff_completed: set when a takeover command completed a handoff.
    """

    disposition: str
    new_boundary: int | None = None
    decision_id: str | None = None
    decision_boundary: int | None = None
    handoff_completed: str | None = None
    detail: str = ""

    @property
    def sealed(self) -> bool:
        return self.decision_id is not None
