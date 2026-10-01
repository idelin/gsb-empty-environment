"""Independent per-batch reference implementation.

Recomputes everything from the raw dataset with its own gradient and SGD
code (no shared accumulation / shard / coordinator logic) so the Trainer
can be cross-checked end to end under a fault schedule.
"""

from __future__ import annotations

from .tensor import Tensor


def _full_batch_grad(params, dataset, indices):
    """Mean gradient of the MSE loss over the given samples, computed in one
    pass directly from the dataset (independent of gradacc.model)."""
    w, b = params["w"], params["b"]
    out_dim, in_dim = w.shape
    gw = [0.0] * (out_dim * in_dim)
    gb = [0.0] * out_dim
    for idx in indices:
        x, target = dataset[idx]
        for o in range(out_dim):
            pred = sum(w.data[o * in_dim + i] * x[i] for i in range(in_dim))
            pred += b.data[o]
            err = pred - target[o]
            gb[o] += 2.0 * err
            for i in range(in_dim):
                gw[o * in_dim + i] += 2.0 * err * x[i]
    n = float(len(indices))
    return (Tensor([g / n for g in gw], w.shape),
            Tensor([g / n for g in gb], b.shape))


def run_reference(init_params, dataset, batches, events, lr, momentum):
    """Replay an event schedule with dead-simple, obviously-correct logic."""
    params = {k: v.copy() for k, v in init_params.items()}
    velocity = {k: Tensor.zeros(v.shape) for k, v in params.items()}
    step = 0
    version = 0
    open_batches = {}  # bid -> {"version", "micros": {mid: indices}, "corrupt": set}
    closed = {}        # bid -> status string
    for event in events:
        op = event[0]
        if op == "begin":
            bid = event[1]
            if bid not in closed and bid not in open_batches:
                open_batches[bid] = {"version": version, "micros": {},
                                     "corrupt": set()}
        elif op in ("micro", "corrupt"):
            _, bid, mid = event[:3]
            ob = open_batches[bid]
            ob["micros"][mid] = list(batches[bid][mid])
            if op == "corrupt":
                ob["corrupt"].add(mid)
            else:
                ob["corrupt"].discard(mid)  # clean retry replaces corrupt copy
        elif op == "revoke":
            _, bid, mid = event
            ob = open_batches[bid]
            ob["micros"].pop(mid, None)
            ob["corrupt"].discard(mid)
        elif op == "zero":
            ob = open_batches[event[1]]
            ob["micros"].clear()
            ob["corrupt"].clear()
            ob["version"] = version
        elif op == "commit":
            bid = event[1]
            if bid in closed:
                continue  # idempotent duplicate commit
            ob = open_batches.pop(bid)
            if ob["version"] != version:
                closed[bid] = "rejected_stale"
                continue
            if not ob["micros"]:
                closed[bid] = "rejected_empty"
                continue
            if ob["corrupt"] & ob["micros"].keys():
                closed[bid] = "skipped_overflow"
                continue
            indices = [i for mid in sorted(ob["micros"])
                       for i in ob["micros"][mid]]
            gw, gb = _full_batch_grad(params, dataset, indices)
            for name, grad in (("w", gw), ("b", gb)):
                vel = velocity[name]
                param = params[name]
                nv = [momentum * v + g for v, g in zip(vel.data, grad.data)]
                velocity[name] = Tensor(nv, vel.shape)
                params[name] = Tensor([p - lr * v
                                       for p, v in zip(param.data, nv)],
                                      param.shape)
            step += 1
            version += 1
            closed[bid] = "committed"
        elif op in ("arm_crash",):
            pass  # fault-injection marker: no semantic effect
        else:
            raise ValueError(f"unknown event op {op!r}")
    return {"params": params, "velocity": velocity, "step": step,
            "version": version, "closed": closed}
