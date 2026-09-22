"""Pure coordination state machine.

The engine has no I/O: it takes the current state plus a command and returns
the new state together with the durable *effects* produced by the command
(``DECISION`` / ``HANDOFF_DONE``).  The kernel layer wraps it with journaling,
publishing and recovery.

Effective watermark rule per source (epoch ``e``)
-------------------------------------------------
Let ``cut``/``frontier`` be the newest progress promise.  The effective water
of the source can only reach ``frontier`` once **every** ``seq`` in
``1..cut`` has both arrived and been explicitly acknowledged.  Otherwise the
source stays at its previous water (it never moves to event times of
in-flight records).  Progress may arrive before the data it covers;
duplicates and reordering cannot skip a missing seq.
"""
from __future__ import annotations

from dataclasses import dataclass, field

from .model import (
    Ack,
    BeginHandoff,
    CancelHandoff,
    ChildTakeover,
    Command,
    ConfirmDecision,
    Data,
    DataMessage,
    IdleTimeout,
    JoinSource,
    Progress,
    ProgressMessage,
    ProtocolError,
    PublishDecision,
    RegisterSource,
    SubmitResult,
)

DECISION_PREFIX = "D"


def decision_id(seq: int) -> str:
    return f"{DECISION_PREFIX}{seq:08d}"


def new_state() -> dict:
    """Create an empty coordination state (initial global boundary is 0)."""
    return {
        "boundary": 0,
        "sources": {},          # name -> SourceState (dict)
        "handoffs": {},         # id -> HandoffState (dict)
        "parent_of": {},        # handoff id -> parent (active handoffs)
        "decisions": {},        # decision id -> {"boundary": int, "status": str}
        "decision_seq": 0,
        "late": [],             # late channel: (reason, message-as-dict)
    }


def _epoch_state(epoch: int, frontier: int) -> dict:
    return {
        "epoch": epoch,
        "frontier": frontier,
        "cut": 0,
        # Contiguous prefix counters: seqs 1..arrived_prefix are known to have
        # arrived (and likewise for acks).  Sets hold out-of-order seqs beyond
        # the prefix.  This makes each arrival/ack amortized O(1).
        "arrived_prefix": 0,
        "acked_prefix": 0,
        "arrived": set(),       # arrived seqs past arrived_prefix
        "acked": set(),         # acked seqs past acked_prefix
        "early_acks": set(),    # acks that landed before their data
        "ts": {},               # seq -> event time (kept while pending)
        "water": None,
    }


def _source_state(epoch: int | None = None, floor: int = 0) -> dict:
    return {
        "status": "active",             # active | idle | exited
        "epochs": set(),                # every epoch ever used (no reuse)
        "current": _epoch_state(epoch, 0) if epoch is not None else None,
        "floor": floor,                 # accepted join floor (>= global)
        "water": floor,                 # monotone effective water
        "idle": False,
    }


def _ensure_epoch(source: dict, epoch: int) -> dict:
    """Get the current runtime for ``epoch`` or open a new epoch.

    Epochs may never be reused.  Opening a new epoch seals the old one; its
    contribution is folded into the source's monotone ``water`` immediately.
    """
    if epoch in source["epochs"]:
        cur = source["current"]
        if cur is not None and cur["epoch"] == epoch:
            return cur
        raise ProtocolError(f"epoch {epoch} cannot be reused")
    # Strictly increasing epoch numbers (defense against accidental reuse).
    if source["epochs"] and epoch < max(source["epochs"]):
        raise ProtocolError(f"epoch {epoch} is older than a used epoch")
    cur = source["current"]
    if cur is not None and _outstanding(source):
        raise ProtocolError(
            "cannot open a new epoch while the current cut has a gap or "
            "unconfirmed data"
        )
    if cur is not None:
        _fold_water(source)
    runtime = _epoch_state(epoch, 0)
    source["current"] = runtime
    source["epochs"].add(epoch)
    source["idle"] = False
    return runtime


def _fold_water(source: dict) -> None:
    """Fold a sealed epoch's water into the source-level monotone water."""
    cur = source["current"]
    if cur is not None and cur["water"] is not None:
        if cur["water"] > source["water"]:
            source["water"] = cur["water"]


def _extend_prefix(runtime: dict, which: str) -> None:
    """Advance a contiguous prefix over the matching out-of-order set.

    ``which`` is ``arrived`` or ``acked``.  Each seq leaves the set once, so
    total work across the life of an epoch is linear in the message count.
    """
    prefix_key = f"{which}_prefix"
    nxt = runtime[prefix_key] + 1
    while nxt in runtime[which]:
        runtime[which].discard(nxt)
        runtime[prefix_key] = nxt
        nxt += 1


def _epoch_complete(runtime: dict) -> bool:
    """True iff seqs 1..cut all arrived and are acknowledged."""
    if runtime["cut"] == 0:
        return True
    return (
        runtime["arrived_prefix"] >= runtime["cut"]
        and runtime["acked_prefix"] >= runtime["cut"]
    )


def _recompute_source_water(source: dict) -> bool:
    """Advance current epoch water to frontier when its cut is fully acked.

    Returns whether the source-level effective water changed.
    """
    runtime = source["current"]
    if runtime is None:
        return False
    if _epoch_complete(runtime):
        floor = runtime["water"] if runtime["water"] is not None else 0
        if runtime["frontier"] > floor:
            runtime["water"] = runtime["frontier"]
    if runtime["water"] is not None and runtime["water"] > source["water"]:
        source["water"] = runtime["water"]
        return True
    return False


def _participating(state: dict) -> list[tuple[str, int]]:
    """Sources currently participating in the global minimum.

    Rules:
    * exited parents never participate;
    * an active source always participates (no effective water yet counts as
      0);
    * an idle source participates **only while it still blocks**, i.e. it
      carries un-acked data or a gap inside a declared cut.  An idle source
      with a clean water simply leaves the minimum temporarily;
    * child sources reserved by a not-yet-completed handoff do not participate
      (the parent is still present until completion).
    """
    out: list[tuple[str, int]] = []
    for name, source in state["sources"].items():
        if source["status"] == "exited":
            continue
        if source.get("reserved"):
            continue
        if source["idle"] and not _outstanding(source):
            continue
        out.append((name, source["water"]))
    return out


def _outstanding(source: dict) -> bool:
    """True when the source still has data/gaps blocking closure.

    Blockers are: a declared cut whose prefix has not fully arrived (a gap),
    or any arrived but unacknowledged record (including records beyond the
    declared cut).  Idle timeout cannot remove either.
    """
    runtime = source["current"]
    if runtime is None:
        return False
    if runtime["arrived_prefix"] < runtime["cut"]:
        return True
    if runtime["arrived"] or runtime["early_acks"]:
        return True
    if runtime["acked_prefix"] < runtime["arrived_prefix"]:
        return True
    return False


def _global_minimum(state: dict) -> int | None:
    participants = _participating(state)
    if not participants:
        return None
    return min(water for _, water in participants)


@dataclass
class Applied:
    """Result of applying a command to the state (pure)."""

    disposition: str = "accepted"
    effects: list[tuple[str, dict]] = field(default_factory=list)
    new_boundary: int | None = None
    decision_id: str | None = None
    decision_boundary: int | None = None
    handoff_completed: str | None = None
    detail: str = ""

    def to_result(self) -> SubmitResult:
        return SubmitResult(
            disposition=self.disposition,
            new_boundary=self.new_boundary,
            decision_id=self.decision_id,
            decision_boundary=self.decision_boundary,
            handoff_completed=self.handoff_completed,
            detail=self.detail,
        )


def _record_late(state: dict, reason: str, message: dict) -> None:
    state["late"].append((reason, message))


def _recompute_global(state: dict, applied: Applied) -> None:
    """Monotone global boundary; seal (emit DECISION) when it rises."""
    candidate = _global_minimum(state)
    if candidate is not None and candidate > state["boundary"]:
        state["boundary"] = candidate
        state["decision_seq"] += 1
        did = decision_id(state["decision_seq"])
        state["decisions"][did] = {
            "boundary": candidate,
            "status": "durable",
        }
        applied.decision_id = did
        applied.decision_boundary = candidate
        applied.new_boundary = candidate
        applied.effects.append(
            ("DECISION", {"id": did, "boundary": candidate})
        )
    elif candidate is not None:
        applied.new_boundary = state["boundary"]


def apply(state: dict, command: Command) -> Applied:
    """Apply one command; mutate state; return durable effects/meta."""
    if isinstance(command, RegisterSource):
        return _apply_register(state, command)
    if isinstance(command, Data):
        return _apply_data(state, command)
    if isinstance(command, Ack):
        return _apply_ack(state, command)
    if isinstance(command, Progress):
        return _apply_progress(state, command)
    if isinstance(command, IdleTimeout):
        return _apply_idle(state, command)
    if isinstance(command, JoinSource):
        return _apply_join(state, command)
    if isinstance(command, BeginHandoff):
        return _apply_begin_handoff(state, command)
    if isinstance(command, ChildTakeover):
        return _apply_takeover(state, command)
    if isinstance(command, CancelHandoff):
        return _apply_cancel_handoff(state, command)
    if isinstance(command, PublishDecision):
        return _apply_publish(state, command)
    if isinstance(command, ConfirmDecision):
        return _apply_confirm(state, command)
    raise ProtocolError(f"unknown command: {command!r}")


def _apply_register(state: dict, command: RegisterSource) -> Applied:
    """Bootstrap registration of a participant at the initial floor 0."""
    if command.source in state["sources"]:
        raise ProtocolError(f"source {command.source!r} already registered")
    state["sources"][command.source] = _source_state()
    return Applied(disposition="accepted", new_boundary=state["boundary"])


def _apply_join(state: dict, command: JoinSource) -> Applied:
    """New source joins after explicitly accepting the current boundary."""
    if command.source in state["sources"]:
        raise ProtocolError(f"source {command.source!r} already exists")
    floor = state["boundary"]
    if command.accepted_floor != floor:
        raise ProtocolError(
            f"source must explicitly accept current boundary {floor}, "
            f"got {command.accepted_floor}"
        )
    state["sources"][command.source] = _source_state(floor=floor)
    return Applied(disposition="accepted", new_boundary=floor)


def _route_data(
    state: dict, source_name: str, msg: DataMessage
) -> tuple[str, str]:
    """Classify a data message: (disposition, detail).

    ``late`` means the data can never alter a closed result; ``ignored``
    covers harmless duplicates.  Mutates the epoch index for accepted data.
    """
    source = state["sources"].get(source_name)
    if source is None:
        raise ProtocolError(f"data from unknown source {source_name!r}")
    if source["status"] == "exited":
        _record_late(
            state,
            "exited_source",
            vars(msg),
        )
        return "late", "source exited after completed handoff"
    if source.get("reserved"):
        raise ProtocolError(
            "child cannot receive data before handoff completion"
        )
    if msg.epoch in source["epochs"] and (
        source["current"] is None or source["current"]["epoch"] != msg.epoch
    ):
        _record_late(state, "old_epoch", vars(msg))
        return "late", "message from a superseded epoch"
    runtime = _ensure_epoch(source, msg.epoch)
    seq = msg.seq
    already = (
        seq <= runtime["arrived_prefix"]
        or seq in runtime["arrived"]
    )
    if already:
        known_ts = runtime["ts"].get(seq)
        if known_ts is not None and known_ts != msg.ts:
            raise ProtocolError(
                f"duplicate seq {seq} with conflicting event time"
            )
        return "ignored", "duplicate"
    closed = max(source["water"], state["boundary"])
    if closed > msg.ts:
        # Below the source's own promise or below the globally closed
        # boundary (e.g. the source was idle-excluded when the boundary
        # moved past its water): late, closed results are unaffected.
        _record_late(state, "below_closed_boundary", vars(msg))
        return "late", f"event time {msg.ts} below closed boundary {closed}"
    runtime["arrived"].add(seq)
    runtime["ts"][seq] = msg.ts
    _extend_prefix(runtime, "arrived")
    if seq in runtime["early_acks"]:
        runtime["early_acks"].discard(seq)
        runtime["acked"].add(seq)
        _extend_prefix(runtime, "acked")
    source["idle"] = False
    return "accepted", ""


def _apply_data(state: dict, command: Data) -> Applied:
    msg = command.msg
    msg.validate()
    disposition, detail = _route_data(state, msg.source, msg)
    applied = Applied(disposition=disposition, detail=detail)
    if disposition == "accepted":
        _recompute_source_water(state["sources"][msg.source])
        _recompute_global(state, applied)
    return applied


def _apply_ack(state: dict, command: Ack) -> Applied:
    name = command.source
    if command.seq < 1:
        raise ProtocolError("ack seq must be >= 1")
    source = state["sources"].get(name)
    if source is None:
        raise ProtocolError(f"ack for unknown source {name!r}")
    if source["status"] == "exited":
        return Applied(disposition="ignored", detail="exited source")
    if command.epoch not in source["epochs"]:
        raise ProtocolError("ack for unknown epoch")
    runtime = source["current"]
    if runtime is None or runtime["epoch"] != command.epoch:
        return Applied(disposition="ignored", detail="superseded epoch")
    seq = command.seq
    already = seq <= runtime["acked_prefix"] or seq in runtime["acked"]
    if already:
        return Applied(disposition="ignored", detail="duplicate ack")
    if seq > runtime["arrived_prefix"] and seq not in runtime["arrived"]:
        # Ack ahead of data: remember it; it completes nothing until data.
        runtime["early_acks"].add(seq)
        return Applied(disposition="accepted", new_boundary=state["boundary"])
    runtime["acked"].add(seq)
    _extend_prefix(runtime, "acked")
    source["idle"] = False
    _reclaim_index(runtime)
    _recompute_source_water(source)
    applied = Applied(disposition="accepted")
    _maybe_complete_handoff_for(state, source, applied)
    _recompute_global(state, applied)
    return applied


def _reclaim_index(runtime: dict) -> None:
    """Reclaim gap/ack/event-time index space after a cut is fully confirmed.

    Once ``1..cut`` is arrived+acked the source water reaches the frontier; the
    prefix event times and counters up to ``cut`` are dead.  Records beyond
    ``cut`` remain tracked because a later progress message may extend the cut.
    """
    cut = runtime["cut"]
    if cut == 0 or not _epoch_complete(runtime):
        return
    # Drop event times for the confirmed prefix (they can no longer matter).
    dead = [seq for seq in runtime["ts"] if seq <= cut]
    for seq in dead:
        del runtime["ts"][seq]
    # Prefix counters already cover the range; tail seqs remain in the sets.


def _apply_progress(state: dict, command: Progress) -> Applied:
    msg = command.msg
    msg.validate()
    source = state["sources"].get(msg.source)
    if source is None:
        raise ProtocolError(f"progress from unknown source {msg.source!r}")
    if source["status"] == "exited":
        return Applied(disposition="ignored", detail="exited source")
    if msg.epoch in source["epochs"] and (
        source["current"] is None or source["current"]["epoch"] != msg.epoch
    ):
        return Applied(disposition="ignored", detail="superseded epoch")
    runtime = _ensure_epoch(source, msg.epoch)
    if msg.cut_seq < runtime["cut"]:
        raise ProtocolError("cut_seq must be non-decreasing within an epoch")
    if msg.frontier < runtime["frontier"]:
        raise ProtocolError("frontier must be non-decreasing within an epoch")
    runtime["cut"] = msg.cut_seq
    if msg.frontier > runtime["frontier"]:
        runtime["frontier"] = msg.frontier
    source["idle"] = False
    _recompute_source_water(source)
    applied = Applied(disposition="accepted")
    _maybe_complete_handoff_for(state, source, applied)
    _recompute_global(state, applied)
    return applied


def _apply_idle(state: dict, command: IdleTimeout) -> Applied:
    source = state["sources"].get(command.source)
    if source is None:
        raise ProtocolError("idle timeout for unknown source")
    if source["status"] == "exited":
        return Applied(disposition="ignored", detail="exited source")
    if source.get("reserved"):
        return Applied(disposition="ignored", detail="reserved child")
    # Timeout is honored as a request; sources carrying blockers remain
    # participants even when flagged idle (see _participating).
    source["idle"] = True
    applied = Applied(disposition="accepted")
    _recompute_global(state, applied)
    return applied


def _apply_begin_handoff(state: dict, command: BeginHandoff) -> Applied:
    existing = state["handoffs"].get(command.handoff_id)
    if existing is not None:
        if existing["status"] != "pending":
            raise ProtocolError(
                f"handoff {command.handoff_id!r} already {existing['status']}"
            )
        if (
            existing["parent"] != command.parent
            or existing["boundary"] != command.boundary
            or tuple(existing["expected_children"]) != tuple(command.children)
        ):
            raise ProtocolError(
                "handoff retry with mismatching parameters"
            )
        return Applied(disposition="ignored", detail="handoff already pending")
    if command.boundary < state["boundary"]:
        raise ProtocolError(
            f"handoff boundary {command.boundary} below global boundary "
            f"{state['boundary']}"
        )
    parent = state["sources"].get(command.parent)
    if parent is None:
        raise ProtocolError(f"unknown parent {command.parent!r}")
    if parent["status"] != "active":
        raise ProtocolError("parent cannot hand off again")
    if state["parent_of"].get(command.handoff_id) is not None:
        raise ProtocolError("duplicate handoff registration")
    for child in command.children:
        if child in state["sources"] or child == command.parent:
            raise ProtocolError(f"child {child!r} already exists")
    handoff = {
        "status": "pending",
        "parent": command.parent,
        "boundary": command.boundary,
        "expected_children": list(command.children),
        "children": {},
    }
    state["handoffs"][command.handoff_id] = handoff
    state["parent_of"][command.handoff_id] = command.parent
    applied = Applied(disposition="accepted", new_boundary=state["boundary"])
    return applied


def _completable(state: dict, handoff: dict) -> bool:
    parent = state["sources"][handoff["parent"]]
    if parent["water"] < handoff["boundary"]:
        return False
    for child in handoff["expected_children"]:
        if child not in handoff["children"]:
            return False
    return True


def _complete_handoff(state: dict, handoff_id: str, applied: Applied) -> None:
    handoff = state["handoffs"][handoff_id]
    boundary = handoff["boundary"]
    parent = state["sources"][handoff["parent"]]
    parent["status"] = "exited"
    parent["idle"] = False
    for child_name in handoff["expected_children"]:
        info = handoff["children"][child_name]
        child = state["sources"][child_name]
        child["reserved"] = False
        child["status"] = "active"
        child["idle"] = False
        child["floor"] = boundary
        child["water"] = max(boundary, info["frontier"])
        runtime = _epoch_state(info["epoch"], info["frontier"])
        runtime["cut"] = 0
        child["current"] = runtime
        child["epochs"] = {info["epoch"]}
    handoff["status"] = "done"
    state["parent_of"].pop(handoff_id, None)
    applied.handoff_completed = handoff_id
    applied.effects.append(
        (
            "HANDOFF_DONE",
            {
                "id": handoff_id,
                "parent": handoff["parent"],
                "boundary": boundary,
                "children": list(handoff["expected_children"]),
            },
        )
    )
    _recompute_global(state, applied)


def _maybe_complete_handoff_for(
    state: dict, source: dict, applied: Applied
) -> None:
    for handoff_id, parent_name in list(state["parent_of"].items()):
        handoff = state["handoffs"][handoff_id]
        if handoff["parent"] != parent_name or source is not state["sources"].get(
            parent_name
        ):
            continue
        if _completable(state, handoff):
            _complete_handoff(state, handoff_id, applied)


def _apply_takeover(state: dict, command: ChildTakeover) -> Applied:
    handoff = state["handoffs"].get(command.handoff_id)
    if handoff is None:
        raise ProtocolError(f"unknown handoff {command.handoff_id!r}")
    if handoff["status"] == "done":
        info = handoff["children"].get(command.child)
        if (
            info is not None
            and info["epoch"] == command.epoch
            and info["frontier"] == command.frontier
        ):
            return Applied(disposition="ignored", detail="already taken over")
        raise ProtocolError("handoff already completed")
    if handoff["status"] == "cancelled":
        raise ProtocolError("handoff was cancelled")
    if command.child not in handoff["expected_children"]:
        raise ProtocolError(
            f"{command.child!r} is not a declared child of this handoff"
        )
    if command.frontier < handoff["boundary"]:
        raise ProtocolError(
            "first child frontier must not be below the handoff boundary"
        )
    if command.epoch < 1:
        raise ProtocolError("child epoch must be a positive integer")
    known = handoff["children"].get(command.child)
    if known is not None:
        if known["epoch"] != command.epoch or known["frontier"] != command.frontier:
            raise ProtocolError("child retry with mismatching parameters")
        return Applied(disposition="ignored", detail="takeover already recorded")
    existing = state["sources"].get(command.child)
    if existing is not None:
        raise ProtocolError("child participant already registered")
    handoff["children"][command.child] = {
        "epoch": command.epoch,
        "frontier": command.frontier,
    }
    reserved = _source_state(floor=handoff["boundary"])
    reserved["reserved"] = True
    state["sources"][command.child] = reserved
    applied = Applied(disposition="accepted", new_boundary=state["boundary"])
    if _completable(state, handoff):
        _complete_handoff(state, command.handoff_id, applied)
    return applied


def _apply_cancel_handoff(
    state: dict, command: CancelHandoff
) -> Applied:
    handoff = state["handoffs"].get(command.handoff_id)
    if handoff is None:
        raise ProtocolError("unknown handoff")
    if handoff["status"] == "done":
        raise ProtocolError("completed handoff cannot be cancelled")
    if handoff["status"] == "cancelled":
        return Applied(disposition="ignored", detail="already cancelled")
    for child in handoff["children"]:
        state["sources"].pop(child, None)
    handoff["status"] = "cancelled"
    state["parent_of"].pop(command.handoff_id, None)
    applied = Applied(disposition="accepted", new_boundary=state["boundary"])
    _recompute_global(state, applied)
    return applied


def _apply_confirm(state: dict, command: ConfirmDecision) -> Applied:
    decision = state["decisions"].get(command.decision_id)
    if decision is None:
        raise ProtocolError(f"unknown decision {command.decision_id!r}")
    if decision["status"] == "confirmed":
        return Applied(disposition="ignored", detail="already confirmed")
    decision["status"] = "confirmed"
    return Applied(disposition="accepted")


def _apply_publish(state: dict, command) -> Applied:
    decision = state["decisions"].get(command.decision_id)
    if decision is None:
        raise ProtocolError(f"unknown decision {command.decision_id!r}")
    if decision["boundary"] != command.boundary:
        raise ProtocolError("publish boundary differs from sealed decision")
    if decision["status"] == "durable":
        decision["status"] = "published"
        return Applied(disposition="accepted")
    return Applied(disposition="ignored", detail=f"status={decision['status']}")


# ---------------------------------------------------------------------------
# Snapshot serialization (used by checkpoints)
# ---------------------------------------------------------------------------


def snapshot_state(state: dict) -> dict:
    sources = {}
    for name, source in state["sources"].items():
        cur = source["current"]
        current = None
        if cur is not None:
            current = {
                "epoch": cur["epoch"],
                "frontier": cur["frontier"],
                "cut": cur["cut"],
                "arrived_prefix": cur["arrived_prefix"],
                "acked_prefix": cur["acked_prefix"],
                "arrived": sorted(cur["arrived"]),
                "acked": sorted(cur["acked"]),
                "early_acks": sorted(cur["early_acks"]),
                "ts": {str(k): v for k, v in sorted(cur["ts"].items())},
                "water": cur["water"],
            }
        sources[name] = {
            "status": source["status"],
            "epochs": sorted(source["epochs"]),
            "current": current,
            "floor": source["floor"],
            "water": source["water"],
            "idle": source["idle"],
            "reserved": source.get("reserved", False),
        }
    handoffs = {}
    for hid, h in state["handoffs"].items():
        handoffs[hid] = {
            "status": h["status"],
            "parent": h["parent"],
            "boundary": h["boundary"],
            "expected_children": list(h["expected_children"]),
            "children": {
                child: dict(info) for child, info in h["children"].items()
            },
        }
    return {
        "boundary": state["boundary"],
        "sources": sources,
        "handoffs": handoffs,
        "parent_of": dict(state["parent_of"]),
        "decisions": {k: dict(v) for k, v in state["decisions"].items()},
        "decision_seq": state["decision_seq"],
        "late": [
            [reason, message] for reason, message in state["late"]
        ],
    }


def restore_state(snap: dict) -> dict:
    sources = {}
    for name, raw in snap["sources"].items():
        source = {
            "status": raw["status"],
            "epochs": set(raw["epochs"]),
            "current": None,
            "floor": raw["floor"],
            "water": raw["water"],
            "idle": raw["idle"],
            "reserved": raw.get("reserved", False),
        }
        cur = raw.get("current")
        if cur is not None:
            source["current"] = {
                "epoch": cur["epoch"],
                "frontier": cur["frontier"],
                "cut": cur["cut"],
                "arrived_prefix": cur["arrived_prefix"],
                "acked_prefix": cur["acked_prefix"],
                "arrived": set(cur["arrived"]),
                "acked": set(cur["acked"]),
                "early_acks": set(cur["early_acks"]),
                "ts": {int(k): v for k, v in cur["ts"].items()},
                "water": cur["water"],
            }
        sources[name] = source
    handoffs = {}
    for hid, raw in snap["handoffs"].items():
        handoffs[hid] = {
            "status": raw["status"],
            "parent": raw["parent"],
            "boundary": raw["boundary"],
            "expected_children": list(raw["expected_children"]),
            "children": {
                child: dict(info) for child, info in raw["children"].items()
            },
        }
    return {
        "boundary": snap["boundary"],
        "sources": sources,
        "handoffs": handoffs,
        "parent_of": dict(snap.get("parent_of", {})),
        "decisions": {
            k: dict(v) for k, v in snap.get("decisions", {}).items()
        },
        "decision_seq": snap.get("decision_seq", 0),
        "late": [tuple(item) for item in snap.get("late", [])],
    }
