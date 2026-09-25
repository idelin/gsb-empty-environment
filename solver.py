"""Exact solver (small scale) for partition placement / migration.

Two lexicographic objectives:
  1. minimise the maximum per-slot load (bytes);
  2. subject to that, minimise bytes that move relative to `current`.

Constraints solved jointly: per-slot capacity, partition affinity
(allowed slots), pairwise anti-affinity, and (at planning time) the
per-round move budget -- the budget is handled by the planner; here the
total moved bytes objective is what the budget bounds.

The search is branch-and-bound with exact integer arithmetic. When the
tree is exhausted the result is a proved optimum (gap == 0). A node
limit can stop the search early; the best feasible assignment together
with the proved lower bound and the remaining gap is then returned.
"""

from __future__ import annotations

from dataclasses import dataclass


class InfeasibleError(Exception):
    """No placement satisfies capacity / affinity / anti-affinity."""


class LimitReached(Exception):
    """Internal: node budget exhausted while the tree is still open."""


@dataclass(frozen=True)
class SolveResult:
    assignment: dict
    before_loads: dict
    loads: dict
    max_load: int
    moved_bytes: int
    max_load_lower_bound: int
    moved_bytes_lower_bound: int
    gap: int
    unmet: dict            # pid -> bytes that cannot be placed whole
    nodes: int
    proven_optimal: bool
    status: str


def capacity_bound_lower_bound(caps: list, total_weight: int) -> int:
    """Smallest L with sum(min(cap, L)) >= total_weight.

    Valid lower bound on the maximum load: under max load <= L every
    slot can hold at most min(capacity, L) bytes.
    """
    if total_weight <= 0:
        return 0
    lo, hi = 0, max(caps)
    while lo < hi:
        mid = (lo + hi) // 2
        usable = sum(min(c, mid) for c in caps)
        if usable >= total_weight:
            hi = mid
        else:
            lo = mid + 1
    return lo


@dataclass
class _Item:
    pid: str
    weight: int
    options: list  # sorted feasible slot indices
    current: "int | None"


def solve(cluster, node_limit: int = 1_000_000) -> SolveResult:
    slot_ids = sorted(cluster.slots)
    caps = [cluster.slots[sid].capacity for sid in slot_ids]
    sid_index = {sid: i for i, sid in enumerate(slot_ids)}
    m = len(slot_ids)

    unmet: dict = {}
    items: list[_Item] = []
    for pid in sorted(cluster.partitions):
        part = cluster.partitions[pid]
        allowed = cluster.allowed_slot_ids(pid)
        options = sorted(sid_index[s] for s in allowed if caps[sid_index[s]] >= part.weight)
        if not options:
            # Whole partition fits nowhere: it must never be fake-split.
            unmet[pid] = part.weight
            continue
        cur = cluster.current.get(pid)
        items.append(
            _Item(pid, part.weight, options, sid_index[cur] if cur is not None else None)
        )

    # Heavy first is a strong, deterministic branching order.
    items.sort(key=lambda it: (-it.weight, it.pid))
    n = len(items)
    weights = [it.weight for it in items]
    suffix = [0] * (n + 1)
    for i in range(n - 1, -1, -1):
        suffix[i] = suffix[i + 1] + weights[i]

    partners = {it.pid: cluster.conflict_partners(it.pid) for it in items}
    pid_item = {it.pid: k for k, it in enumerate(items)}

    total_weight = suffix[0]
    root_lb = max(
        max((it.weight for it in items), default=0),
        capacity_bound_lower_bound(caps, total_weight),
    )

    loads = [0] * m
    assign = [-1] * n
    slot_parts = [set() for _ in range(m)]
    nodes = 0
    aborted = False

    def feasible_options(k: int) -> list:
        """Options for item k that survive capacity and anti-affinity."""
        it = items[k]
        out = []
        for j in it.options:
            if loads[j] + it.weight <= caps[j] and not (partners[it.pid] & slot_parts[j]):
                out.append(j)
        return out

    # ---------------- phase 1: minimise maximum load ----------------
    best_max = None
    best_assign = None

    def dfs_max(k: int) -> None:
        nonlocal nodes, best_max, best_assign, aborted
        nodes += 1
        if nodes > node_limit:
            aborted = True
            raise LimitReached
        cur_max = max(loads) if m else 0
        if best_max is not None and cur_max >= best_max:
            return
        free = sum(caps[j] - loads[j] for j in range(m))
        if suffix[k] > free:
            return
        if k == n:
            if best_max is None or cur_max < best_max:
                best_max = cur_max
                best_assign = assign.copy()
            return
        opts = feasible_options(k)
        opts.sort(key=lambda j: (loads[j], j))  # best-fit, deterministic
        it = items[k]
        for j in opts:
            if best_max is not None and loads[j] + it.weight >= best_max:
                continue
            assign[k] = j
            loads[j] += it.weight
            slot_parts[j].add(it.pid)
            dfs_max(k + 1)
            slot_parts[j].discard(it.pid)
            loads[j] -= it.weight
            assign[k] = -1

    try:
        dfs_max(0)
    except LimitReached:
        pass
    phase1_exhausted = not aborted

    if best_assign is None:
        if aborted:
            raise LimitReached("node limit reached before any feasible assignment")
        raise InfeasibleError("no assignment satisfies capacity and placement constraints")

    if not phase1_exhausted:
        proven_lb_max = root_lb
    else:
        proven_lb_max = best_max  # tree exhausted => best_max is the optimum

    # ------------- phase 2: minimise bytes that must move ------------
    for k, j in enumerate(best_assign):
        assign[k] = j
        loads[j] += items[k].weight
        slot_parts[j].add(items[k].pid)

    best_cost = sum(
        items[k].weight for k, j in enumerate(best_assign) if items[k].current != j
    )
    cost_assign = best_assign.copy()
    phase2_exhausted = False
    nodes_phase2 = 0

    def static_forced_move(k: int) -> int:
        it = items[k]
        if it.current is None or it.current not in it.options:
            return it.weight
        return 0

    root_move_lb = sum(static_forced_move(k) for k in range(n))

    def dfs_move(k: int, cost: int) -> None:
        nonlocal nodes, nodes_phase2, best_cost, cost_assign, aborted
        nodes += 1
        nodes_phase2 += 1
        if nodes > node_limit:
            aborted = True
            raise LimitReached
        if cost >= best_cost:
            return
        # remaining weight must fit within capacity capped at best_max
        room = sum(min(caps[j], best_max) - loads[j] for j in range(m))
        if suffix[k] > room:
            return
        if k == n:
            best_cost = cost
            cost_assign = assign.copy()
            return
        # lower bound: current cost plus unassigned partitions forced to move
        lb = cost
        it = items[k]
        for r in range(k, n):
            rj = items[r].current
            forced = rj is None or rj not in items[r].options
            if not forced and rj is not None:
                # staying put would already overflow the optimal load cap
                forced = loads[rj] + items[r].weight > best_max
            if forced:
                lb += items[r].weight
        if lb >= best_cost:
            return
        opts = feasible_options(k)
        opts = [j for j in opts if loads[j] + it.weight <= best_max]
        # try the zero-cost slot first, then best-fit; keep determinism
        opts.sort(key=lambda j: (0 if j == it.current else 1, loads[j], j))
        for j in opts:
            add = 0 if it.current == j else it.weight
            if cost + add >= best_cost:
                continue
            assign[k] = j
            loads[j] += it.weight
            slot_parts[j].add(it.pid)
            dfs_move(k + 1, cost + add)
            slot_parts[j].discard(it.pid)
            loads[j] -= it.weight
            assign[k] = -1

    try:
        dfs_move(0, 0)
        phase2_exhausted = not aborted
    except LimitReached:
        phase2_exhausted = False

    # restore best assignment into a clean result map
    final = cost_assign if cost_assign is not None else best_assign
    assignment = {items[k].pid: slot_ids[j] for k, j in enumerate(final)}

    loads_after = {sid: 0 for sid in slot_ids}
    for k, j in enumerate(final):
        loads_after[slot_ids[j]] += items[k].weight
    before_loads = cluster.loads_for(cluster.current)
    moved = sum(items[k].weight for k, j in enumerate(final) if items[k].current != j)

    proven_optimal = phase1_exhausted and phase2_exhausted
    move_lb = moved if phase2_exhausted else max(root_move_lb, 0)
    status = "proven optimal" if proven_optimal else "open: node limit reached, gap not closed"

    return SolveResult(
        assignment=assignment,
        before_loads=before_loads,
        loads=loads_after,
        max_load=max(loads_after.values()) if loads_after else 0,
        moved_bytes=moved,
        max_load_lower_bound=min(proven_lb_max, max(loads_after.values()) if loads_after else 0)
        if not proven_optimal else proven_lb_max,
        moved_bytes_lower_bound=move_lb,
        gap=(max(loads_after.values()) if loads_after else 0) - proven_lb_max
        if not proven_optimal else 0,
        unmet=dict(sorted(unmet.items())),
        nodes=nodes,
        proven_optimal=proven_optimal,
        status=status,
    )


def verify_assignment(cluster, assignment: dict) -> dict:
    """Independently re-check an assignment. No trust in the solver.

    Returns a dict with ok / errors / loads / max_load / moved_bytes.
    Unmet partitions (weight fits nowhere) are expected to be absent.
    """
    errors = []
    placeable = set()
    for pid, part in cluster.partitions.items():
        fits = any(
            s in cluster.slots and cluster.slots[s].capacity >= part.weight
            for s in cluster.allowed_slot_ids(pid)
        )
        if fits:
            placeable.add(pid)

    if set(assignment) != placeable:
        missing = placeable - set(assignment)
        extra = set(assignment) - placeable
        if missing:
            errors.append(f"partitions not assigned: {sorted(missing)}")
        if extra:
            errors.append(f"unexpected assignments: {sorted(extra)}")

    loads = {sid: 0 for sid in cluster.slots}
    for pid, sid in assignment.items():
        if pid not in cluster.partitions:
            errors.append(f"unknown partition {pid}")
            continue
        if sid not in cluster.slots:
            errors.append(f"{pid}: unknown slot {sid}")
            continue
        part = cluster.partitions[pid]
        if part.allowed_slots is not None and sid not in part.allowed_slots:
            errors.append(f"{pid}: affinity violated on {sid}")
        loads[sid] += part.weight

    for sid, load in loads.items():
        if load > cluster.slots[sid].capacity:
            errors.append(f"slot {sid}: load {load} exceeds capacity {cluster.slots[sid].capacity}")

    location = {}
    for pid, sid in assignment.items():
        location.setdefault(sid, set()).add(pid)
    for pair in cluster.anti_affinity:
        a, b = tuple(pair)
        if assignment.get(a) is not None and assignment.get(a) == assignment.get(b):
            errors.append(f"anti-affinity violated: {a} and {b} share {assignment[a]}")

    moved = 0
    for pid, sid in assignment.items():
        if cluster.current.get(pid) != sid:
            moved += cluster.partitions[pid].weight

    return {
        "ok": not errors,
        "errors": errors,
        "loads": loads,
        "max_load": max(loads.values()) if loads else 0,
        "moved_bytes": moved,
    }
