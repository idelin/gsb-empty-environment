"""Continuously-executing dependency DAG with stage barriers and resource quotas.

Semantics
---------
- Vertices and edges carry string IDs. Isolated vertices and multi-edges are kept.
- Time intervals are left-closed right-open: a task occupying [start, end) frees
  its resources at ``end``, which may be reused by tasks starting at ``end``.
- Graph updates are atomic transactions: cycle check, not-yet-started target
  check (completed/running preconditions), and resource-quota check are all
  validated before commit; any violation rejects the whole update and preserves
  the original plan.
- Graph versions and execution attempts are separated. Each vertex has an epoch
  (the graph version that last invalidated it). An attempt born under an older
  epoch is stale: its result must not unlock successors in the new graph.
- Updates only re-plan the affected successors; independent branches keep
  running untouched.
- The scheduler is fully controllable: a manual clock plus a deterministic
  event heap ordered by (time, insertion sequence), so tests can interleave
  completions, cancels, retries and graph updates precisely, including
  simultaneous events.
"""

from __future__ import annotations

import heapq
from dataclasses import dataclass, field
from enum import Enum


class VertexState(Enum):
    PENDING = "PENDING"
    RUNNING = "RUNNING"
    SUCCEEDED = "SUCCEEDED"
    FAILED = "FAILED"
    CANCELLED = "CANCELLED"


@dataclass
class Vertex:
    vid: str
    stage: int = 0
    duration: float = 1.0
    resources: dict = field(default_factory=dict)
    effects: tuple = ()


@dataclass
class Edge:
    eid: str
    src: str
    dst: str


@dataclass
class GraphUpdate:
    add_vertices: list = field(default_factory=list)
    add_edges: list = field(default_factory=list)


class UpdateRejected(Exception):
    """Raised when an atomic graph-update transaction fails validation."""

    def __init__(self, reasons):
        self.reasons = list(reasons)
        super().__init__("; ".join(self.reasons))


class Graph:
    """Directed multigraph with string vertex/edge IDs; keeps isolated vertices."""

    def __init__(self):
        self.vertices = {}
        self.edges = {}
        self._out = {}
        self._in = {}

    def copy(self):
        g = Graph()
        g.vertices = dict(self.vertices)
        g.edges = dict(self.edges)
        g._out = {k: list(v) for k, v in self._out.items()}
        g._in = {k: list(v) for k, v in self._in.items()}
        return g

    def add_vertex(self, v):
        self.vertices[v.vid] = v

    def add_edge(self, e):
        self.edges[e.eid] = e
        self._out.setdefault(e.src, []).append(e.eid)
        self._in.setdefault(e.dst, []).append(e.eid)

    def predecessors(self, vid):
        return sorted({self.edges[eid].src for eid in self._in.get(vid, [])})

    def successors(self, vid):
        return sorted({self.edges[eid].dst for eid in self._out.get(vid, [])})

    def descendants(self, vid):
        seen = set()
        stack = [vid]
        while stack:
            u = stack.pop()
            for w in self.successors(u):
                if w not in seen:
                    seen.add(w)
                    stack.append(w)
        return seen

    def find_cycle(self):
        """Return a cycle as a list of vertex IDs, or None if acyclic."""
        indeg = {vid: 0 for vid in self.vertices}
        for e in self.edges.values():
            indeg[e.dst] += 1
        ready = [v for v, d in indeg.items() if d == 0]
        done = set()
        while ready:
            u = ready.pop()
            done.add(u)
            for eid in self._out.get(u, []):
                w = self.edges[eid].dst
                indeg[w] -= 1
                if indeg[w] == 0:
                    ready.append(w)
        remaining = [v for v in self.vertices if v not in done]
        if not remaining:
            return None
        remaining_set = set(remaining)
        path = []
        on_path = set()
        visited = set()

        def dfs(u):
            path.append(u)
            on_path.add(u)
            visited.add(u)
            for eid in self._out.get(u, []):
                w = self.edges[eid].dst
                if w not in remaining_set:
                    continue
                if w in on_path:
                    return path[path.index(w):] + [w]
                if w not in visited:
                    r = dfs(w)
                    if r:
                        return r
            path.pop()
            on_path.discard(u)
            return None

        for root in remaining:
            if root not in visited:
                cyc = dfs(root)
                if cyc:
                    return cyc
        return remaining

    def levels(self):
        """Topological execution layers (Kahn layering, multi-edge aware)."""
        indeg = {vid: 0 for vid in self.vertices}
        for e in self.edges.values():
            indeg[e.dst] += 1
        current = sorted(v for v, d in indeg.items() if d == 0)
        out = []
        while current:
            out.append(current)
            nxt = []
            for u in current:
                for eid in self._out.get(u, []):
                    w = self.edges[eid].dst
                    indeg[w] -= 1
                    if indeg[w] == 0:
                        nxt.append(w)
            current = sorted(nxt)
        return out


@dataclass
class Checkpoint:
    version: int
    now: float
    journal: frozenset
    state: dict
    vertex_epoch: dict
    attempt_counter: dict


class _Hooks:
    """Crash-injection / observability hooks used by the controllable scheduler."""

    def __init__(self):
        self.before_state_commit = None


class Engine:
    def __init__(self, quotas=None):
        self.graph = Graph()
        self.version = 0
        self.quotas = dict(quotas or {})
        self.state = {}
        self.vertex_epoch = {}
        self.attempt_counter = {}
        self.current_attempt = {}  # vid -> (attempt_id, epoch)
        self.used = {}
        self.now = 0.0
        self._events = []
        self._seq = 0
        self.outcomes = {}
        self.journal = set()
        self.effects_executed = []
        self.effects_skipped = []
        self.unlocked_log = []
        self.stale_log = []
        self.replanned_log = []
        self.started_log = []
        self.finished_log = []
        self.cancelled_log = []
        self.invalidated_attempts = {}
        self.rejected_updates = []
        self.hooks = _Hooks()
        self._unlocked_seen = set()

    # ------------------------------------------------------------------ update

    def apply_update(self, update):
        """Atomically validate and commit a graph update.

        Raises UpdateRejected (leaving the graph untouched) on any violation.
        Returns the affected vertex set on commit.
        """
        new_graph = self.graph.copy()
        reasons = []
        for v in update.add_vertices:
            if v.vid in new_graph.vertices:
                reasons.append(f"duplicate vertex id '{v.vid}'")
            else:
                new_graph.add_vertex(v)
        for e in update.add_edges:
            if e.eid in new_graph.edges:
                reasons.append(f"duplicate edge id '{e.eid}'")
                continue
            missing = [x for x in (e.src, e.dst) if x not in new_graph.vertices]
            if missing:
                reasons.append(f"edge '{e.eid}': unknown vertices {missing}")
                continue
            st = self.state.get(e.dst)
            if st is not None and st != VertexState.PENDING:
                reasons.append(
                    f"edge '{e.eid}': target '{e.dst}' already started "
                    f"(state={st.name}); new dependencies require a "
                    f"not-yet-started node"
                )
            new_graph.add_edge(e)
        cycle = new_graph.find_cycle()
        if cycle:
            reasons.append(f"cycle detected: {' -> '.join(cycle)}")
        for vid, v in new_graph.vertices.items():
            for res, amt in v.resources.items():
                quota = self.quotas.get(res, 0.0)
                if amt > quota:
                    reasons.append(
                        f"vertex '{vid}' requires {amt} of '{res}' "
                        f"exceeding quota {quota}"
                    )
        if reasons:
            raise UpdateRejected(reasons)

        self.graph = new_graph
        self.version += 1
        for v in update.add_vertices:
            self.state[v.vid] = VertexState.PENDING
            self.vertex_epoch[v.vid] = self.version
            self.attempt_counter.setdefault(v.vid, 0)
        affected = set()
        for e in update.add_edges:
            affected.add(e.src)
            affected.add(e.dst)
            affected |= new_graph.descendants(e.dst)
        for vid in sorted(affected):
            self.vertex_epoch[vid] = self.version
            st = self.state.get(vid)
            if st == VertexState.RUNNING:
                att_id, _ = self.current_attempt.pop(vid)
                self.invalidated_attempts[att_id] = (
                    f"graph updated to v{self.version}"
                )
                self._release(vid)
            if st is not None and st != VertexState.PENDING:
                self.state[vid] = VertexState.PENDING
                self.replanned_log.append((self.version, vid))
        return affected

    # ----------------------------------------------------------------- control

    def set_outcome(self, vid, attempt_number, outcome):
        self.outcomes[(vid, attempt_number)] = outcome

    def cancel(self, vid):
        att = self.current_attempt.pop(vid, None)
        if att is not None:
            self.invalidated_attempts[att[0]] = "cancelled"
            self._release(vid)
        if self.state.get(vid) == VertexState.RUNNING:
            self.state[vid] = VertexState.CANCELLED
        self.cancelled_log.append((vid, self.now))

    def retry(self, vid):
        if self.state.get(vid) in (VertexState.FAILED, VertexState.CANCELLED):
            self.state[vid] = VertexState.PENDING

    def schedule_update(self, time, update):
        self._push(time, ("update", update))

    # ------------------------------------------------------------------- clock

    def advance_to(self, t):
        while self._events and self._events[0][0] <= t:
            time, _, payload = heapq.heappop(self._events)
            self.now = time
            if payload[0] == "completion":
                _, vid, attempt_id, outcome = payload
                self.report_completion(vid, attempt_id, outcome)
            elif payload[0] == "update":
                try:
                    self.apply_update(payload[1])
                except UpdateRejected as exc:
                    self.rejected_updates.append(exc)
            self.settle()
        self.now = t
        self.settle()

    def run_to_completion(self):
        while self._events:
            self.advance_to(self._events[0][0])

    # -------------------------------------------------------------- scheduling

    def settle(self):
        for vid in sorted(self.graph.vertices):
            if self.state.get(vid) != VertexState.PENDING:
                continue
            if self._deps_unmet(vid) or self._stage_blockers(vid):
                continue
            key = (vid, self.vertex_epoch[vid])
            if key not in self._unlocked_seen:
                self._unlocked_seen.add(key)
                self.unlocked_log.append((vid, self.now, self.version))
        progress = True
        while progress:
            progress = False
            for vid in sorted(self.graph.vertices):
                if self.state.get(vid) != VertexState.PENDING:
                    continue
                if (
                    self._deps_unmet(vid)
                    or self._stage_blockers(vid)
                    or self._resource_shortage(vid)
                ):
                    continue
                self._start(vid)
                progress = True

    def report_completion(self, vid, attempt_id, outcome):
        att = self.current_attempt.get(vid)
        if att is None or att[0] != attempt_id:
            reason = self.invalidated_attempts.get(
                attempt_id, "unknown or superseded attempt"
            )
            self.stale_log.append((vid, attempt_id, reason, self.now))
            return False
        if att[1] != self.vertex_epoch[vid]:
            self.stale_log.append(
                (vid, attempt_id,
                 f"epoch {att[1]} != current {self.vertex_epoch[vid]}", self.now)
            )
            return False
        self._release(vid)
        del self.current_attempt[vid]
        if outcome == "ok":
            self._confirm_effects(vid)
            if self.hooks.before_state_commit is not None:
                self.hooks.before_state_commit(vid)
            self.state[vid] = VertexState.SUCCEEDED
        else:
            self.state[vid] = VertexState.FAILED
        self.finished_log.append((vid, attempt_id, outcome, self.now))
        return True

    # ------------------------------------------------------------- checkpoint

    def checkpoint(self):
        return Checkpoint(
            version=self.version,
            now=self.now,
            journal=frozenset(self.journal),
            state=dict(self.state),
            vertex_epoch=dict(self.vertex_epoch),
            attempt_counter=dict(self.attempt_counter),
        )

    def restore(self, cp):
        self.version = cp.version
        self.now = cp.now
        self.journal = set(cp.journal)
        self.state = dict(cp.state)
        self.vertex_epoch = dict(cp.vertex_epoch)
        self.attempt_counter = dict(cp.attempt_counter)
        # In-flight attempts are lost on crash; their late reports become stale.
        for vid, st in list(self.state.items()):
            if st == VertexState.RUNNING:
                self.state[vid] = VertexState.PENDING
        self.current_attempt.clear()
        self.used = {}

    # ----------------------------------------------------------------- evidence

    def levels(self):
        return self.graph.levels()

    def blocking_report(self):
        report = {}
        for vid in sorted(self.graph.vertices):
            st = self.state.get(vid)
            if st == VertexState.FAILED:
                report[vid] = "attempt failed; awaiting retry"
            elif st == VertexState.CANCELLED:
                report[vid] = "cancelled; awaiting retry"
            elif st == VertexState.PENDING:
                reason = self._block_reason(vid)
                if reason:
                    report[vid] = reason
        return report

    # ----------------------------------------------------------------- internal

    def _push(self, time, payload):
        self._seq += 1
        heapq.heappush(self._events, (time, self._seq, payload))

    def _start(self, vid):
        n = self.attempt_counter.get(vid, 0) + 1
        self.attempt_counter[vid] = n
        attempt_id = f"{vid}#{n}"
        self.current_attempt[vid] = (attempt_id, self.vertex_epoch[vid])
        self.state[vid] = VertexState.RUNNING
        self._allocate(vid)
        outcome = self.outcomes.get((vid, n), "ok")
        self._push(
            self.now + self.graph.vertices[vid].duration,
            ("completion", vid, attempt_id, outcome),
        )
        self.started_log.append((vid, attempt_id, self.now))

    def _allocate(self, vid):
        for res, amt in self.graph.vertices[vid].resources.items():
            self.used[res] = self.used.get(res, 0.0) + amt

    def _release(self, vid):
        for res, amt in self.graph.vertices[vid].resources.items():
            self.used[res] = self.used.get(res, 0.0) - amt

    def _confirm_effects(self, vid):
        for eff in self.graph.vertices[vid].effects:
            if eff in self.journal:
                self.effects_skipped.append(eff)
            else:
                self.effects_executed.append(eff)
                self.journal.add(eff)

    def _deps_unmet(self, vid):
        return [
            p for p in self.graph.predecessors(vid)
            if self.state.get(p) != VertexState.SUCCEEDED
        ]

    def _stage_blockers(self, vid):
        stage = self.graph.vertices[vid].stage
        return sorted(
            u for u, vv in self.graph.vertices.items()
            if vv.stage < stage and self.state.get(u) != VertexState.SUCCEEDED
        )

    def _resource_shortage(self, vid):
        out = []
        for res, amt in self.graph.vertices[vid].resources.items():
            avail = self.quotas.get(res, 0.0) - self.used.get(res, 0.0)
            if amt > avail + 1e-9:
                out.append(f"'{res}' need {amt}, available {avail}")
        return out

    def _block_reason(self, vid):
        parts = []
        unmet = self._deps_unmet(vid)
        if unmet:
            parts.append(f"waiting on predecessors: {unmet}")
        blockers = self._stage_blockers(vid)
        if blockers:
            parts.append(f"stage barrier: earlier stages not complete: {blockers}")
        shortage = self._resource_shortage(vid)
        if shortage:
            parts.append(f"insufficient resources: {', '.join(shortage)}")
        return "; ".join(parts)
