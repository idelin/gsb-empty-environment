"""Computation graphs, nodes, and the gradient-mode context stack."""

from __future__ import annotations

import threading


class Node:
    """One recorded differentiable operator application."""

    __slots__ = (
        "name",
        "graph",
        "inputs",
        "outputs",
        "saved",
        "saved_versions",
        "backward_fn",
        "metadata",
        "run_count",
    )

    def __init__(self, name, graph, inputs, outputs, backward_fn, saved=None,
                 metadata=None):
        self.name = name
        self.graph = graph
        self.inputs = tuple(inputs)
        self.outputs = tuple(outputs)
        self.backward_fn = backward_fn
        self.metadata = metadata or {}
        self.saved = tuple(saved) if saved is not None else tuple(inputs)
        self.saved_versions = tuple(t.version for t in self.saved)
        self.run_count = 0

    def check_versions(self):
        for tensor, version in zip(self.saved, self.saved_versions):
            if tensor.version != version:
                from .errors import VersionMismatchError

                raise VersionMismatchError(
                    f"tensor saved by node '{self.name}' was modified in place "
                    f"after forward (version {version} -> {tensor.version}); "
                    "refusing to run backward on stale data"
                )

    def run(self, out_grads):
        """Validate saved versions then execute the local backward rule once."""
        self.check_versions()
        self.run_count += 1
        result = self.backward_fn(out_grads, self)
        if result is None:
            result = []
        return list(result)


class Graph:
    """A rooted set of nodes built while this graph is active."""

    def __init__(self, name="graph"):
        self.name = name
        self.nodes = []
        self._released = False
        self.lock = threading.RLock()

    def add_node(self, node):
        self.nodes.append(node)

    @property
    def released(self):
        return self._released

    def is_active(self):
        return _state().current is self

    def use(self):
        return _GraphContext(self)

    def release(self):
        """Drop every forward-time reference the graph holds."""
        with self.lock:
            self._released = True
            for node in self.nodes:
                node.inputs = ()
                node.outputs = ()
                node.saved = ()
                node.saved_versions = ()
                node.backward_fn = None
                node.metadata = {}
            self.nodes = []

    def __repr__(self):
        state = "released" if self._released else f"{len(self.nodes)} nodes"
        return f"Graph({self.name!r}, {state})"


class _GraphState(threading.local):
    def __init__(self):
        self.stack = []
        self.grad_enabled = True

    @property
    def current(self):
        return self.stack[-1] if self.stack else None


_state = _GraphState()


class _GraphContext:
    def __init__(self, graph):
        self.graph = graph

    def __enter__(self):
        _state.stack.append(self.graph)
        return self.graph

    def __exit__(self, *exc):
        stack = _state.stack
        assert stack and stack[-1] is self.graph, "graph contexts exited out of order"
        stack.pop()
        return False


def current_graph(strict=False):
    graph = _state.current
    if strict and graph is None:
        from .errors import AutogradError

        raise AutogradError(
            "operation on a requires_grad tensor outside of any Graph context"
        )
    return graph


def is_grad_enabled():
    return _state.grad_enabled and _state.current is not None


class no_grad:
    """Suspend graph construction (values still compute, without history)."""

    def __enter__(self):
        self._prev = _state.grad_enabled
        _state.grad_enabled = False
        return self

    def __exit__(self, *exc):
        _state.grad_enabled = self._prev
        return False


class enable_grad:
    def __enter__(self):
        self._prev = _state.grad_enabled
        _state.grad_enabled = True
        return self

    def __exit__(self, *exc):
        _state.grad_enabled = self._prev
        return False
