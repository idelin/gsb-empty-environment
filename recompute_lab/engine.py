"""Execution engine: forward with selective saving, backward with recompute.

Properties enforced here:

* deterministic recompute -- every random op derives its rng from
  ``Random(f"{seed}:{node_name}")`` freshly created at each (re)computation,
  so a recomputed random op reproduces the original forward bit-for-bit;
* side-effect guard -- a node flagged ``side_effect=True`` raises unless it is
  also flagged ``allow_recompute=True`` whenever a plan would recompute it;
* identity & version checks -- every stored activation records
  ``(id(tensor), storage_id, version)``; a backward read re-validates all
  three, which catches in-place mutation through shared views;
* peak memory includes temporaries -- gradient buffers allocated during
  backward are tracked by the same ``MemoryTracker`` and appear in the event
  trace, which ``simulator.verify_trace`` can independently validate.
"""
from __future__ import annotations

import random
from dataclasses import dataclass, field

import minitensor as mt
from minitensor import Tensor


class StaleActivationError(RuntimeError):
    pass


class SideEffectRecomputeError(RuntimeError):
    pass


@dataclass(frozen=True)
class Node:
    name: str
    op: str
    inputs: tuple = ()
    param: object = None
    side_effect: bool = False
    allow_recompute: bool = False


class MemoryTracker:
    """Counts live tensor elements; records the peak and an event trace."""

    def __init__(self):
        self.live = 0
        self.peak = 0
        self.events = []

    def alloc(self, name, size, kind="alloc"):
        self.live += size
        self.peak = max(self.peak, self.live)
        self.events.append((kind, name, size))

    def free(self, name, size, kind="free"):
        self.live -= size
        self.events.append((kind, name, size))


# ---------------------------------------------------------------------------
# op implementations: forward returns (output, ctx); backward returns one grad
# tensor per input.  ``ctx`` is rebuilt on every recompute.
# ---------------------------------------------------------------------------

def _fwd_add(engine, node, args, rng):
    return mt.add(args[0], args[1]), None


def _bwd_add(engine, node, ctx, grad, args):
    return [grad.clone(), grad.clone()]


def _fwd_mul(engine, node, args, rng):
    return mt.mul(args[0], args[1]), None


def _bwd_mul(engine, node, ctx, grad, args):
    return [mt.mul(grad, args[1]), mt.mul(grad, args[0])]


def _fwd_scale(engine, node, args, rng):
    return mt.scale(args[0], node.param), None


def _bwd_scale(engine, node, ctx, grad, args):
    return [mt.scale(grad, node.param)]


def _fwd_matmul(engine, node, args, rng):
    return mt.matmul(args[0], args[1]), None


def _bwd_matmul(engine, node, ctx, grad, args):
    return [
        mt.matmul(grad, mt.transpose(args[1])),
        mt.matmul(mt.transpose(args[0]), grad),
    ]


def _fwd_relu(engine, node, args, rng):
    return mt.relu(args[0]), None


def _bwd_relu(engine, node, ctx, grad, args):
    mask = mt.Tensor.zeros(args[0].shape)
    for i, v in enumerate(args[0].tolist_flat()):
        mask.storage.data[i] = 1.0 if v > 0.0 else 0.0
    return [mt.mul(grad, mask)]


def _fwd_tanh(engine, node, args, rng):
    out = mt.tanh(args[0])
    return out, out  # ctx keeps the output for the derivative


def _bwd_tanh(engine, node, ctx, grad, args):
    out = ctx
    one = mt.ones(out.shape)
    return [mt.mul(grad, mt.sub(one, mt.mul(out, out)))]


def _fwd_sum(engine, node, args, rng):
    return mt.sum_all(args[0]), None


def _bwd_sum(engine, node, ctx, grad, args):
    return [mt.full(args[0].shape, grad[0])]


def _fwd_dropout(engine, node, args, rng):
    out, mask = mt.dropout(args[0], node.param, rng)
    return out, mask


def _bwd_dropout(engine, node, ctx, grad, args):
    return [mt.mul(grad, ctx)]


def _fwd_reshape(engine, node, args, rng):
    return args[0].reshape(node.param), None  # a view: shares storage


def _bwd_reshape(engine, node, ctx, grad, args):
    return [grad.reshape(args[0].shape)]


OPS = {
    "add": (_fwd_add, _bwd_add),
    "mul": (_fwd_mul, _bwd_mul),
    "scale": (_fwd_scale, _bwd_scale),
    "matmul": (_fwd_matmul, _bwd_matmul),
    "relu": (_fwd_relu, _bwd_relu),
    "tanh": (_fwd_tanh, _bwd_tanh),
    "sum": (_fwd_sum, _bwd_sum),
    "dropout": (_fwd_dropout, _bwd_dropout),
    "reshape": (_fwd_reshape, _bwd_reshape),
}

RANDOM_OPS = {"dropout"}


class Engine:
    def __init__(self, nodes, seed=0):
        self.nodes = {n.name: n for n in nodes}
        self.order = [n.name for n in nodes]  # required to be topological
        self.parents = {n.name: list(n.inputs) for n in nodes}
        self.seed = seed
        self.output = self.order[-1]

    # -- rng -----------------------------------------------------------------
    def _rng_for(self, name):
        return random.Random(f"{self.seed}:{name}")

    # -- forward ---------------------------------------------------------------
    def _compute(self, name, env, ctxs):
        node = self.nodes[name]
        args = [env[i] for i in node.inputs]
        fwd, _ = OPS[node.op]
        rng = self._rng_for(name) if node.op in RANDOM_OPS else None
        out, ctx = fwd(self, node, args, rng)
        env[name] = out
        ctxs[name] = ctx
        return out

    def run(self, inputs, checkpoints, grad_output=None, forward_hook=None):
        """Full forward+backward.  Returns (output, grads, stats).

        ``checkpoints`` is the set of node names whose activations persist;
        everything else is freed as soon as possible and recomputed on demand
        during the backward pass.
        """
        checkpoints = set(checkpoints) | {self.output}
        tracker = MemoryTracker()
        env, ctxs = {}, {}
        meta = {}  # name -> (id, storage_id, version) recorded at store time
        sizes = {}

        consumers = {v: [] for v in self.order}
        for v in self.order:
            for p in self.parents[v]:
                consumers[p].append(v)
        pos = {v: i for i, v in enumerate(self.order)}
        fwd_last_use = {
            v: max([pos[c] for c in consumers[v]], default=pos[v]) for v in self.order
        }
        bwd_last_use = {
            v: min([pos[v]] + [pos[c] for c in consumers[v]]) for v in self.order
        }

        def store(name):
            t = env[name]
            sizes[name] = t.numel()
            meta[name] = (id(t), t.storage_id, t.version)
            tracker.alloc(name, sizes[name])

        def release(name):
            tracker.free(name, sizes[name])
            env.pop(name, None)
            ctxs.pop(name, None)
            meta.pop(name, None)

        def check(name):
            t = env[name]
            want = meta[name]
            got = (id(t), t.storage_id, t.version)
            if got != want:
                raise StaleActivationError(
                    f"activation {name!r} changed identity or version: "
                    f"stored {want}, found {got} (in-place mutation through a "
                    f"shared view?)"
                )

        # ---- forward ---------------------------------------------------------
        for i, v in enumerate(self.order):
            node = self.nodes[v]
            if node.op == "input":
                env[v] = inputs[v]
                ctxs[v] = None
            else:
                self._compute(v, env, ctxs)
            store(v)
            for u in list(env):
                if u not in checkpoints and u != v and fwd_last_use[u] <= i:
                    release(u)

        if forward_hook is not None:
            forward_hook(env)
        output = env[self.output]

        # ---- backward ----------------------------------------------------------
        grads = {v: None for v in self.order}
        grads[self.output] = (
            grad_output if grad_output is not None else mt.ones(env[self.output].shape)
        )
        recompute_counts = {v: 0 for v in self.order}

        def ensure_live(v):
            if v in env:
                return
            # collect the missing ancestor cone
            missing = set()

            def visit(x):
                if x in env or x in missing:
                    return
                missing.add(x)
                for p in self.parents[x]:
                    visit(p)

            visit(v)
            for u in self.order:  # topo order: parents recomputed first
                if u not in missing:
                    continue
                node = self.nodes[u]
                if node.op == "input":
                    raise StaleActivationError(
                        f"input {u!r} was freed; inputs must be checkpoints"
                    )
                if node.side_effect and not node.allow_recompute:
                    raise SideEffectRecomputeError(
                        f"node {u!r} has side effects and may not be recomputed"
                    )
                self._compute(u, env, ctxs)
                recompute_counts[u] += 1
                store(u)

        for i in range(len(self.order) - 1, -1, -1):
            v = self.order[i]
            g = grads[v]
            if g is None:
                continue
            ensure_live(v)
            check(v)
            node = self.nodes[v]
            if node.op == "input":
                continue
            args = []
            for p in node.inputs:
                ensure_live(p)
                check(p)
                args.append(env[p])
            _, bwd = OPS[node.op]
            tracker.alloc(f"grad:{v}", g.numel(), kind="grad_alloc")  # temp
            input_grads = bwd(self, node, ctxs[v], g, args)
            tracker.free(f"grad:{v}", g.numel(), kind="grad_free")
            for p, pg in zip(node.inputs, input_grads):
                grads[p] = pg if grads[p] is None else mt.add(grads[p], pg)
            for u in list(env):
                if u not in checkpoints and bwd_last_use[u] >= i:
                    release(u)

        stats = {
            "peak_memory": tracker.peak,
            "recompute_counts": {k: c for k, c in recompute_counts.items() if c},
            "total_recomputes": sum(recompute_counts.values()),
            "events": tracker.events,
        }
        input_grads = {v: grads[v] for v in self.order if self.nodes[v].op == "input"}
        return output, input_grads, stats
