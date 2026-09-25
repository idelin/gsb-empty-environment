"""Computation graph, nodes, and the atomic backward engine."""

import threading

from .errors import (
    BackwardCancelled,
    GraphReleasedError,
    GraphStructureError,
    StaleInputError,
)

_local = threading.local()


def current_graph():
    return getattr(_local, "graph", None)


class CancelToken:
    """Cooperative cancellation for a backward transaction."""

    def __init__(self):
        self._event = threading.Event()

    def cancel(self):
        self._event.set()

    @property
    def cancelled(self):
        return self._event.is_set()


class Node:
    """One operation in a graph.

    ``saved`` tensors are pinned and their versions recorded at forward
    time; backward re-validates the versions so in-place mutation of a
    saved input is reported instead of silently producing wrong grads.

    A node may instead carry a ``recompute`` callable plus an ``rng_key``:
    nothing extra is saved and the backward pass deterministically
    recomputes what it needs from the (version-checked) live inputs.
    """

    __slots__ = (
        "graph", "name", "inputs", "output", "backward_fn",
        "saved", "saved_versions", "recompute", "rng_key",
    )

    def __init__(self, graph, name, inputs, output, backward_fn,
                 saved=(), recompute=None, rng_key=None):
        self.graph = graph
        self.name = name
        self.inputs = tuple(inputs)
        self.output = output
        self.backward_fn = backward_fn
        self.saved = tuple(saved)
        self.saved_versions = tuple(t.version for t in self.saved)
        self.recompute = recompute
        self.rng_key = rng_key
        for t in self.saved:
            t._storage.pin()

    def check_versions(self):
        for t, v in zip(self.saved, self.saved_versions):
            if t.version != v:
                raise StaleInputError(
                    f"node '{self.name}': saved tensor "
                    f"'{t.name or '<unnamed>'}' was modified in-place "
                    f"(forward v{v}, now v{t.version})"
                )

    def release(self):
        for t in self.saved:
            t._storage.unpin()


class Graph:
    """An independent computation graph with atomic backward transactions."""

    def __init__(self, name=""):
        self.name = name
        self.nodes = []
        self._released = False
        self._consumed = False  # a non-retained backward already ran
        self._lock = threading.RLock()

    def __enter__(self):
        self._prev = current_graph()
        _local.graph = self
        return self

    def __exit__(self, *exc):
        _local.graph = getattr(self, "_prev", None)
        return False

    # -- lifecycle -------------------------------------------------------
    @property
    def released(self):
        return self._released

    def _ensure_alive(self):
        if self._released:
            raise GraphReleasedError(f"graph '{self.name}' has been released")

    def add_node(self, node):
        with self._lock:
            self._ensure_alive()
            self.nodes.append(node)

    def release(self):
        """Free saved storage.  Idempotency is NOT provided: a second
        release (or a concurrent loser) raises GraphReleasedError."""
        with self._lock:
            self._ensure_alive()
            self._released = True
            nodes, self.nodes = self.nodes, []
        for n in nodes:
            n.release()

    # -- backward --------------------------------------------------------
    def _topo_order(self, output):
        order, visited = [], set()

        def visit(t):
            node = t._node
            if node is None or id(node) in visited:
                return
            if node.graph is not self:
                raise GraphStructureError(
                    "output depends on a tensor from another graph")
            visited.add(id(node))
            for inp in node.inputs:
                visit(inp)
            order.append(node)

        visit(output)
        if not order and output._node is None:
            raise GraphStructureError("output is not part of this graph")
        return order

    def backward(self, output, grad=None, retain_graph=False,
                 cancel_token=None):
        """Reverse-mode accumulation as an atomic transaction.

        Phase 1 computes every node's gradient contribution into private
        buffers; phase 2 commits them into leaf ``.grad`` fields.  Any
        failure (stale input, injected op error, cancellation) aborts
        before the commit, so previously committed gradients are never
        polluted.  Each shared node executes its backward logic exactly
        once per call.
        """
        with self._lock:
            self._ensure_alive()
            if self._consumed:
                raise GraphReleasedError(
                    "graph was not retained; a second backward is refused")
            order = self._topo_order(output)

            if grad is None:
                if output.size != 1:
                    raise GraphStructureError(
                        "grad required for non-scalar output")
                grad = [1.0]
            grad = list(grad)

            pending = {id(output): [output, grad]}
            executions = 0
            try:
                for node in reversed(order):
                    if cancel_token is not None and cancel_token.cancelled:
                        raise BackwardCancelled(
                            f"backward of '{self.name}' cancelled")
                    node.check_versions()
                    _, gout = pending.pop(id(node.output))
                    contributions = node.backward_fn(gout, node)
                    executions += 1
                    for t, g in contributions:
                        if not t.requires_grad:
                            continue
                        slot = pending.get(id(t))
                        if slot is None:
                            pending[id(t)] = [t, list(g)]
                        else:
                            acc = slot[1]
                            for i in range(len(acc)):
                                acc[i] += g[i]
            except Exception:
                # Rollback: nothing was committed, graph state unchanged.
                raise

            # Commit phase: fold buffers into leaf gradients.
            for t, g in pending.values():
                if t._node is not None or not t.requires_grad:
                    continue
                if t.grad is None:
                    from .tensor import Tensor
                    t.grad = Tensor(g, shape=t.shape)
                else:
                    for i in range(len(g)):
                        t.grad.data[i] += g[i]

            if not retain_graph:
                self._consumed = True
                self.release()
            return executions
