"""Straightforward per-sample reference renderer.

Deliberately independent from the block scheduler: no block segmentation,
events are applied at exact sample indices inside a single naive loop,
transactions are validated atomically, and the routing topology is
recomputed every sample. Used to verify ``Scheduler`` output
sample-by-sample within ``fixtures.TOLERANCE``.
"""
from __future__ import annotations

import math

from .graph import RoutingGraph
from .scheduler import MAX_GAIN


def _alpha(cutoff, sample_rate):
    return 1.0 - math.exp(-2.0 * math.pi * cutoff / sample_rate)


def reference_render(sample_rate, tracks, sources, master, events,
                     num_samples, default_edges=None):
    nodes = list(tracks) + [master]
    if default_edges is None:
        default_edges = [(t, master) for t in tracks]
    graph = RoutingGraph(nodes)
    for s, d in default_edges:
        graph.connect(s, d)
    nyquist = sample_rate / 2.0
    gain = {n: 1.0 for n in nodes}
    cutoff = {n: nyquist for n in nodes}
    z = {n: 0.0 for n in nodes}

    by_pos = {}
    for e in events:
        slot = by_pos.setdefault(e.position, {})
        cur = slot.get(e.event_id)
        if cur is None or e.version > cur.version:
            slot[e.event_id] = e

    out = []
    for i in range(num_samples):
        group = by_pos.get(i)
        if group:
            # On rollback the old state is kept; either outcome is legit.
            _, gain, cutoff, graph = _try_transaction(
                group, gain, cutoff, graph, sample_rate, nodes)
        order = graph.topo_order()
        incoming = {n: [s for s in nodes if n in graph.edges[s]]
                    for n in nodes}
        values = {}
        for n in order:
            src = sources.get(n)
            acc = src[i] if src is not None else 0.0
            for prev in incoming[n]:
                acc += values[prev]
            a = _alpha(cutoff[n], sample_rate)
            z[n] = z[n] + a * (acc - z[n])
            values[n] = z[n] * gain[n]
        out.append(values[master])
    return out


def _try_transaction(group, gain, cutoff, graph, sample_rate, nodes):
    """Atomic validate+apply. On any failure the old state is returned."""
    new_gain = dict(gain)
    new_cutoff = dict(cutoff)
    new_graph = graph.copy()
    for e in sorted(group.values(), key=lambda e: e.event_id):
        p = e.params
        if e.kind == "gain":
            node, g = p.get("track"), p.get("gain")
            if (node not in new_gain or not isinstance(g, (int, float))
                    or not math.isfinite(g) or not 0.0 <= g <= MAX_GAIN):
                return False, gain, cutoff, graph
            new_gain[node] = float(g)
        elif e.kind == "filter":
            node, c = p.get("track"), p.get("cutoff")
            if (node not in new_cutoff or not isinstance(c, (int, float))
                    or not math.isfinite(c)
                    or not 0.0 < c <= sample_rate / 2.0):
                return False, gain, cutoff, graph
            new_cutoff[node] = float(c)
        elif e.kind == "route":
            op, s, d = p.get("op"), p.get("src"), p.get("dst")
            if s not in nodes or d not in nodes:
                return False, gain, cutoff, graph
            if op == "connect":
                if d in new_graph.edges[s]:
                    return False, gain, cutoff, graph
                new_graph.connect(s, d)
            elif op == "disconnect":
                if d not in new_graph.edges[s]:
                    return False, gain, cutoff, graph
                new_graph.disconnect(s, d)
            else:
                return False, gain, cutoff, graph
        else:
            return False, gain, cutoff, graph
    if new_graph.has_cycle():
        return False, gain, cutoff, graph
    return True, new_gain, new_cutoff, new_graph
