"""Independent verification: per-sample reference loops + finite differences."""
from __future__ import annotations

from . import tensor as T
from .graph import evaluate
from .grad import value_and_grad


def _mapped(in_axes):
    return {k: v for k, v in in_axes.items() if v is not None}


def _batch_size(env, mapped):
    sizes = {env[k].shape[a % len(env[k].shape)] for k, a in mapped.items()}
    if len(sizes) != 1:
        raise ValueError(f"inconsistent batch sizes: {sizes}")
    return sizes.pop()


def _slice_env(env, mapped, i):
    return {k: (T.slice_along(v, mapped[k], i) if k in mapped else v)
            for k, v in env.items()}


def per_sample_loop(graph, in_axes, env):
    """Reference: run the (single-sample) graph one sample at a time and
    stack each output along a fresh leading axis."""
    mapped = _mapped(in_axes)
    size = _batch_size(env, mapped)
    collected = {o: [] for o in graph.outputs}
    for i in range(size):
        values = evaluate(graph, _slice_env(env, mapped, i))
        for o in graph.outputs:
            collected[o].append(values[o])
    return {o: T.stack(ts, axis=0) for o, ts in collected.items()}


def per_sample_grads(graph, in_axes, env, wrt):
    """Reference gradients: per-sample reverse mode.

    Gradients w.r.t. mapped inputs are stacked along a leading axis;
    gradients w.r.t. shared inputs are summed across samples.
    """
    mapped = _mapped(in_axes)
    size = _batch_size(env, mapped)
    stacked = {w: [] for w in wrt if w in mapped}
    summed = {}
    for i in range(size):
        _, grads = value_and_grad(graph, wrt, _slice_env(env, mapped, i))
        for w in wrt:
            if w in mapped:
                stacked[w].append(grads[w])
            else:
                summed[w] = grads[w] if w not in summed else T.add(summed[w], grads[w])
    # stack per-sample grads, then move the sample axis back to the mapped
    # axis position so layouts match the batched graph's gradients
    out = {w: T.moveaxis(T.stack(ts, axis=0), 0, mapped[w])
           for w, ts in stacked.items()}
    out.update(summed)
    return out


def compare_batched_vs_loop(single_graph, in_axes, env, wrt, batched_graph,
                            tol: float = 1e-9):
    """Compare a batched graph against the per-sample reference, forward
    *and* reverse (never forward-only)."""
    report = {}
    batched_values = evaluate(batched_graph, env)
    loop_outs = per_sample_loop(single_graph, in_axes, env)
    for o in single_graph.outputs:
        report[f"forward:{o}"] = T.max_abs_diff(batched_values[o], loop_outs[o])
    _, batched_grads = value_and_grad(batched_graph, wrt, env)
    loop_grads = per_sample_grads(single_graph, in_axes, env, wrt)
    for w in wrt:
        report[f"grad:{w}"] = T.max_abs_diff(batched_grads[w], loop_grads[w])
    report["ok"] = all(v <= tol for k, v in report.items())
    return report


def finite_difference_grad(graph, wrt, env, loss_output: str | None = None,
                           eps: float = 1e-5):
    """Independent numerical gradients via central finite differences of
    ``sum(loss_output)``.  No graph machinery is reused here."""
    loss_output = loss_output or graph.outputs[0]

    def total_loss(e):
        return T.sum_all(evaluate(graph, e)[loss_output])

    grads = {}
    for w in wrt:
        base = env[w]
        flat = []
        for i in range(len(base.data)):
            plus = dict(env)
            minus = dict(env)
            tp = T.Tensor(list(base.data), base.shape)
            tm = T.Tensor(list(base.data), base.shape)
            tp.data[i] += eps
            tm.data[i] -= eps
            plus[w] = tp
            minus[w] = tm
            flat.append((total_loss(plus) - total_loss(minus)) / (2.0 * eps))
        grads[w] = T.Tensor(flat, base.shape)
    return grads
