"""Block-based multi-track audio + control-event scheduler.

Standard library only; no audio device is touched. Source samples come from
local fixture arrays and rendered blocks are returned as plain data
(`OutputBlock`).

Semantics
---------
- The sample clock advances in blocks of ``block_size`` samples. Declared
  latency is exactly one block (see ``fixtures.DECLARED_LATENCY_SECONDS``).
- Inside a block, rendering is cut into segments at exact event positions;
  each segment is rendered with the parameter state active for it.
- All events sharing one position commit as a single transaction: every
  parameter is validated and the resulting routing graph is cycle-checked
  *before* any of it becomes visible, so a block never observes an
  intermediate (possibly cyclic) routing state. One invalid parameter
  rolls the whole transaction back.
- Events carry ``epoch`` and ``version``. Events from a foreign epoch are
  dropped; for the same ``event_id`` only the highest version is kept.
- Late corrections (position already rendered): if the covering output
  block is still unconfirmed, the scheduler restores the block-start
  checkpoint and recomputes only the unconfirmed tail; if the output was
  already confirmed (played), an audit conflict is recorded instead.
- ``pause()`` may happen after events were applied but before the output
  is confirmed; ``resume()`` dedupes emitted output by output id.
"""
from __future__ import annotations

import copy
import math
from dataclasses import dataclass, field

from .events import FILTER, GAIN, ROUTE, ControlEvent
from .graph import RoutingGraph

MAX_GAIN = 4.0


@dataclass
class NodeState:
    gain: float
    cutoff: float
    alpha: float
    z: float = 0.0  # one-pole filter state


@dataclass
class AuditEntry:
    kind: str  # "conflict" | "rollback" | "epoch_drop" | "rejected"
    message: str
    details: dict = field(default_factory=dict)


@dataclass
class OutputBlock:
    output_id: str
    epoch: int
    start: int
    length: int
    samples: tuple


@dataclass
class _Snapshot:
    position: int
    epoch: int
    states: dict
    edges: dict
    applied: frozenset


class Scheduler:
    def __init__(self, sample_rate, block_size, tracks, sources,
                 master="master", default_edges=None):
        self.sample_rate = sample_rate
        self.block_size = block_size
        self.master = master
        self._sources = dict(sources)
        nodes = list(tracks) + [master]
        self._graph = RoutingGraph(nodes)
        if default_edges is None:
            default_edges = [(t, master) for t in tracks]
        for s, d in default_edges:
            self._graph.connect(s, d)
        nyquist = sample_rate / 2.0
        self._states = {
            n: NodeState(gain=1.0, cutoff=nyquist, alpha=self._alpha(nyquist))
            for n in nodes
        }
        self.epoch = 0
        self.position = 0
        self._schedule = {}          # position -> {event_id: ControlEvent}
        self._applied = set()        # {(event_id, version)}
        self.applied_log = []        # (event_id, version, position, outcome)
        self.audit_log = []          # [AuditEntry]
        self._pending = {}           # output_id -> OutputBlock (unconfirmed)
        self._confirmed = set()      # output_ids already played
        self._emit_log = []          # history of emitted output ids
        self._paused = False
        self._block_checkpoints = {}  # block start sample -> _Snapshot
        self._named_checkpoints = {}
        self._recompute_topology()

    # ------------------------------------------------------------------ API
    def submit(self, event: ControlEvent) -> bool:
        """Schedule an event, or route it to late-correction handling."""
        if event.position < 0:
            self._audit("rejected", f"negative position: {event.event_id}")
            return False
        if event.epoch != self.epoch:
            self._audit(
                "epoch_drop",
                f"event {event.event_id} epoch {event.epoch} != current "
                f"epoch {self.epoch}; dropped",
                {"event_id": event.event_id, "epoch": event.epoch})
            return False
        if event.position < self.position:
            return self._handle_late(event)
        slot = self._schedule.setdefault(event.position, {})
        existing = slot.get(event.event_id)
        if existing is not None and existing.version >= event.version:
            return False  # stale version
        slot[event.event_id] = event
        return True

    def render_block(self):
        """Render the next block. Returns None while paused, or when the
        output id was already confirmed (resume dedups by output id)."""
        if self._paused:
            return None
        return self._render_block_internal()

    def confirm(self, output_id: str):
        """Mark an output block as played; it can no longer be recomputed."""
        out = self._pending.pop(output_id, None)
        if out is not None:
            self._confirmed.add(output_id)

    def pause(self):
        self._paused = True

    def resume(self):
        self._paused = False

    @property
    def paused(self):
        return self._paused

    def checkpoint(self, name: str):
        """Save a named snapshot of the render state (params, routing,
        filter state, position, epoch, applied-event set)."""
        self._named_checkpoints[name] = self._snapshot()

    def restore(self, name: str):
        self._restore(self._named_checkpoints[name])

    def set_epoch(self, epoch: int):
        """Switch sample-clock epoch; stale-epoch events never fire."""
        dropped = [e.event_id for evs in self._schedule.values()
                   for e in evs.values() if e.epoch != epoch]
        for pos in list(self._schedule):
            keep = {eid: e for eid, e in self._schedule[pos].items()
                    if e.epoch == epoch}
            if keep:
                self._schedule[pos] = keep
            else:
                del self._schedule[pos]
        self.epoch = epoch
        if dropped:
            self._audit("epoch_drop",
                        f"epoch -> {epoch}: dropped {len(dropped)} stale "
                        f"event(s)", {"events": sorted(dropped)})

    def pending_outputs(self):
        return list(self._pending.values())

    def is_confirmed(self, output_id: str) -> bool:
        return output_id in self._confirmed

    @property
    def emit_log(self):
        return list(self._emit_log)

    # ------------------------------------------------------------- internal
    def _alpha(self, cutoff):
        return 1.0 - math.exp(-2.0 * math.pi * cutoff / self.sample_rate)

    def _audit(self, kind, message, details=None):
        self.audit_log.append(AuditEntry(kind, message, details or {}))

    def _recompute_topology(self):
        self._order = self._graph.topo_order()
        self._incoming = {
            n: [s for s in self._graph.nodes if n in self._graph.edges[s]]
            for n in self._graph.nodes
        }

    def _snapshot(self):
        return _Snapshot(
            position=self.position,
            epoch=self.epoch,
            states=copy.deepcopy(self._states),
            edges={n: set(d) for n, d in self._graph.edges.items()},
            applied=frozenset(self._applied),
        )

    def _restore(self, snap):
        self.position = snap.position
        self.epoch = snap.epoch
        self._states = copy.deepcopy(snap.states)
        self._graph = RoutingGraph(list(self._graph.nodes))
        for n, ds in snap.edges.items():
            self._graph.edges[n] = set(ds)
        self._applied = set(snap.applied)
        self._recompute_topology()

    def _render_block_internal(self):
        start = self.position
        self._block_checkpoints[start] = self._snapshot()
        samples = self._render_range(start, start + self.block_size)
        self.position = start + self.block_size
        output_id = f"{self.epoch}:{start}"
        if output_id in self._confirmed:
            return None  # dedup: already-played output is never re-emitted
        out = OutputBlock(output_id, self.epoch, start, self.block_size,
                          tuple(samples))
        self._pending[output_id] = out  # replace-on-recompute dedups by id
        self._emit_log.append(output_id)
        return out

    def _render_range(self, start, end):
        """Render [start, end), cutting segments at event positions."""
        positions = sorted(p for p in self._schedule if start <= p < end)
        out = []
        cur = start
        for p in positions:
            if p > cur:
                out.extend(self._render_segment(cur, p))
            self._apply_transaction(p)
            cur = p
        if cur < end:
            out.extend(self._render_segment(cur, end))
        return out

    def _render_segment(self, start, end):
        order = self._order
        incoming = self._incoming
        states = self._states
        sources = self._sources
        out = []
        for i in range(start, end):
            values = {}
            for node in order:
                src = sources.get(node)
                acc = src[i] if src is not None else 0.0
                for prev in incoming[node]:
                    acc += values[prev]
                st = states[node]
                st.z = st.z + st.alpha * (acc - st.z)
                values[node] = st.z * st.gain
            out.append(values[self.master])
        return out

    def _apply_transaction(self, position):
        events = [e for e in self._schedule.get(position, {}).values()
                  if (e.event_id, e.version) not in self._applied
                  and e.epoch == self.epoch]
        if not events:
            return
        states = copy.deepcopy(self._states)
        graph = self._graph.copy()
        errors = []
        for e in sorted(events, key=lambda e: e.event_id):
            err = self._apply_one(e, states, graph)
            if err:
                errors.append(f"{e.event_id}: {err}")
        # The transaction is validated as a whole on the *final* graph, so
        # same-sample route pairs never expose an intermediate illegal ring.
        if not errors and graph.has_cycle():
            errors.append("routing cycle detected")
        outcome = "committed" if not errors else "rolled_back"
        if errors:
            self._audit("rollback",
                        f"transaction at sample {position} rolled back",
                        {"errors": errors,
                         "events": sorted(e.event_id for e in events)})
        else:
            self._states = states
            self._graph = graph
            self._recompute_topology()
        for e in events:
            self._applied.add((e.event_id, e.version))
            self.applied_log.append(
                (e.event_id, e.version, position, outcome))

    def _apply_one(self, event, states, graph):
        """Validate and apply one event to the working copies.
        Returns an error string, or None on success."""
        p = event.params
        if event.kind == GAIN:
            node, g = p.get("track"), p.get("gain")
            if node not in states:
                return f"unknown track {node!r}"
            if (not isinstance(g, (int, float)) or not math.isfinite(g)
                    or not 0.0 <= g <= MAX_GAIN):
                return f"invalid gain {g!r}"
            states[node].gain = float(g)
            return None
        if event.kind == FILTER:
            node, c = p.get("track"), p.get("cutoff")
            if node not in states:
                return f"unknown track {node!r}"
            if (not isinstance(c, (int, float)) or not math.isfinite(c)
                    or not 0.0 < c <= self.sample_rate / 2.0):
                return f"invalid cutoff {c!r}"
            states[node].cutoff = float(c)
            states[node].alpha = self._alpha(float(c))
            return None
        if event.kind == ROUTE:
            op, s, d = p.get("op"), p.get("src"), p.get("dst")
            if s not in graph.nodes or d not in graph.nodes:
                return f"unknown endpoint {s!r}->{d!r}"
            if op == "connect":
                if d in graph.edges[s]:
                    return f"edge {s!r}->{d!r} already exists"
                graph.connect(s, d)
                return None
            if op == "disconnect":
                if d not in graph.edges[s]:
                    return f"edge {s!r}->{d!r} does not exist"
                graph.disconnect(s, d)
                return None
            return f"unknown route op {op!r}"
        return f"unknown kind {event.kind!r}"

    def _handle_late(self, event):
        """Late correction: recompute unconfirmed output, or record an
        audit conflict if the covering output was already played."""
        blk_start = event.position - (event.position % self.block_size)
        output_id = f"{event.epoch}:{blk_start}"
        if output_id in self._confirmed:
            self._audit("conflict",
                        f"late correction {event.event_id} v{event.version} "
                        f"targets already-played output {output_id}",
                        {"event_id": event.event_id,
                         "version": event.version,
                         "output_id": output_id})
            return False
        snap = self._block_checkpoints.get(blk_start)
        if snap is None:
            self._audit("conflict",
                        f"late correction {event.event_id}: no checkpoint "
                        f"for block starting at {blk_start}",
                        {"event_id": event.event_id})
            return False
        frontier = self.position
        self._restore(snap)
        # Drop stale unconfirmed outputs at/after the correction point;
        # confirmed ones are kept and skipped by id on re-render.
        for oid in [oid for oid, o in self._pending.items()
                    if o.start >= blk_start]:
            del self._pending[oid]
        self.submit(event)  # now in the future relative to restored position
        while self.position < frontier:
            self._render_block_internal()
        return True
