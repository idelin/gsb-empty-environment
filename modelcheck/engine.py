"""Layer-synchronous BFS model checker.

Guarantees:
  * explores only the given finite-state model;
  * shortest (minimal-depth) counterexample traces, because dedup is a
    synchronous per-layer barrier: successor generation may run in
    parallel, but no state is admitted to the visited set or the next
    frontier before every expansion of the current layer has finished;
  * normalization never loses information (states are always expanded
    from their recovered concrete form);
  * hash collisions are resolved by comparing full original states;
  * budgets are enforced at layer granularity; on exhaustion the run is
    PENDING (never "safe") and the unexplored frontier is written out as
    evidence together with coverage statistics;
  * every layer boundary is checkpointed to disk, so a run can be
    resumed across layers without duplicates or omissions.
"""
from __future__ import annotations

import json
import os
import tempfile
import time
from concurrent.futures import ThreadPoolExecutor
from dataclasses import asdict, dataclass, field
from pathlib import Path

from .encoding import to_jsonable
from .frontier import LayeredFrontier
from .normalizers import IdentityNormalizer
from .store import CompressedStateStore

SAFE = "safe"
VIOLATED = "violated"
PENDING = "pending"


@dataclass
class Budget:
    max_states: int | None = None
    max_layers: int | None = None
    max_seconds: float | None = None


@dataclass
class CoverageStats:
    states_expanded: int = 0
    states_discovered: int = 0
    layers_completed: int = 0
    layer_sizes: list[int] = field(default_factory=list)
    elapsed_seconds: float = 0.0
    stop_reason: str = ""


@dataclass
class Trace:
    """A replayable counterexample: states[0] is initial, actions[i] leads
    from states[i] to states[i+1].  Length-0 trace = initial state violates."""
    states: list = field(default_factory=list)
    actions: list = field(default_factory=list)
    violated_invariant: str = ""

    def __len__(self) -> int:
        return len(self.actions)


@dataclass
class Result:
    status: str
    trace: Trace | None
    stats: CoverageStats
    run_dir: str
    frontier_evidence: str | None = None
    reachable: dict | None = None  # canonical key -> (state, distance), if collect=True


def run_bfs(model, *, normalizer=None, budget=None, run_dir=None, resume=False,
            workers=1, checkpoint_every=1, collect=False) -> Result:
    normalizer = normalizer or IdentityNormalizer()
    budget = budget or Budget()
    if run_dir is None:
        run_dir = tempfile.mkdtemp(prefix="modelcheck-")
    run_dir = Path(run_dir)
    run_dir.mkdir(parents=True, exist_ok=True)
    layers = LayeredFrontier(run_dir / "frontier")
    started = time.monotonic()

    if resume:
        store, parents, distances, stats, layer = _load_checkpoint(run_dir, collect)
        frontier = layers.read_layer(layer)
    else:
        store = CompressedStateStore()
        parents = {}
        distances = {} if collect else None
        stats = CoverageStats()
        layer = 0
        frontier = []
        for state0 in model.initial_states():
            key, recovery = normalizer.normalize(state0)
            if not store.add(key):
                continue
            parents[key] = (None, None, recovery)
            if distances is not None:
                distances[key] = 0
            frontier.append((key, recovery))
            bad = _violated_invariant(model, state0)
            if bad is not None:
                _finalize_stats(stats, started, "violated", store)
                trace = Trace(states=[state0], actions=[], violated_invariant=bad)
                return Result(VIOLATED, trace, stats, str(run_dir),
                              reachable=_reachable(normalizer, store, parents, distances))
        layers.write_layer(0, frontier)
        _save_checkpoint(run_dir, 0, store, parents, stats, distances)

    while True:
        if not frontier:
            _finalize_stats(stats, started, "explored", store)
            _save_checkpoint(run_dir, layer, store, parents, stats, distances)
            return Result(SAFE, None, stats, str(run_dir),
                          reachable=_reachable(normalizer, store, parents, distances))
        reason = _budget_reason(budget, stats, store, started)
        if reason is not None:
            _finalize_stats(stats, started, reason, store)
            evidence = _write_frontier_evidence(run_dir, layer, frontier, normalizer, stats, reason)
            _save_checkpoint(run_dir, layer, store, parents, stats, distances)
            return Result(PENDING, None, stats, str(run_dir), frontier_evidence=evidence,
                          reachable=_reachable(normalizer, store, parents, distances))

        # Expand the current layer (successor generation may be parallel).
        if workers > 1:
            with ThreadPoolExecutor(max_workers=workers) as pool:
                expansions = list(pool.map(lambda e: _expand(model, normalizer, e), frontier))
        else:
            expansions = [_expand(model, normalizer, entry) for entry in frontier]

        # Synchronous dedup barrier: all expansions of this layer are
        # complete before any admission decision, so BFS layer (distance)
        # semantics are preserved even with asynchronous generation.
        next_frontier = []
        stats.states_expanded += len(frontier)
        stats.layer_sizes.append(len(frontier))
        for parent_key, generated in expansions:
            for action, succ_state, key, recovery in generated:
                if not store.add(key):
                    continue
                parents[key] = (parent_key, action, recovery)
                if distances is not None:
                    distances[key] = layer + 1
                bad = _violated_invariant(model, succ_state)
                if bad is not None:
                    trace = _build_trace(normalizer, parents, key, bad)
                    _finalize_stats(stats, started, "violated", store)
                    return Result(VIOLATED, trace, stats, str(run_dir),
                                  reachable=_reachable(normalizer, store, parents, distances))
                next_frontier.append((key, recovery))

        stats.layers_completed += 1
        layer += 1
        layers.write_layer(layer, next_frontier)
        if checkpoint_every and layer % checkpoint_every == 0:
            _save_checkpoint(run_dir, layer, store, parents, stats, distances)
        # The on-disk layer file is the single source of truth: re-read it
        # exactly as a resumed run would.
        frontier = layers.read_layer(layer)


def replay_trace(model, normalizer, trace: Trace) -> bool:
    """Re-execute a trace against the model: initial state, every action
    must produce the recorded next state, and the final state must violate
    the recorded invariant."""
    normalizer = normalizer or IdentityNormalizer()
    initial_keys = {normalizer.normalize(s)[0] for s in model.initial_states()}
    if not trace.states or normalizer.normalize(trace.states[0])[0] not in initial_keys:
        return False
    current = trace.states[0]
    for action, nxt in zip(trace.actions, trace.states[1:]):
        want_key = normalizer.normalize(nxt)[0]
        found = None
        for candidate_action, succ in model.successors(current):
            if candidate_action == action and normalizer.normalize(succ)[0] == want_key:
                found = succ
                break
        if found is None:
            return False
        current = found
    predicates = dict(model.invariants())
    predicate = predicates.get(trace.violated_invariant)
    return predicate is not None and not predicate(current)


def _expand(model, normalizer, entry):
    key, recovery = entry
    state = normalizer.recover(key, recovery)  # recovery mapping in action
    generated = []
    for action, succ in model.successors(state):
        succ_key, succ_recovery = normalizer.normalize(succ)
        generated.append((action, succ, succ_key, succ_recovery))
    return key, generated


def _violated_invariant(model, state):
    for name, predicate in model.invariants():
        if not predicate(state):
            return name
    return None


def _build_trace(normalizer, parents, key, violated) -> Trace:
    chain = []
    actions = []
    current = key
    while current is not None:
        parent, action, recovery = parents[current]
        chain.append((current, recovery))
        if action is not None:
            actions.append(action)
        current = parent
    chain.reverse()
    actions.reverse()
    states = [normalizer.recover(k, rec) for k, rec in chain]
    return Trace(states=states, actions=actions, violated_invariant=violated)


def _reachable(normalizer, store, parents, distances):
    if distances is None:
        return None
    out = {}
    for key in store.keys():
        recovery = parents[key][2]
        out[key] = (normalizer.recover(key, recovery), distances[key])
    return out


def _budget_reason(budget, stats, store, started):
    if budget.max_layers is not None and stats.layers_completed >= budget.max_layers:
        return f"layer budget exhausted (max_layers={budget.max_layers})"
    if budget.max_states is not None and len(store) >= budget.max_states:
        return f"state budget exhausted (max_states={budget.max_states})"
    if budget.max_seconds is not None and time.monotonic() - started >= budget.max_seconds:
        return f"time budget exhausted (max_seconds={budget.max_seconds})"
    return None


def _finalize_stats(stats, started, reason, store):
    stats.states_discovered = len(store)
    stats.elapsed_seconds = time.monotonic() - started
    stats.stop_reason = reason


def _write_frontier_evidence(run_dir, layer, frontier, normalizer, stats, reason) -> str:
    entries = [
        {"key": key.hex(), "state": to_jsonable(normalizer.recover(key, recovery))}
        for key, recovery in frontier
    ]
    evidence = {
        "status": PENDING,
        "reason": reason,
        "unexplored_layer": layer,
        "unexplored_frontier": entries,
        "coverage": asdict(stats),
    }
    path = Path(run_dir) / "frontier_evidence.json"
    path.write_text(json.dumps(evidence, indent=2))
    return str(path)


def _save_checkpoint(run_dir, layer, store, parents, stats, distances):
    run_dir = Path(run_dir)
    stats.states_discovered = len(store)
    store.save(run_dir / "store.chk")
    with open(run_dir / "parents.chk", "w", encoding="utf-8") as handle:
        for key, (parent, action, recovery) in parents.items():
            handle.write(json.dumps({
                "key": key.hex(),
                "parent": parent.hex() if parent is not None else None,
                "action": action,
                "recovery": recovery.hex(),
            }) + "\n")
    if distances is not None:
        with open(run_dir / "distances.chk", "w", encoding="ascii") as handle:
            for key, dist in distances.items():
                handle.write(f"{key.hex()} {dist}\n")
    tmp = run_dir / "checkpoint.json.tmp"
    with open(tmp, "w", encoding="utf-8") as handle:
        json.dump({"layer": layer, "stats": asdict(stats)}, handle)
    os.replace(tmp, run_dir / "checkpoint.json")


def _load_checkpoint(run_dir, collect):
    run_dir = Path(run_dir)
    ckpt_path = run_dir / "checkpoint.json"
    if not ckpt_path.exists():
        raise FileNotFoundError(f"no checkpoint found in {run_dir}")
    ckpt = json.loads(ckpt_path.read_text())
    store = CompressedStateStore.load(run_dir / "store.chk")
    parents = {}
    with open(run_dir / "parents.chk", encoding="utf-8") as handle:
        for line in handle:
            line = line.strip()
            if not line:
                continue
            record = json.loads(line)
            parent = record["parent"]
            parents[bytes.fromhex(record["key"])] = (
                bytes.fromhex(parent) if parent is not None else None,
                record["action"],
                bytes.fromhex(record["recovery"]),
            )
    distances = None
    if collect:
        dist_path = run_dir / "distances.chk"
        if not dist_path.exists():
            raise ValueError("cannot resume with collect=True: original run did not "
                             "record distances")
        distances = {}
        for line in dist_path.read_text().splitlines():
            if not line.strip():
                continue
            key_hex, dist = line.split(" ")
            distances[bytes.fromhex(key_hex)] = int(dist)
    stats = CoverageStats(**ckpt["stats"])
    return store, parents, distances, stats, ckpt["layer"]
