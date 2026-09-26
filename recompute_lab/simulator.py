"""Independent event-level simulation of a checkpoint plan.

This module is deliberately separate from the execution engine: it consumes an
abstract graph (topological order + parent lists + activation sizes + recompute
costs) and a checkpoint set, produces an event trace, and validates that no
event ever reads an activation that has already been freed.  It also computes
the peak memory, *including* the temporary gradient buffers allocated during
the backward pass.

Event kinds (tuples ``(kind, node, size)``):
    alloc      activation of ``node`` becomes live
    free       activation of ``node`` is released
    read       backward (or forward consumer) reads activation of ``node``
    grad_alloc temporary gradient buffer for ``node`` is allocated
    grad_free  temporary gradient buffer for ``node`` is released
"""
from __future__ import annotations

from dataclasses import dataclass, field


class ReadAfterFreeError(RuntimeError):
    pass


class InvalidTraceError(RuntimeError):
    pass


@dataclass
class SimulationResult:
    feasible: bool
    peak_memory: float
    recompute_cost: float
    recompute_counts: dict = field(default_factory=dict)
    events: list = field(default_factory=list)


def _transitive_missing(target, parents, live):
    """Nodes that must be recomputed so that ``target`` becomes live.

    Returns the missing ancestors (including ``target``) in an order that
    respects dependencies (parents before children), or ``None`` if some
    required ancestor can never be recovered (not live, not a source... every
    non-live node is recoverable as long as the recursion bottoms out at live
    nodes, which it always does when sources are checkpoints).
    """
    missing = set()

    def visit(v):
        if v in live or v in missing:
            return
        missing.add(v)
        for p in parents[v]:
            visit(p)

    visit(target)
    return missing


def simulate_plan(order, parents, sizes, costs, checkpoints, budget):
    """Simulate forward + backward under ``checkpoints`` and validate liveness."""
    pos = {v: i for i, v in enumerate(order)}
    consumers = {v: [] for v in order}
    for v in order:
        for p in parents[v]:
            consumers[p].append(v)

    checkpoints = set(checkpoints)
    # Forward liveness: a non-checkpoint activation dies right after its last
    # consumer is computed.  Nodes with no consumers (outputs) stay live until
    # the backward pass reaches them.
    fwd_last_use = {
        v: max([pos[c] for c in consumers[v]], default=pos[v]) for v in order
    }
    # Backward liveness: processing node v (reverse topo) reads act(v) and
    # act(parents(v)); the last backward read of u happens at the smallest
    # topo position among {u} u consumers(u).
    bwd_last_use = {
        v: min([pos[v]] + [pos[c] for c in consumers[v]]) for v in order
    }

    events = []
    live = set()
    mem = 0
    peak = 0
    recompute_cost = 0.0
    recompute_counts = {v: 0 for v in order}

    def alloc(v, kind="alloc"):
        nonlocal mem, peak
        live.add(v)
        mem += sizes[v]
        peak = max(peak, mem)
        events.append((kind, v, sizes[v]))

    def free(v, kind="free"):
        nonlocal mem
        if v not in live:
            raise InvalidTraceError(f"double free of {v!r}")
        live.discard(v)
        mem -= sizes[v]
        events.append((kind, v, sizes[v]))

    def read(v):
        if v not in live:
            raise ReadAfterFreeError(f"backward read of freed activation {v!r}")
        events.append(("read", v, sizes[v]))

    def ensure_live(v):
        """Recompute the missing ancestor cone of ``v`` from live nodes."""
        nonlocal recompute_cost
        if v in live:
            return
        missing = _transitive_missing(v, parents, live)
        for u in order:  # topo order guarantees parents recomputed first
            if u in missing:
                for p in parents[u]:
                    if p not in live:
                        raise ReadAfterFreeError(
                            f"recompute of {u!r} needs {p!r} which is not live"
                        )
                alloc(u, kind="recompute_alloc")
                recompute_cost += costs[u]
                recompute_counts[u] += 1

    # ---- forward -----------------------------------------------------------
    for i, v in enumerate(order):
        alloc(v)
        for u in list(live):
            if u not in checkpoints and u != v and fwd_last_use[u] <= i:
                free(u)

    # ---- backward (reverse topological order) ------------------------------
    grad_live = set()

    def grad_alloc(v):
        nonlocal mem, peak
        grad_live.add(v)
        mem += sizes[v]
        peak = max(peak, mem)
        events.append(("grad_alloc", v, sizes[v]))

    def grad_free(v):
        nonlocal mem
        grad_live.discard(v)
        mem -= sizes[v]
        events.append(("grad_free", v, sizes[v]))

    for i in range(len(order) - 1, -1, -1):
        v = order[i]
        ensure_live(v)
        read(v)
        grad_alloc(v)  # temporary gradient buffer, counted in the peak
        for p in parents[v]:
            ensure_live(p)
            read(p)
        grad_free(v)
        # release activations whose last backward use has passed
        for u in list(live):
            if u not in checkpoints and bwd_last_use[u] >= i and u not in grad_live:
                # u is needed by nodes at positions <= bwd_last_use[u]; if that
                # position is >= i we have just processed its final reader.
                free(u)

    return SimulationResult(
        feasible=peak <= budget,
        peak_memory=peak,
        recompute_cost=recompute_cost,
        recompute_counts={k: c for k, c in recompute_counts.items() if c},
        events=events,
    )


def verify_trace(events, sizes):
    """Standalone checker: replay an event trace and enforce liveness rules.

    Raises :class:`ReadAfterFreeError` / :class:`InvalidTraceError` on any
    violation; returns the peak memory otherwise.  Used to independently
    validate traces emitted by the real execution engine.
    """
    live = set()
    mem = 0
    peak = 0
    for kind, node, size in events:
        if kind in ("alloc", "recompute_alloc", "grad_alloc"):
            if node in live and kind != "grad_alloc":
                raise InvalidTraceError(f"double alloc of {node!r}")
            live.add((kind, node) if kind == "grad_alloc" else node)
            mem += size
            peak = max(peak, mem)
        elif kind in ("free", "recompute_free", "grad_free"):
            key = ("grad_alloc", node) if kind == "grad_free" else node
            if key not in live:
                raise InvalidTraceError(f"free of non-live {node!r}")
            live.discard(key)
            mem -= size
        elif kind == "read":
            if node not in live:
                raise ReadAfterFreeError(f"read of freed activation {node!r}")
        else:
            raise InvalidTraceError(f"unknown event kind {kind!r}")
    if mem < 0:
        raise InvalidTraceError("negative memory")
    return peak
