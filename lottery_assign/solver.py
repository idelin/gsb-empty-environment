"""Solver: lexicographically smallest maximum matching under a seeded edge order.

Model
-----
Bipartite assignment between candidates and awards with constraints:

* each candidate receives at most one award;
* each award ``a`` has a capacity (default 1);
* each award ``a`` has per-group quotas: at most ``quotas[a][g]`` recipients
  of group ``g`` may receive award ``a`` (a missing group means 0, i.e. that
  group is not eligible for the award);
* forbidden (candidate, award) pairs may never be assigned.

Procedure (fully deterministic given the seed)
----------------------------------------------
1. Enumerate the feasible edges in a canonical order (sorted by
   ``(candidate_id, award_id)``) and draw one SplitMix64 value per edge from
   the seeded PRNG stream.  Sorting edges by ``(draw, canonical_index)``
   yields a total order over candidate edges.  Ties in the 64-bit draw are
   broken by the canonical index, so the order is total and reproducible.
2. Compute the maximum assignment size via max-flow (Dinic).
3. Among all maximum assignments, select the one whose edge sequence
   (edge positions in the random order, sorted ascending) is
   lexicographically smallest.  This is done by scanning edges in the random
   order and forcing an edge in whenever some maximum assignment still
   extends the forced set (a standard exchange argument shows this greedy
   procedure yields the lexicographic minimum).
"""

from __future__ import annotations

from dataclasses import dataclass, field

from .prng import SplitMix64


# ---------------------------------------------------------------------------
# Problem definition
# ---------------------------------------------------------------------------


@dataclass(frozen=True)
class Candidate:
    id: str
    group: str


@dataclass(frozen=True)
class Award:
    id: str
    capacity: int
    quotas: tuple  # tuple of (group, quota) pairs, sorted by group

    def quota_for(self, group: str) -> int:
        for g, q in self.quotas:
            if g == group:
                return q
        return 0


@dataclass
class Problem:
    candidates: list  # list[Candidate]
    awards: list  # list[Award]
    forbidden: frozenset  # frozenset[(candidate_id, award_id)]
    seed: int = 0

    @staticmethod
    def from_dict(data: dict) -> "Problem":
        candidates = [
            Candidate(id=str(c["id"]), group=str(c["group"]))
            for c in data.get("candidates", [])
        ]
        awards = []
        for a in data.get("awards", []):
            quotas = tuple(sorted((str(g), int(q)) for g, q in a.get("quotas", {}).items()))
            awards.append(
                Award(id=str(a["id"]), capacity=int(a.get("capacity", 1)), quotas=quotas)
            )
        forbidden = frozenset(
            (str(pair[0]), str(pair[1])) for pair in data.get("forbidden", [])
        )
        seed = int(data.get("seed", 0))
        problem = Problem(candidates=candidates, awards=awards, forbidden=forbidden, seed=seed)
        problem.validate()
        return problem

    def validate(self) -> None:
        cand_ids = [c.id for c in self.candidates]
        award_ids = [a.id for a in self.awards]
        if len(set(cand_ids)) != len(cand_ids):
            raise ValueError("duplicate candidate id")
        if len(set(award_ids)) != len(award_ids):
            raise ValueError("duplicate award id")
        cand_set, award_set = set(cand_ids), set(award_ids)
        for a in self.awards:
            if a.capacity < 0:
                raise ValueError(f"award {a.id!r} has negative capacity")
            for g, q in a.quotas:
                if q < 0:
                    raise ValueError(f"award {a.id!r} has negative quota for group {g!r}")
        for c, a in self.forbidden:
            if c not in cand_set:
                raise ValueError(f"forbidden edge references unknown candidate {c!r}")
            if a not in award_set:
                raise ValueError(f"forbidden edge references unknown award {a!r}")

    def feasible_edges(self) -> list:
        """All allowed (candidate_id, award_id) pairs in canonical order."""
        group_of = {c.id: c.group for c in self.candidates}
        award_by_id = {a.id: a for a in self.awards}
        edges = []
        for cid in sorted(group_of):
            for aid in sorted(award_by_id):
                if (cid, aid) in self.forbidden:
                    continue
                if award_by_id[aid].quota_for(group_of[cid]) > 0:
                    edges.append((cid, aid))
        return edges


# ---------------------------------------------------------------------------
# Deterministic edge order
# ---------------------------------------------------------------------------


def edge_order(edges: list, seed: int) -> list:
    """Return (ordered_edges, draws) for the canonical edge list and seed."""
    rng = SplitMix64(seed)
    draws = [rng.next_u64() for _ in edges]
    order = sorted(range(len(edges)), key=lambda i: (draws[i], i))
    return [edges[i] for i in order], draws


# ---------------------------------------------------------------------------
# Max-flow (Dinic) over the constraint network
# ---------------------------------------------------------------------------


class _Dinic:
    def __init__(self, n: int):
        self.n = n
        self.graph = [[] for _ in range(n)]  # edges: [to, cap, rev_index]

    def add_edge(self, u: int, v: int, cap: int) -> None:
        self.graph[u].append([v, cap, len(self.graph[v])])
        self.graph[v].append([u, 0, len(self.graph[u]) - 1])

    def max_flow(self, s: int, t: int) -> int:
        flow = 0
        n = self.n
        while True:
            level = [-1] * n
            level[s] = 0
            queue = [s]
            head = 0
            while head < len(queue):
                u = queue[head]
                head += 1
                for to, cap, _ in self.graph[u]:
                    if cap > 0 and level[to] < 0:
                        level[to] = level[u] + 1
                        queue.append(to)
            if level[t] < 0:
                return flow
            it = [0] * n

            def dfs(u: int, f: int) -> int:
                if u == t:
                    return f
                while it[u] < len(self.graph[u]):
                    e = self.graph[u][it[u]]
                    to, cap, rev = e
                    if cap > 0 and level[to] == level[u] + 1:
                        d = dfs(to, min(f, cap))
                        if d > 0:
                            e[1] -= d
                            self.graph[to][rev][1] += d
                            return d
                    it[u] += 1
                return 0

            while True:
                pushed = dfs(s, 1 << 62)
                if pushed == 0:
                    break
                flow += pushed


def _max_assignment_size(problem: Problem, forced: list) -> int | None:
    """Max total assigned count given forced edges, or None if infeasible.

    ``forced`` is a list of (candidate_id, award_id) edges that must be used.
    Returns |forced| + max flow on the remaining network, or None when the
    forced set itself violates a constraint.
    """
    group_of = {c.id: c.group for c in problem.candidates}
    award_by_id = {a.id: a for a in problem.awards}

    used_candidate = set()
    award_used = {a.id: 0 for a in problem.awards}
    quota_used = {(a.id, g): 0 for a in problem.awards for g, _ in a.quotas}
    for cid, aid in forced:
        if cid in used_candidate:
            return None
        if (cid, aid) in problem.forbidden:
            return None
        award = award_by_id[aid]
        group = group_of[cid]
        if award.quota_for(group) <= quota_used.get((aid, group), 0):
            return None
        if award_used[aid] >= award.capacity:
            return None
        used_candidate.add(cid)
        award_used[aid] += 1
        quota_used[(aid, group)] = quota_used.get((aid, group), 0) + 1

    # Node layout: source, candidates, (award, group) quota nodes, awards, sink.
    cand_index = {}
    idx = 1
    for c in problem.candidates:
        if c.id not in used_candidate:
            cand_index[c.id] = idx
            idx += 1
    qnode_index = {}
    for a in problem.awards:
        for g, q in a.quotas:
            if q - quota_used[(a.id, g)] > 0:
                qnode_index[(a.id, g)] = idx
                idx += 1
    award_index = {}
    for a in problem.awards:
        if award_used[a.id] < a.capacity:
            award_index[a.id] = idx
            idx += 1
    source, sink = 0, idx
    dinic = _Dinic(idx + 1)

    for cid, u in cand_index.items():
        dinic.add_edge(source, u, 1)
    for cid, u in cand_index.items():
        group = group_of[cid]
        for a in problem.awards:
            key = (a.id, group)
            if key in qnode_index and (cid, a.id) not in problem.forbidden:
                dinic.add_edge(u, qnode_index[key], 1)
    for (aid, g), u in qnode_index.items():
        if aid not in award_index:
            continue
        remaining = award_by_id[aid].quota_for(g) - quota_used[(aid, g)]
        dinic.add_edge(u, award_index[aid], remaining)
    for aid, u in award_index.items():
        dinic.add_edge(u, sink, award_by_id[aid].capacity - award_used[aid])

    return len(forced) + dinic.max_flow(source, sink)


# ---------------------------------------------------------------------------
# Main solve
# ---------------------------------------------------------------------------


def solve(problem: Problem) -> dict:
    """Solve and return a fully deterministic, JSON-serialisable result dict."""
    edges = problem.feasible_edges()
    ordered, draws = edge_order(edges, problem.seed)
    draw_of = {edge: draws[i] for i, edge in enumerate(edges)}
    rank_of = {edge: pos for pos, edge in enumerate(ordered)}

    optimum = _max_assignment_size(problem, [])

    forced = []
    for edge in ordered:
        trial = forced + [edge]
        size = _max_assignment_size(problem, trial)
        if size is not None and size == optimum:
            forced = trial

    group_of = {c.id: c.group for c in problem.candidates}
    assignments = [
        {"candidate": cid, "award": aid, "group": group_of[cid], "edge_rank": rank_of[(cid, aid)]}
        for cid, aid in sorted(forced, key=lambda e: rank_of[e])
    ]
    return {
        "seed": problem.seed,
        "prng": "splitmix64",
        "edge_order": [
            {"candidate": cid, "award": aid, "draw": draw_of[(cid, aid)]}
            for cid, aid in ordered
        ],
        "assignments": assignments,
        "num_assigned": len(assignments),
        "optimum": optimum,
    }


# ---------------------------------------------------------------------------
# Brute-force verification (small instances only)
# ---------------------------------------------------------------------------


def brute_force_lexmin(problem: Problem) -> tuple | None:
    """Enumerate all maximum assignments; return the lex-min edge sequence.

    Returns a tuple of edge ranks (positions in the seeded random order) in
    ascending order, or None when no assignment exists.  Exponential; use
    only for small instances.
    """
    edges = problem.feasible_edges()
    ordered, _ = edge_order(edges, problem.seed)
    rank_of = {edge: pos for pos, edge in enumerate(ordered)}

    group_of = {c.id: c.group for c in problem.candidates}
    award_by_id = {a.id: a for a in problem.awards}
    cand_ids = sorted(group_of)

    best_size = -1
    best_seq = None
    chosen = []  # list of (cid, aid)
    award_used = {a.id: 0 for a in problem.awards}
    quota_used = {(a.id, g): 0 for a in problem.awards for g, _ in a.quotas}

    def rec(i: int) -> None:
        nonlocal best_size, best_seq
        if i == len(cand_ids):
            seq = tuple(sorted(rank_of[e] for e in chosen))
            if len(chosen) > best_size or (len(chosen) == best_size and seq < best_seq):
                best_size = len(chosen)
                best_seq = seq
            return
        cid = cand_ids[i]
        group = group_of[cid]
        # Option 1: skip this candidate.
        rec(i + 1)
        # Option 2: assign a feasible award.
        for aid in sorted(award_by_id):
            award = award_by_id[aid]
            if (cid, aid) in problem.forbidden:
                continue
            if award_used[aid] >= award.capacity:
                continue
            quota = award.quota_for(group)
            if quota == 0 or quota_used.get((aid, group), 0) >= quota:
                continue
            chosen.append((cid, aid))
            award_used[aid] += 1
            quota_used[(aid, group)] = quota_used.get((aid, group), 0) + 1
            rec(i + 1)
            chosen.pop()
            award_used[aid] -= 1
            quota_used[(aid, group)] -= 1

    rec(0)
    return best_seq
