"""Gradient checkpointing / recomputation.

The forward pass runs with graph construction suspended under a bound random
key, so only the *inputs* and a :class:`RecomputeNode` are remembered.  During
backward the whole segment is replayed under that same key (reproducing
 stochastic draws), differentiated on a private throwaway graph, and the input
gradients flow back into the outer transaction.  A saved input version counter
guarantees a mutated captured input aborts the outer backward.
"""

from __future__ import annotations

from .graph import Graph, Node, current_graph, is_grad_enabled
from .rng import rng_key


class RecomputeNode(Node):
    __slots__ = ("func", "rng_seed", "full_args")

    def __init__(self, name, graph, inputs, outputs, func, rng_seed, full_args):
        super().__init__(
            name=name,
            graph=graph,
            inputs=inputs,
            outputs=outputs,
            backward_fn=self._replay,
            saved=inputs,
            metadata={"kind": "recompute"},
        )
        self.func = func
        self.rng_seed = rng_seed
        self.full_args = full_args

    def _replay(self, out_grads, node):
        # Rebuild the segment on a private graph with the same key.  This runs
        # as a nested, non-committing atomic transaction: its staging buffer is
        # discarded entirely if replay (or any inner backward) fails.
        inner_graph = Graph(name="checkpoint-replay")
        outputs = None
        with inner_graph.use(), rng_key(self.rng_seed):
            outputs = self.func(*self.full_args)
        if not isinstance(outputs, tuple):
            outputs = (outputs,)

        from .engine import _run_engine

        staged = _run_engine(
            roots=list(outputs),
            root_grads=list(out_grads),
            graph=inner_graph,
            retain_graph=False,
            cancel=None,
            commit=False,
        )
        from .tensor import Tensor

        return [Tensor(staged[t], t.shape) for t in self.inputs]


def checkpoint(func, *args, seed=1234, name="checkpoint"):
    """Apply ``func`` with its internals omitted from the saved graph."""
    graph = current_graph(strict=True)
    tracked = [t for t in args if getattr(t, "requires_grad", False)]

    if not is_grad_enabled() or not tracked:
        return func(*args)

    # Opaque forward: no inner nodes are recorded; stochastic draws are pinned.
    from .graph import no_grad
    from .tensor import Tensor

    with no_grad(), rng_key(seed):
        raw = func(*args)
    multi = isinstance(raw, tuple)
    raw_outputs = raw if multi else (raw,)

    # Wrap in fresh tensors (copying the computed values) owned by the graph.
    outputs = tuple(
        Tensor(list(t.data), t.shape, requires_grad=True) for t in raw_outputs
    )
    node = RecomputeNode(
        name=name,
        graph=graph,
        inputs=tuple(tracked),
        outputs=outputs,
        func=func,
        rng_seed=int(seed),
        full_args=tuple(args),
    )
    for out in outputs:
        out.grad_fn = node
        out.graph = graph
    graph.add_node(node)
    return outputs if multi else outputs[0]
