"""Batch transform: lift a single-sample pure graph into a batched graph.

Design:
  * Every mapped axis is canonicalised to the *front* (leading batch axes),
    so per-sample ops keep operating on trailing (feature) dims and nested
    transforms compose by stacking another leading axis.
  * Shared inputs (in_axes=None) keep their shape; broadcasting against
    leading batch axes makes per-sample reuse automatic, and reverse-mode
    "unbroadcast" sums their gradients across samples.
  * Ops that cannot preserve sample independence (side effects, cross-sample
    collectives) are rejected with the shortest dependency path back to a
    mapped input.
  * Every decision is recorded in a JSON-serialisable Certificate.
"""
from __future__ import annotations

import json
from collections import deque
from dataclasses import asdict, dataclass, field

from . import tensor as T
from .graph import (ELEMENTWISE_BINARY, ELEMENTWISE_UNARY, SIDE_EFFECT_OPS,
                    Graph, GraphBuilder, infer_shape)


class BatchTransformError(Exception):
    """Raised when a graph cannot be batch-transformed.

    Carries ``certificate`` describing the rejection (reason plus minimal
    dependency path) or the validation failure.
    """

    def __init__(self, message: str, certificate: "Certificate"):
        super().__init__(message)
        self.certificate = certificate


@dataclass
class Certificate:
    """Transformation certificate: axis tracking + audit trail."""
    status: str = "ok"
    batch_size: int | None = None
    in_axes: dict = field(default_factory=dict)
    mapped_inputs: list = field(default_factory=list)
    shared_inputs: list = field(default_factory=list)
    axis_moves: list = field(default_factory=list)
    node_axes: dict = field(default_factory=dict)   # node -> {"batch_dims", "shape"}
    outputs: dict = field(default_factory=dict)
    rejected: list = field(default_factory=list)
    notes: list = field(default_factory=list)

    def to_json(self) -> str:
        return json.dumps(asdict(self), indent=2, sort_keys=True)


def _dependency_path(graph: Graph, target: str, preferred_roots: set) -> list:
    """Shortest path from a graph input to ``target`` (BFS over input edges).

    Prefers paths rooted at a mapped input; falls back to any graph input.
    """
    parents = {n.name: list(n.inputs) for n in graph.nodes.values()}

    def bfs(roots):
        prev = {target: None}
        queue = deque([target])
        while queue:
            cur = queue.popleft()
            if cur in roots:
                path = [cur]
                while prev[path[-1]] is not None:
                    path.append(prev[path[-1]])
                return path
            for parent in parents.get(cur, []):
                if parent not in prev:
                    prev[parent] = cur
                    queue.append(parent)
        return None

    path = bfs(preferred_roots)
    if path is None:
        path = bfs(set(graph.inputs))
    return path if path is not None else [target]


def batch_transform(graph: Graph, in_axes: dict, batched_shapes: dict):
    """Transform ``graph`` into a batched graph.

    ``in_axes``: input name -> mapped axis (int) or None / missing for shared
    inputs.  ``batched_shapes``: actual shapes of the (already batched) inputs;
    shared inputs must keep their single-sample shape.

    Returns ``(batched_graph, certificate)``.  Raises ``BatchTransformError``
    (carrying the rejection certificate) when the transform is impossible.
    """
    cert = Certificate()
    axes = {name: in_axes.get(name) for name in graph.inputs}

    def fail(message, rejected=None):
        cert.status = "rejected"
        if rejected is not None:
            cert.rejected.append(rejected)
        raise BatchTransformError(message, cert)

    # ---- early validation: shapes and batch-size agreement -----------------
    batch_size = None
    for name in graph.inputs:
        axis = axes[name]
        if name not in batched_shapes:
            fail(f"missing shape for input {name!r}")
        actual = tuple(int(s) for s in batched_shapes[name])
        single = tuple(graph.input_shapes[name])
        if axis is None:
            if actual != single:
                fail(f"shared input {name!r} has shape {actual}, expected {single}")
            continue
        if not actual or not (-len(actual) <= axis < len(actual)):
            fail(f"mapped axis {axis} out of range for input {name!r} with shape {actual}")
        axis %= len(actual)
        axes[name] = axis
        if len(actual) != len(single) + 1:
            fail(f"mapped input {name!r} has batched shape {actual}, "
                 f"expected rank {len(single) + 1} (sample shape {single})")
        if actual[:axis] + actual[axis + 1:] != single:
            fail(f"mapped input {name!r}: batched shape {actual} incompatible "
                 f"with sample shape {single}")
        if batch_size is None:
            batch_size = actual[axis]
        elif actual[axis] != batch_size:
            fail(f"batch size mismatch: input {name!r} has {actual[axis]} samples, "
                 f"expected {batch_size}",
                 rejected={"node": name, "op": "input",
                           "reason": "batch size mismatch across mapped inputs",
                           "dependency_path": [name]})

    cert.batch_size = batch_size
    cert.in_axes = dict(axes)
    cert.mapped_inputs = [n for n in graph.inputs if axes[n] is not None]
    cert.shared_inputs = [n for n in graph.inputs if axes[n] is None]

    # ---- transform ----------------------------------------------------------
    builder = GraphBuilder()
    state = {}  # old node name -> (new node name, batch_ndim, batched shape)

    for name in graph.inputs:
        axis = axes[name]
        shape = tuple(batched_shapes[name])
        builder.input(name, shape)
        if axis is None:
            state[name] = (name, 0, shape)
            cert.node_axes[name] = {"batch_dims": [], "shape": list(shape)}
        elif axis == 0:
            state[name] = (name, 1, shape)
            cert.node_axes[name] = {"batch_dims": [0], "shape": list(shape)}
        else:
            # rearrange: move the mapped axis to the front
            perm = (axis,) + tuple(i for i in range(len(shape)) if i != axis)
            new_shape = tuple(shape[p] for p in perm)
            moved = builder.add_node("transpose", (name,), {"perm": perm},
                                     name=f"{name}__batch_front")
            cert.axis_moves.append({"input": name, "from_axis": axis,
                                    "to_axis": 0, "node": moved})
            cert.node_axes[name] = {"batch_dims": [axis], "shape": list(shape)}
            cert.node_axes[moved] = {"batch_dims": [0], "shape": list(new_shape)}
            state[name] = (moved, 1, new_shape)

    for node in graph.nodes.values():
        if node.op == "input":
            continue
        if node.op in SIDE_EFFECT_OPS:
            path = _dependency_path(graph, node.name, set(cert.mapped_inputs))
            fail(f"cannot batch-transform node {node.name!r}: "
                 f"{SIDE_EFFECT_OPS[node.op]}",
                 rejected={"node": node.name, "op": node.op,
                           "reason": SIDE_EFFECT_OPS[node.op],
                           "dependency_path": path})
        in_states = [state[i] for i in node.inputs]
        in_names = [s[0] for s in in_states]
        in_shapes = [s[2] for s in in_states]
        bnd = max((s[1] for s in in_states), default=0)
        if len({s[1] for s in in_states if s[1] > 0}) > 1:
            fail(f"misaligned batch axes at node {node.name!r}")
        op = node.op
        params = dict(node.params)
        if op in ELEMENTWISE_BINARY + ELEMENTWISE_UNARY or op == "matmul":
            pass  # leading batch axes broadcast naturally
        elif op == "transpose":
            orig_rank = len(in_shapes[0]) - bnd
            perm = tuple(p % orig_rank for p in params["perm"])
            params["perm"] = tuple(range(bnd)) + tuple(p + bnd for p in perm)
        elif op == "reshape":
            lead = in_shapes[0][:bnd]
            rest = tuple(params["shape"])
            if T.numel(rest) != T.numel(in_shapes[0][bnd:]):
                fail(f"reshape at node {node.name!r} would alter the mapped axis")
            params["shape"] = lead + rest
        elif op in ("reduce_sum", "reduce_mean"):
            orig_rank = len(in_shapes[0]) - bnd
            params["axis"] = params["axis"] % orig_rank + bnd
        else:
            fail(f"no batching rule for op {op!r} at node {node.name!r}")
        out_shape = infer_shape(op, in_shapes, params)
        builder.add_node(op, tuple(in_names), params, name=node.name)
        state[node.name] = (node.name, bnd, out_shape)
        cert.node_axes[node.name] = {"batch_dims": list(range(bnd)),
                                     "shape": list(out_shape)}

    out_names = []
    for o in graph.outputs:
        new_name, bnd, shape = state[o]
        out_names.append(new_name)
        cert.outputs[o] = {"node": new_name, "batch_dims": list(range(bnd)),
                           "shape": list(shape)}
    batched = builder.build(out_names)
    cert.notes.append("mapped axes canonicalised to leading positions")
    cert.notes.append("shared-input gradients are summed over batch axes by "
                      "reverse-mode unbroadcast")
    return batched, cert
