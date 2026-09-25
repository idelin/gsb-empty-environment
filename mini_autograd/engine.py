"""Topological backward execution with atomic gradient transactions."""

from __future__ import annotations

from .errors import (
    BackwardAbortedError,
    GraphReleasedError,
    InvalidBackwardError,
)


def _run_engine(roots, root_grads, graph, retain_graph, cancel, commit):
    """Execute reverse-topological backward on ``graph``.

    Gradients accumulate in a private ``staged`` dict and are only published to
    ``Tensor.grad`` (when ``commit``) after every node has succeeded.  Any
    exception -- injected operator error, stale saved input, or ``cancel`` --
    discards the whole staging buffer, so a failed transaction never pollutes a
    previously committed gradient.
    """

    # ---- build reverse-topological order ---------------------------------
    order = []
    visiting = set()

    def visit(node):
        if id(node) in visiting:
            return
        visiting.add(id(node))
        for tensor in node.inputs:
            producer = tensor.grad_fn
            if producer is not None and producer.graph is graph:
                visit(producer)
        order.append(node)

    for root in roots:
        node = root.grad_fn
        if node is not None and node.graph is graph:
            visit(node)

    exec_order = list(reversed(order))

    # ---- seed root gradients ---------------------------------------------
    staged = {}  # Tensor -> flat list
    for root, root_grad in zip(roots, root_grads):
        g = staged.get(root)
        if g is None:
            staged[root] = list(root_grad)
        else:
            staged[root] = [a + b for a, b in zip(g, root_grad)]

    def accumulate(tensor, values):
        cur = staged.get(tensor)
        if cur is None:
            staged[tensor] = list(values)
        else:
            staged[tensor] = [a + b for a, b in zip(cur, values)]

    # ---- run nodes (staging only; no leaf is touched on failure) ---------
    try:
        for node in exec_order:
            out_grads = []
            for out in node.outputs:
                if out not in staged:
                    raise InvalidBackwardError(
                        f"internal: missing gradient for output of {node.name}"
                    )
                out_grads.append(staged[out])
            if cancel is not None and cancel(node):
                raise BackwardAbortedError(
                    f"backward cancelled at node '{node.name}'"
                )
            in_grads = node.run(out_grads)
            targets = node.inputs
            if len(in_grads) != len(targets):
                raise InvalidBackwardError(
                    f"node '{node.name}' returned {len(in_grads)} gradients "
                    f"for {len(targets)} requires_grad inputs"
                )
            for target, grad in zip(targets, in_grads):
                accumulate(target, grad.data)
    except BaseException:
        staged.clear()
        raise

    # ---- commit or hand gradients back to a nested engine ----------------
    if commit:
        leaves = [t for t in staged if t.is_leaf and t.requires_grad]
        for leaf in leaves:
            leaf.add_grad(staged[leaf])

    if not retain_graph:
        graph.release()

    return staged


def backward(root, gradient=None, retain_graph=False, cancel=None):
    """Run an atomic backward transaction from scalar tensor ``root``."""
    from .tensor import Tensor

    if not isinstance(root, Tensor):
        raise InvalidBackwardError("backward root must be a Tensor")

    graph = root.graph

    if root.is_leaf:
        # Root is itself a leaf (e.g. a raw parameter): there is no graph to
        # traverse; seed the leaf directly and atomically commit.
        if root.requires_grad:
            seed = _default_seed(root, gradient)
            if graph is not None and graph.released:
                raise GraphReleasedError("graph was already released")
            root.add_grad(seed)
        if graph is not None and not retain_graph:
            graph.release()
        return

    if graph is None:
        raise InvalidBackwardError("tensor does not require grad / has no graph")

    with graph.lock:
        if graph.released:
            raise GraphReleasedError(
                "graph was released by a previous backward(retain_graph=False); "
                "rebuild the forward pass or pass retain_graph=True"
            )
        if not root.requires_grad:
            raise InvalidBackwardError("cannot backward a tensor with requires_grad=False")
        seed = _default_seed(root, gradient)
        _run_engine(
            roots=[root],
            root_grads=[seed],
            graph=graph,
            retain_graph=retain_graph,
            cancel=cancel,
            commit=True,
        )


def backward_multi(roots, gradients=None, retain_graph=False, cancel=None):
    """Backward from several scalar roots of the same graph in one transaction."""
    from .tensor import Tensor

    graphs = {r.graph for r in roots if not r.is_leaf}
    if len(graphs) != 1:
        raise InvalidBackwardError("all roots must belong to the same graph")
    (graph,) = graphs
    if gradients is None:
        gradients = [None] * len(roots)

    with graph.lock:
        if graph.released:
            raise GraphReleasedError("graph was already released")
        seeds = [_default_seed(r, g) for r, g in zip(roots, gradients)]
        _run_engine(
            roots=list(roots),
            root_grads=seeds,
            graph=graph,
            retain_graph=retain_graph,
            cancel=cancel,
            commit=True,
        )


def _default_seed(root, gradient):
    from .tensor import Tensor

    if gradient is None:
        if root.shape != ():
            raise InvalidBackwardError(
                "grad can be implicitly created only for scalar outputs"
            )
        return [1.0]
    if isinstance(gradient, Tensor):
        if gradient.shape != root.shape:
            raise InvalidBackwardError("gradient shape must match root shape")
        return list(gradient.data)
    vals = [float(v) for v in gradient]
    if len(vals) != len(root.data):
        raise InvalidBackwardError("gradient length must match root")
    return vals
