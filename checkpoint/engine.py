"""Execution engine: checkpointed forward, recomputing backward.

* Forward stores only the planned checkpoints; other activations are
  freed as soon as their last forward consumer has run.
* Backward recomputes missing activations from the nearest stored
  ancestors, restoring the RNG snapshot taken before the recomputed
  segment so stochastic ops replay bit-exactly.  Every recomputed tensor
  is checked against the forward-pass audit; a mismatch raises.
* Nodes with side effects cannot be recomputed unless declared
  `replayable`; otherwise RecomputeError is raised.
* Shared views are detected by storage identity (aliases are recorded and
  cost 0 bytes) and every read verifies the storage version.
* All allocations (activations, gradient buffers, temporaries) go through
  MemoryTracker, so the peak includes temporary gradients.  Every
  alloc/free/read is emitted as an event for the independent simulator.
"""
from __future__ import annotations

from .graph import topo_order
from .rng import Rng
from .tensor import Tensor, add as tensor_add


class RecomputeError(RuntimeError):
    pass


class AuditMismatch(RuntimeError):
    pass


class MemoryTracker:
    def __init__(self):
        self.current = 0
        self.peak = 0
        self.live = {}
        self.events = []
        self._tmp = 0

    def alloc(self, tag, size):
        if tag in self.live:
            raise KeyError(f"double alloc {tag}")
        self.live[tag] = size
        self.current += size
        self.peak = max(self.peak, self.current)
        self.events.append(("alloc", tag, size))

    def free(self, tag):
        if tag not in self.live:
            raise KeyError(f"free of unknown {tag}")
        size = self.live.pop(tag)
        self.current -= size
        self.events.append(("free", tag, size))

    def read(self, tag):
        if tag not in self.live:
            raise KeyError(f"read of unallocated {tag}")
        self.events.append(("read", tag, 0))

    def tmp_tag(self):
        self._tmp += 1
        return f"tmp:{self._tmp}"


class Stats:
    def __init__(self):
        self.recompute_count = 0      # number of node recomputations
        self.recompute_cost = 0.0     # sum of node.cost over recomputations
        self.peak_bytes = 0           # incl. gradient + temporary buffers
        self.checkpoints = ()         # stored activation names
        self.checkpoint_bytes = 0

    def __repr__(self):
        return (f"Stats(peak_bytes={self.peak_bytes}, "
                f"recompute_count={self.recompute_count}, "
                f"recompute_cost={self.recompute_cost}, "
                f"checkpoints={len(self.checkpoints)})")


class RunResult:
    def __init__(self, output, grads, stats, events, audit, aliases):
        self.output = output          # sink Tensor
        self.grads = grads            # grads w.r.t. graph inputs
        self.stats = stats
        self.events = events
        self.audit = audit            # name -> (shape, tuple(values))
        self.aliases = aliases        # view node -> base activation name


class Engine:
    def __init__(self, nodes, input_names):
        self.nodes = {n.name: n for n in nodes}
        self.input_names = list(input_names)
        self.topo = topo_order(list(nodes), self.input_names)
        self.consumers = {nm: [] for nm in
                          list(self.input_names) + [n.name for n in nodes]}
        for n in nodes:
            for dep in n.inputs:
                self.consumers[dep].append(n.name)

    def run(self, inputs, checkpoints=(), seed=0, sink_grad=None):
        tracker = MemoryTracker()
        rng = Rng(seed)
        stats = Stats()
        acts = {}
        aliases = {}
        audit = {}
        pre_state = {}
        stored = set(checkpoints) | set(self.input_names)

        def store_act(name, tensor):
            size = tensor.nbytes
            for other_name, other in acts.items():
                if tensor.is_view_of(other):
                    aliases[name] = other_name   # identity check
                    size = 0                     # shared storage: no new bytes
                    break
            acts[name] = tensor
            tracker.alloc("act:" + name, size)

        def drop_act(name):
            if name in acts and name not in stored:
                tracker.free("act:" + name)
                del acts[name]

        # forward pass
        fwd_remaining = {nm: len(self.consumers[nm]) for nm in self.consumers}
        for nm in self.input_names:
            store_act(nm, inputs[nm])
        for name in self.topo:
            node = self.nodes[name]
            pre_state[name] = rng.snapshot()
            args = []
            for dep in node.inputs:
                tracker.read("act:" + dep)
                acts[dep].check_version()
                args.append(acts[dep])
            out = node.fwd(rng, *args)
            audit[name] = (out.shape, tuple(out.data()))
            store_act(name, out)
            for dep in node.inputs:
                fwd_remaining[dep] -= 1
                if fwd_remaining[dep] == 0:
                    drop_act(dep)

        # backward pass
        grads = {}
        grad_tags = {}

        def set_grad(name, g):
            if name in grads:
                tmp = tracker.tmp_tag()
                tracker.alloc(tmp, g.nbytes)
                merged = tensor_add(grads[name], g)
                tracker.free(grad_tags[name])
                tracker.alloc(grad_tags[name], merged.nbytes)
                tracker.free(tmp)
                grads[name] = merged
            else:
                tag = "grad:" + name
                tracker.alloc(tag, g.nbytes)
                grad_tags[name] = tag
                grads[name] = g

        def recompute(targets):
            closure = {}

            def visit(u):
                if u in acts or u in self.input_names:
                    return
                if u in closure:
                    return
                for dep in self.nodes[u].inputs:
                    visit(dep)
                closure[u] = True

            for t in targets:
                visit(t)
            if not closure:
                return ()
            order = [nm for nm in self.topo if nm in closure]
            for nm in order:
                nd = self.nodes[nm]
                if nd.side_effect and not nd.replayable:
                    raise RecomputeError(
                        f"node {nm!r} has side effects and was not declared "
                        "replayable; its activation must be checkpointed")
            rng.restore(pre_state[order[0]])
            local_uses = {nm: 0 for nm in order}
            for nm in order:
                for dep in self.nodes[nm].inputs:
                    if dep in local_uses:
                        local_uses[dep] += 1
            for t in targets:
                if t in local_uses:
                    local_uses[t] += 1
            recomputed = []
            for nm in order:
                nd = self.nodes[nm]
                out = nd.fwd(rng, *[acts[d] for d in nd.inputs])
                if audit[nm] != (out.shape, tuple(out.data())):
                    raise AuditMismatch(
                        f"recompute of {nm!r} diverged from the forward pass")
                store_act(nm, out)
                recomputed.append(nm)
                stats.recompute_count += 1
                stats.recompute_cost += nd.cost
                for dep in nd.inputs:
                    if dep in local_uses:
                        local_uses[dep] -= 1
                        if local_uses[dep] == 0:
                            drop_act(dep)
            return tuple(recomputed)

        sink = self.topo[-1]
        if sink_grad is None:
            sink_grad = Tensor.full(audit[sink][0], 1.0)
        set_grad(sink, sink_grad)

        for name in reversed(self.topo):
            if name not in grads:
                continue
            node = self.nodes[name]
            needed = list(node.inputs) + [name]
            missing = [u for u in needed if u not in acts]
            ephemeral = recompute(missing) if missing else ()
            for u in needed:
                tracker.read("act:" + u)
                acts[u].check_version()
            g_out = grads[name]
            g_ins = node.bwd(g_out, [acts[d] for d in node.inputs], acts[name])
            for dep, g in zip(node.inputs, g_ins):
                set_grad(dep, g)
            tracker.free(grad_tags[name])
            del grads[name], grad_tags[name]
            for u in ephemeral:
                drop_act(u)

        result_grads = {nm: grads[nm] for nm in self.input_names if nm in grads}
        # release remaining buffers so the event log ends balanced
        for nm in list(acts):
            tracker.free("act:" + nm)
        for nm in list(grad_tags):
            tracker.free(grad_tags[nm])

        stats.peak_bytes = tracker.peak
        stats.checkpoints = tuple(sorted(stored))
        stats.checkpoint_bytes = sum(
            inputs[nm].nbytes for nm in self.input_names) + sum(
            8 * len(audit[nm][1]) for nm in checkpoints)
        out_shape, out_data = audit[sink]
        return RunResult(Tensor(list(out_data), out_shape), result_grads,
                         stats, tracker.events, audit, aliases)
