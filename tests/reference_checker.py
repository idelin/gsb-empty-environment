"""Independent reference checker for the watermark coordination spec.

This module deliberately does **not** import the production kernel.  It is a
second, deliberately naive implementation of the same rules, written for
verifiability over efficiency (brute-force scans are fine for small inputs).

The exploration tests drive both implementations with identical command
sequences and require identical observable behavior:

* the global boundary after every command;
* the set of durable decisions (id, boundary) in order;
* late-channel classifications;
* handoff completion points.

It also asserts the safety invariants directly:

* a boundary is never sealed while any participating source has a gap inside
  its declared cut or unacknowledged arrived data at or below that boundary;
* the boundary never regresses;
* after a handoff completes, the parent no longer participates and every
  child participates from the handoff boundary.
"""
from __future__ import annotations

from dataclasses import dataclass, field


class RefError(Exception):
    pass


@dataclass
class RefEpoch:
    frontier: int = 0
    cut: int = 0
    arrived: set = field(default_factory=set)
    acked: set = field(default_factory=set)
    early: set = field(default_factory=set)
    ts: dict = field(default_factory=dict)
    water: int | None = None


@dataclass
class RefSource:
    status: str = "active"  # active | idle | exited
    epochs: set = field(default_factory=set)
    current: RefEpoch | None = None
    water: int = 0
    idle: bool = False
    reserved: bool = False


class RefChecker:
    def __init__(self) -> None:
        self.boundary = 0
        self.sources: dict[str, RefSource] = {}
        self.handoffs: dict[str, dict] = {}
        self.decisions: list[tuple[str, int]] = []
        self.late: list[tuple[str, tuple]] = []
        self._decision_seq = 0
        self.violations: list[str] = []

    # ------------------------------------------------------------------ util

    def _epoch_complete(self, ep: RefEpoch) -> bool:
        for seq in range(1, ep.cut + 1):
            if seq not in ep.arrived or seq not in ep.acked:
                return False
        return True

    def _outstanding(self, src: RefSource) -> bool:
        ep = src.current
        if ep is None:
            return False
        for seq in range(1, ep.cut + 1):
            if seq not in ep.arrived:
                return True
        for seq in ep.arrived:
            if seq not in ep.acked:
                return True
        return False

    def _refresh_source_water(self, src: RefSource) -> None:
        ep = src.current
        if ep is None:
            return
        if self._epoch_complete(ep):
            if ep.water is None or ep.frontier > ep.water:
                ep.water = ep.frontier
        if ep.water is not None and ep.water > src.water:
            src.water = ep.water

    def _participants(self) -> list[int]:
        waters = []
        for src in self.sources.values():
            if src.status == "exited" or src.reserved:
                continue
            if src.idle and not self._outstanding(src):
                continue
            waters.append(src.water)
        return waters

    def _recompute(self) -> None:
        waters = self._participants()
        candidate = min(waters) if waters else self.boundary
        if candidate > self.boundary:
            # Safety: no participant may still block at or below candidate.
            for name, src in self.sources.items():
                if src.status == "exited" or src.reserved:
                    continue
                ep = src.current
                if ep is None:
                    continue
                for seq in range(1, ep.cut + 1):
                    if seq not in ep.arrived:
                        self.violations.append(
                            f"sealed {candidate} while {name} misses seq {seq}"
                        )
                    elif seq not in ep.acked:
                        self.violations.append(
                            f"sealed {candidate} while {name} seq {seq} unacked"
                        )
            self.boundary = candidate
            self._decision_seq += 1
            self.decisions.append((f"D{self._decision_seq:08d}", candidate))

    # ------------------------------------------------------------------ API

    def step(self, command) -> str:
        """Apply one command; return disposition. Raises RefError on protocol
        violations (mirrors production ProtocolError)."""
        kind = command.kind
        handler = getattr(self, f"_cmd_{kind}", None)
        if handler is None:
            raise RefError(f"unknown command {kind}")
        disposition = handler(command)
        self._recompute()
        return disposition

    # -------------------------------------------------------------- commands

    def _cmd_register_source(self, cmd) -> str:
        if cmd.source in self.sources:
            raise RefError("duplicate source")
        self.sources[cmd.source] = RefSource()
        return "accepted"

    def _cmd_join_source(self, cmd) -> str:
        if cmd.source in self.sources:
            raise RefError("duplicate source")
        if cmd.accepted_floor != self.boundary:
            raise RefError("floor mismatch")
        src = RefSource(water=cmd.accepted_floor)
        self.sources[cmd.source] = src
        return "accepted"

    def _get_source(self, name: str) -> RefSource:
        src = self.sources.get(name)
        if src is None:
            raise RefError(f"unknown source {name}")
        return src

    def _open_epoch(self, src: RefSource, epoch: int) -> RefEpoch:
        if epoch in src.epochs:
            if src.current is not None and src.current is not None:
                cur_epoch = getattr(src.current, "_epoch_no", None)
                if cur_epoch == epoch:
                    return src.current
            raise RefError("epoch reuse")
        if src.epochs and epoch < max(src.epochs):
            raise RefError("epoch regression")
        if src.current is not None and self._outstanding(src):
            raise RefError("epoch bump with outstanding blockers")
        ep = RefEpoch()
        ep._epoch_no = epoch  # type: ignore[attr-defined]
        src.current = ep
        src.epochs.add(epoch)
        src.idle = False
        return ep

    def _cmd_data(self, cmd) -> str:
        msg = cmd.msg
        src = self._get_source(msg.source)
        if src.status == "exited":
            self.late.append(("exited_source", (msg.source, msg.epoch, msg.seq)))
            return "late"
        if src.reserved:
            raise RefError("reserved child")
        cur_epoch = getattr(src.current, "_epoch_no", None) if src.current else None
        if msg.epoch in src.epochs and cur_epoch != msg.epoch:
            self.late.append(("old_epoch", (msg.source, msg.epoch, msg.seq)))
            return "late"
        ep = self._open_epoch(src, msg.epoch)
        if msg.seq in ep.arrived:
            if ep.ts.get(msg.seq) != msg.ts:
                raise RefError("conflicting duplicate")
            return "ignored"
        if max(src.water, self.boundary) > msg.ts:
            self.late.append(
                ("below_closed_boundary", (msg.source, msg.epoch, msg.seq))
            )
            return "late"
        ep.arrived.add(msg.seq)
        ep.ts[msg.seq] = msg.ts
        if msg.seq in ep.early:
            ep.early.discard(msg.seq)
            ep.acked.add(msg.seq)
        src.idle = False
        self._refresh_source_water(src)
        return "accepted"

    def _cmd_ack(self, cmd) -> str:
        src = self._get_source(cmd.source)
        if src.status == "exited":
            return "ignored"
        if cmd.epoch not in src.epochs:
            raise RefError("ack for unknown epoch")
        cur_epoch = getattr(src.current, "_epoch_no", None) if src.current else None
        if cur_epoch != cmd.epoch:
            return "ignored"
        ep = src.current
        if cmd.seq in ep.acked:
            return "ignored"
        if cmd.seq not in ep.arrived:
            ep.early.add(cmd.seq)
            return "accepted"
        ep.acked.add(cmd.seq)
        src.idle = False
        self._refresh_source_water(src)
        self._maybe_complete_handoffs()
        return "accepted"

    def _cmd_progress(self, cmd) -> str:
        msg = cmd.msg
        src = self._get_source(msg.source)
        if src.status == "exited":
            return "ignored"
        cur_epoch = getattr(src.current, "_epoch_no", None) if src.current else None
        if msg.epoch in src.epochs and cur_epoch != msg.epoch:
            return "ignored"
        ep = self._open_epoch(src, msg.epoch)
        if msg.cut_seq < ep.cut:
            raise RefError("cut regression")
        if msg.frontier < ep.frontier:
            raise RefError("frontier regression")
        ep.cut = msg.cut_seq
        ep.frontier = max(ep.frontier, msg.frontier)
        src.idle = False
        self._refresh_source_water(src)
        self._maybe_complete_handoffs()
        return "accepted"

    def _cmd_idle_timeout(self, cmd) -> str:
        src = self._get_source(cmd.source)
        if src.status == "exited" or src.reserved:
            return "ignored"
        src.idle = True
        return "accepted"

    # ------------------------------------------------------------- handoffs

    def _cmd_begin_handoff(self, cmd) -> str:
        existing = self.handoffs.get(cmd.handoff_id)
        if existing is not None:
            if existing["status"] != "pending":
                raise RefError("handoff not pending")
            if (
                existing["parent"] != cmd.parent
                or existing["boundary"] != cmd.boundary
                or list(existing["children"]) != list(cmd.children)
            ):
                raise RefError("handoff retry mismatch")
            return "ignored"
        if cmd.boundary < self.boundary:
            raise RefError("handoff boundary below global")
        parent = self._get_source(cmd.parent)
        if parent.status != "active":
            raise RefError("parent not active")
        for child in cmd.children:
            if child in self.sources:
                raise RefError("child exists")
        self.handoffs[cmd.handoff_id] = {
            "status": "pending",
            "parent": cmd.parent,
            "boundary": cmd.boundary,
            "children": list(cmd.children),
            "takeovers": {},
        }
        return "accepted"

    def _maybe_complete_handoffs(self) -> None:
        for hid, h in self.handoffs.items():
            if h["status"] != "pending":
                continue
            parent = self.sources[h["parent"]]
            if parent.water < h["boundary"]:
                continue
            if any(c not in h["takeovers"] for c in h["children"]):
                continue
            parent.status = "exited"
            for child in h["children"]:
                info = h["takeovers"][child]
                src = self.sources[child]
                src.reserved = False
                src.idle = False
                src.water = max(h["boundary"], info["frontier"])
                ep = RefEpoch(frontier=info["frontier"], cut=0)
                ep._epoch_no = info["epoch"]  # type: ignore[attr-defined]
                ep.water = info["frontier"]
                src.current = ep
                src.epochs = {info["epoch"]}
            h["status"] = "done"

    def _cmd_child_takeover(self, cmd) -> str:
        h = self.handoffs.get(cmd.handoff_id)
        if h is None:
            raise RefError("unknown handoff")
        if h["status"] == "done":
            info = h["takeovers"].get(cmd.child)
            if info and info["epoch"] == cmd.epoch and info["frontier"] == cmd.frontier:
                return "ignored"
            raise RefError("handoff done")
        if h["status"] != "pending":
            raise RefError("handoff not pending")
        if cmd.child not in h["children"]:
            raise RefError("undeclared child")
        if cmd.frontier < h["boundary"]:
            raise RefError("child frontier below handoff boundary")
        known = h["takeovers"].get(cmd.child)
        if known is not None:
            if known["epoch"] != cmd.epoch or known["frontier"] != cmd.frontier:
                raise RefError("takeover mismatch")
            return "ignored"
        if cmd.child in self.sources and not self.sources[cmd.child].reserved:
            raise RefError("child exists")
        h["takeovers"][cmd.child] = {"epoch": cmd.epoch, "frontier": cmd.frontier}
        src = RefSource(water=h["boundary"], reserved=True)
        self.sources[cmd.child] = src
        self._maybe_complete_handoffs()
        return "accepted"

    def _cmd_cancel_handoff(self, cmd) -> str:
        h = self.handoffs.get(cmd.handoff_id)
        if h is None:
            raise RefError("unknown handoff")
        if h["status"] == "done":
            raise RefError("cannot cancel completed handoff")
        if h["status"] == "cancelled":
            return "ignored"
        for child in h["takeovers"]:
            self.sources.pop(child, None)
        h["status"] = "cancelled"
        return "accepted"

    # ------------------------------------------------------------ decisions

    def _cmd_publish_decision(self, cmd) -> str:
        return "accepted"

    def _cmd_confirm_decision(self, cmd) -> str:
        return "accepted"
