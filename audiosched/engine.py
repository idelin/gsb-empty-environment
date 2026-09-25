"""Unified multi-track audio + control-event scheduler (offline, sample-accurate).

Semantics implemented here:

* Blocks are cut into segments exactly at event positions; all events at the
  same position commit as one atomic transaction.  If any parameter is
  illegal, or the resulting routing graph contains a cycle, the whole
  transaction rolls back and the block never observes an intermediate
  (illegal) state.
* Every event carries (epoch, version).  Events from a foreign epoch are
  quarantined and never fire.  A higher version of an already-seen event id
  is a correction; lower/equal versions are stale and ignored.
* Late corrections (position already rendered) are only allowed to
  recompute output that has not been confirmed yet.  If the affected output
  was confirmed (i.e. "already played"), an audit conflict is recorded and
  state is left untouched.
* The engine supports named checkpoints plus automatic per-block snapshots
  used by late-correction rollback.
* Pause halts rendering after event application but before output
  confirmation; on resume / rollback, outputs are de-duplicated by output
  id so already-confirmed blocks are never emitted twice.
"""

from __future__ import annotations

import copy
from dataclasses import dataclass, field

from .dsp import OnePoleLowpass
from .events import ControlEvent, EventKind
from .graph import Graph

MASTER = "master"


class InvalidTransaction(Exception):
    """Raised when any event of a transaction is illegal."""


@dataclass
class OutputBlock:
    output_id: tuple[int, int]  # (epoch, start_sample)
    start: int
    epoch: int
    samples: list[float] = field(default_factory=list)


class Engine:
    def __init__(self, sample_rate: int, block_size: int,
                 tracks: dict[str, list[float]],
                 routes: list[tuple[str, str]],
                 buses: tuple[str, ...] = (),
                 latency_samples: int = 0,
                 tolerance: float = 1e-9):
        self.sample_rate = sample_rate
        self.block_size = block_size
        self.latency_samples = latency_samples  # declared, informational
        self.tolerance = tolerance

        self.sources = {name: list(samples) for name, samples in tracks.items()}
        self.gain = {name: 1.0 for name in tracks}
        self.filters = {
            name: OnePoleLowpass(sample_rate, None) for name in tracks
        }
        self.graph = Graph(set(tracks) | set(buses) | {MASTER})
        for src, dst in routes:
            self.graph.add_edge(src, dst)
        if self.graph.has_cycle():
            raise ValueError("initial routing contains a cycle")
        self._topo = self.graph.topological_order()

        self.epoch = 0
        self.clock = 0
        self.pending: dict[str, ControlEvent] = {}
        self.applied_versions: dict[str, int] = {}

        self.emitted: list[OutputBlock] = []
        self._emitted_ids: set[tuple[int, int]] = set()
        self.confirmed: set[tuple[int, int]] = set()

        self.audit: list[dict] = []
        self._checkpoints: dict[str, dict] = {}
        self._auto_checkpoints: list[tuple[int, dict]] = []
        self._paused = False

    # ------------------------------------------------------------------ API

    def submit(self, event: ControlEvent) -> str:
        """Submit a control event.  Returns a disposition string."""
        if event.epoch != self.epoch:
            self.audit.append({
                "type": "quarantined", "event_id": event.event_id,
                "reason": "epoch_mismatch",
                "event_epoch": event.epoch, "engine_epoch": self.epoch,
            })
            return "quarantined"
        applied = self.applied_versions.get(event.event_id)
        if applied is not None and event.version <= applied:
            return "stale"
        pending = self.pending.get(event.event_id)
        if pending is not None and event.version <= pending.version:
            return "stale"
        if event.position < self.clock:
            return self._late_correction(event)
        self.pending[event.event_id] = event
        return "scheduled"

    def render_block(self) -> OutputBlock | None:
        """Render one block; returns None while paused."""
        if self._paused:
            return None
        start = self.clock
        self._auto_checkpoints.append((start, self._snapshot_state()))

        block = [0.0] * self.block_size
        in_block = sorted(
            (e for e in self.pending.values()
             if start <= e.position < start + self.block_size),
            key=lambda e: (e.position, e.event_id))
        positions = sorted({e.position for e in in_block})

        cursor = start
        for pos in positions:
            self._render_segment(cursor, pos, block, start)
            group = [e for e in in_block if e.position == pos]
            self._apply_transaction(pos, group)
            cursor = pos
        self._render_segment(cursor, start + self.block_size, block, start)

        self.clock = start + self.block_size
        out = OutputBlock(output_id=(self.epoch, start),
                          start=start, epoch=self.epoch, samples=block)
        if out.output_id not in self.confirmed \
                and out.output_id not in self._emitted_ids:
            self.emitted.append(out)
            self._emitted_ids.add(out.output_id)
        return out

    def render_blocks(self, n: int) -> list[OutputBlock]:
        out = []
        for _ in range(n):
            block = self.render_block()
            if block is not None:
                out.append(block)
        return out

    def confirm(self, output_id: tuple[int, int]) -> bool:
        """Confirm an output block as played; afterwards it is immutable."""
        if output_id not in self._emitted_ids:
            return False
        self.confirmed.add(output_id)
        self.audit.append({"type": "confirmed", "output_id": output_id})
        return True

    def pause(self) -> None:
        """Pause after event application, before output confirmation."""
        self._paused = True
        self.audit.append({"type": "paused", "clock": self.clock})

    def resume(self) -> None:
        self._paused = False
        self.audit.append({"type": "resumed", "clock": self.clock})

    @property
    def paused(self) -> bool:
        return self._paused

    def set_epoch(self, epoch: int) -> None:
        """Switch sample-clock epoch; pending foreign-epoch events are
        quarantined so they can never misfire on the new clock."""
        dropped = [eid for eid, e in self.pending.items() if e.epoch != epoch]
        for eid in sorted(dropped):
            del self.pending[eid]
            self.audit.append({"type": "quarantined", "event_id": eid,
                               "reason": "epoch_change"})
        self.epoch = epoch
        self.clock = 0
        self._auto_checkpoints.clear()
        self.emitted.clear()
        self._emitted_ids.clear()
        self.confirmed.clear()
        self.audit.append({"type": "epoch_change", "epoch": epoch})

    def checkpoint(self, name: str | None = None) -> str:
        cid = name or f"ckpt-{len(self._checkpoints)}"
        self._checkpoints[cid] = self._snapshot_state()
        self.audit.append({"type": "checkpoint", "id": cid,
                           "clock": self.clock})
        return cid

    def restore(self, checkpoint_id: str) -> None:
        self._restore_state(self._checkpoints[checkpoint_id])
        self.audit.append({"type": "restored", "id": checkpoint_id,
                           "clock": self.clock})

    def routing_has_cycle(self) -> bool:
        return self.graph.has_cycle()

    # ------------------------------------------------------- transactions

    def _apply_transaction(self, position: int,
                           events: list[ControlEvent]) -> bool:
        backup = self._snapshot_core()
        try:
            for ev in sorted(events, key=lambda e: e.event_id):
                self._apply_one(ev)
            if self.graph.has_cycle():
                raise InvalidTransaction(
                    "routing cycle detected in committed graph")
        except (InvalidTransaction, ValueError, KeyError) as exc:
            self._restore_core(backup)
            self.audit.append({
                "type": "transaction_rejected", "position": position,
                "events": [e.event_id for e in events], "reason": str(exc),
            })
            return False
        self._topo = self.graph.topological_order()
        for ev in events:
            self.applied_versions[ev.event_id] = ev.version
            self.pending.pop(ev.event_id, None)
        self.audit.append({
            "type": "transaction_applied", "position": position,
            "events": [e.event_id for e in events],
        })
        return True

    def _apply_one(self, ev: ControlEvent) -> None:
        if ev.kind is EventKind.SET_GAIN:
            gain = ev.params.get("gain")
            if not isinstance(gain, (int, float)) or isinstance(gain, bool) \
                    or gain != gain or gain in (float("inf"), float("-inf")) \
                    or gain < 0:
                raise InvalidTransaction(f"illegal gain {gain!r}")
            if ev.target not in self.gain:
                raise InvalidTransaction(f"unknown track {ev.target!r}")
            self.gain[ev.target] = float(gain)
        elif ev.kind is EventKind.SET_FILTER:
            if ev.target not in self.filters:
                raise InvalidTransaction(f"unknown track {ev.target!r}")
            cutoff = ev.params.get("cutoff")
            try:
                self.filters[ev.target].set_cutoff(cutoff)
            except ValueError as exc:
                raise InvalidTransaction(str(exc)) from exc
        elif ev.kind is EventKind.SET_ROUTE:
            action = ev.params.get("action")
            src, dst = ev.params.get("src"), ev.params.get("dst")
            if src not in self.graph.nodes or dst not in self.graph.nodes:
                raise InvalidTransaction(
                    f"unknown route endpoint {src!r}->{dst!r}")
            if action == "connect":
                self.graph.add_edge(src, dst)
            elif action == "disconnect":
                self.graph.remove_edge(src, dst)
            else:
                raise InvalidTransaction(f"unknown route action {action!r}")
        else:  # pragma: no cover - defensive
            raise InvalidTransaction(f"unknown kind {ev.kind!r}")

    # -------------------------------------------------------- late events

    def _late_correction(self, event: ControlEvent) -> str:
        start_of = lambda s: s - (s % self.block_size)
        affected_id = (self.epoch, start_of(event.position))
        if affected_id in self.confirmed:
            self.audit.append({
                "type": "conflict", "event_id": event.event_id,
                "position": event.position, "output_id": affected_id,
                "reason": "output already confirmed (played)",
            })
            return "conflict"
        bases = [c for c, _ in self._auto_checkpoints
                 if c <= event.position]
        if not bases:
            self.audit.append({
                "type": "conflict", "event_id": event.event_id,
                "position": event.position,
                "reason": "no checkpoint covers position",
            })
            return "conflict"
        base = max(bases)
        snapshot = next(s for c, s in reversed(self._auto_checkpoints)
                        if c == base)
        self._restore_state(snapshot)
        # Retract unconfirmed outputs at/after the rollback point; confirmed
        # ones stay and will be de-duplicated by output id on re-render.
        self.emitted = [o for o in self.emitted
                        if o.start < base or o.output_id in self.confirmed]
        self._emitted_ids = {o.output_id for o in self.emitted}
        self.pending[event.event_id] = event
        self.audit.append({
            "type": "late_correction", "event_id": event.event_id,
            "position": event.position, "rolled_back_to": base,
        })
        return "rescheduled"

    # ------------------------------------------------------------ rendering

    def _render_segment(self, begin: int, end: int,
                        block: list[float], block_start: int) -> None:
        for i in range(begin, end):
            block[i - block_start] = self._sample_at(i)

    def _sample_at(self, i: int) -> float:
        values = {n: 0.0 for n in self._topo}
        for node in self._topo:
            src = self.sources.get(node)
            if src is not None:
                x = src[i] if i < len(src) else 0.0
                values[node] += self.filters[node].process(x) \
                    * self.gain[node]
            for succ in self.graph.successors(node):
                values[succ] += values[node]
        return values[MASTER]

    # ----------------------------------------------------------- snapshots

    def _snapshot_core(self) -> dict:
        return {
            "gain": dict(self.gain),
            "filters": copy.deepcopy(self.filters),
            "graph": self.graph.copy(),
            "topo": list(self._topo),
        }

    def _restore_core(self, snap: dict) -> None:
        self.gain = dict(snap["gain"])
        self.filters = copy.deepcopy(snap["filters"])
        self.graph = snap["graph"].copy()
        self._topo = list(snap["topo"])

    def _snapshot_state(self) -> dict:
        return {
            "core": self._snapshot_core(),
            "epoch": self.epoch,
            "clock": self.clock,
            "pending": dict(self.pending),
            "applied_versions": dict(self.applied_versions),
            "emitted": list(self.emitted),
            "emitted_ids": set(self._emitted_ids),
            "confirmed": set(self.confirmed),
            "paused": self._paused,
        }

    def _restore_state(self, snap: dict) -> None:
        self._restore_core(snap["core"])
        self.epoch = snap["epoch"]
        self.clock = snap["clock"]
        self.pending = dict(snap["pending"])
        self.applied_versions = dict(snap["applied_versions"])
        self.emitted = list(snap["emitted"])
        self._emitted_ids = set(snap["emitted_ids"])
        self.confirmed = set(snap["confirmed"])
        self._paused = snap["paused"]
