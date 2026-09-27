"""Exact distances between discrete distributions (rational arithmetic).

All probabilities are represented as :class:`fractions.Fraction`, so reported
distances are exact.  Distributions are passed as raw integer *count* vectors
aligned with a common support; they are normalised internally.

Definitions (P, Q are discrete distributions over buckets b):

    total variation : TV(P, Q) = 1/2 * sum_b |P(b) - Q(b)|

    1-D earth mover : W_1(P, Q) = sum_{j<k} |CDF_P(j) - CDF_Q(j)| * gap_j
                      with ground cost c_{i,j} = |pos_i - pos_j| on an
                      ordered support

    general EMD     : min  sum_{i,j} f_{i,j} * c_{i,j}
                      s.t. sum_j f_{i,j} = P(i), sum_i f_{i,j} = Q(j),
                           f_{i,j} >= 0
                      solved as a real min-cost flow for arbitrary cost
                      matrices (the 1-D cumulative formula is NOT valid here)
"""

from collections import deque
from fractions import Fraction


def _normalise(counts):
    total = sum(counts)
    if total <= 0:
        raise ValueError(
            "distance is undefined for an empty distribution (total count is zero)"
        )
    return [Fraction(c, total) for c in counts]


def _check_compatible(p_counts, q_counts):
    if len(p_counts) != len(q_counts):
        raise ValueError("count vectors must share the same support length")


# ---------------------------------------------------------------------------
# Total variation
# ---------------------------------------------------------------------------

def total_variation(p_counts, q_counts):
    """Return ``(TV, per_bucket_contributions)`` as exact Fractions."""
    _check_compatible(p_counts, q_counts)
    p = _normalise(p_counts)
    q = _normalise(q_counts)
    contribs = [abs(a - b) / 2 for a, b in zip(p, q)]
    return sum(contribs, Fraction(0)), contribs


# ---------------------------------------------------------------------------
# Ordered 1-D transportation distance
# ---------------------------------------------------------------------------

def emd_1d(p_counts, q_counts, positions):
    """W_1 on an ordered support with ground cost ``|pos_i - pos_j|``.

    Returns a dict containing:

    ``distance``
        Exact Fraction W_1 value.
    ``gap_contribs``
        Per-gap contribution ``|CDF_P(j) - CDF_Q(j)| * gap_j`` (length k-1);
        these sum to ``distance``.
    ``flow``
        Optimal transport matrix (Fractions), built greedily.
    ``dual``
        Optimal dual potentials ``phi``; ``phi`` is 1-Lipschitz on the
        positions and certifies optimality.
    """
    _check_compatible(p_counts, q_counts)
    k = len(p_counts)
    if len(positions) != k:
        raise ValueError("positions must align with the support")
    positions = [Fraction(x) for x in positions]
    if any(positions[i + 1] < positions[i] for i in range(k - 1)):
        raise ValueError("positions must be non-decreasing")
    p = _normalise(p_counts)
    q = _normalise(q_counts)

    gap_contribs = []
    distance = Fraction(0)
    cum = Fraction(0)
    signs = []
    for j in range(k - 1):
        cum += p[j] - q[j]
        gap = positions[j + 1] - positions[j]
        contribution = abs(cum) * gap
        gap_contribs.append(contribution)
        distance += contribution
        signs.append(1 if cum > 0 else (-1 if cum < 0 else 0))

    # Greedy north-west-corner flow: optimal for the 1-D line metric.
    flow = [[Fraction(0)] * k for _ in range(k)]
    rem_p = list(p)
    rem_q = list(q)
    i = j = 0
    while i < k and j < k:
        move = min(rem_p[i], rem_q[j])
        flow[i][j] = move
        rem_p[i] -= move
        rem_q[j] -= move
        if rem_p[i] == 0:
            i += 1
        if rem_q[j] == 0:
            j += 1

    # Dual potentials: phi_{j+1} - phi_j = sign(cumulative difference) * gap.
    dual = [Fraction(0)] * k
    for j in range(k - 1):
        dual[j + 1] = dual[j] + signs[j] * (positions[j + 1] - positions[j])

    return {
        "distance": distance,
        "gap_contribs": gap_contribs,
        "flow": flow,
        "dual": dual,
    }


def _check_flow_shape(flow, k_p, k_q):
    if len(flow) != k_p or any(len(row) != k_q for row in flow):
        raise ValueError("flow matrix has wrong shape")
    if any(f < 0 for row in flow for f in row):
        raise ValueError("flow must be non-negative")


def verify_emd_1d(p_counts, q_counts, positions, flow, claimed_distance, dual):
    """Validate a 1-D transport plan: feasibility, cost, dual optimality.

    Raises ``ValueError`` if any check fails, returns ``True`` otherwise.
    """
    _check_compatible(p_counts, q_counts)
    p = _normalise(p_counts)
    q = _normalise(q_counts)
    k = len(p)
    positions = [Fraction(x) for x in positions]
    claimed_distance = Fraction(claimed_distance)
    _check_flow_shape(flow, k, k)

    for i, row in enumerate(flow):
        if sum(row, Fraction(0)) != p[i]:
            raise ValueError(f"row {i} of flow does not sum to P(i)")
    for j in range(k):
        if sum(flow[i][j] for i in range(k)) != q[j]:
            raise ValueError(f"column {j} of flow does not sum to Q(j)")

    cost = sum(
        flow[i][j] * abs(positions[i] - positions[j])
        for i in range(k)
        for j in range(k)
    )
    if cost != claimed_distance:
        raise ValueError("claimed distance does not equal flow cost")

    if len(dual) != k:
        raise ValueError("dual vector has wrong length")
    # Dual feasibility: phi_j - phi_i <= |pos_i - pos_j| for every pair.
    for i in range(k):
        for j in range(k):
            if dual[j] - dual[i] > abs(positions[i] - positions[j]):
                raise ValueError(f"dual constraint violated for pair ({i}, {j})")

    dual_objective = sum((q[i] - p[i]) * dual[i] for i in range(k))
    if dual_objective != claimed_distance:
        raise ValueError("dual objective != claimed distance: plan not certified optimal")
    return True


# ---------------------------------------------------------------------------
# General cost matrix: genuine min-cost flow
# ---------------------------------------------------------------------------

def min_cost_flow(supplies, demands, cost):
    """Integral min-cost flow from ``supplies`` nodes to ``demands`` nodes.

    ``cost[i][j]`` is an integer per-unit cost from source i to sink j.
    Returns ``(flow_matrix, total_cost)`` with integral flows.  Solved by
    successive shortest augmenting paths (Bellman-Ford/SPFA).
    """
    m, n = len(supplies), len(demands)
    if m == 0 or n == 0:
        raise ValueError("need at least one source and one sink")
    if len(cost) != m or any(len(row) != n for row in cost):
        raise ValueError("cost matrix shape must match supplies/demands")
    total_supply = sum(supplies)
    if total_supply != sum(demands) or total_supply < 0:
        raise ValueError("total supply must equal total demand and be non-negative")
    cost = [[int(c) for c in row] for row in cost]

    source = m + n
    sink = source + 1
    node_count = m + n + 2
    graph = [[] for _ in range(node_count)]

    def add_edge(u, v, capacity, edge_cost):
        graph[u].append([v, capacity, edge_cost, len(graph[v])])
        graph[v].append([u, 0, -edge_cost, len(graph[u]) - 1])

    for i in range(m):
        add_edge(source, i, supplies[i], 0)
    for j in range(n):
        add_edge(m + j, sink, demands[j], 0)
    for i in range(m):
        for j in range(n):
            add_edge(i, m + j, total_supply, cost[i][j])

    total_cost = 0
    sent = 0
    while sent < total_supply:
        dist = [None] * node_count
        dist[source] = 0
        in_queue = [False] * node_count
        prev_node = [-1] * node_count
        prev_edge = [-1] * node_count
        queue = deque([source])
        in_queue[source] = True
        while queue:
            u = queue.popleft()
            in_queue[u] = False
            for edge_index, edge in enumerate(graph[u]):
                v, capacity, edge_cost, _ = edge
                if capacity > 0 and (
                    dist[v] is None or dist[u] + edge_cost < dist[v]
                ):
                    dist[v] = dist[u] + edge_cost
                    prev_node[v] = u
                    prev_edge[v] = edge_index
                    if not in_queue[v]:
                        queue.append(v)
                        in_queue[v] = True
        if dist[sink] is None:
            raise ValueError("infeasible transportation problem")
        augment = total_supply - sent
        v = sink
        while v != source:
            edge = graph[prev_node[v]][prev_edge[v]]
            augment = min(augment, edge[1])
            v = prev_node[v]
        v = sink
        while v != source:
            edge = graph[prev_node[v]][prev_edge[v]]
            edge[1] -= augment
            graph[v][edge[3]][1] += augment
            v = prev_node[v]
        total_cost += augment * dist[sink]
        sent += augment

    flow = [[0] * n for _ in range(m)]
    for i in range(m):
        for edge in graph[i]:
            v = edge[0]
            if m <= v < m + n:
                # Flow sent equals current capacity of the reverse edge.
                flow[i][v - m] = graph[v][edge[3]][1]
    return flow, total_cost


def verify_transport_plan(p_counts, q_counts, cost_matrix, flow, claimed_distance):
    """Validate feasibility and cost of a general transport plan.

    Raises ``ValueError`` on failure, returns ``True`` otherwise.  Optimality
    is certified separately (exhaustive cross-check, or dual certificate).
    """
    _check_compatible(p_counts, q_counts)
    p = _normalise(p_counts)
    q = _normalise(q_counts)
    m, n = len(p), len(q)
    claimed_distance = Fraction(claimed_distance)
    _check_flow_shape(flow, m, n)
    for i, row in enumerate(flow):
        if sum(row, Fraction(0)) != p[i]:
            raise ValueError(f"row {i} of flow does not sum to P(i)")
    for j in range(n):
        if sum(flow[i][j] for i in range(m)) != q[j]:
            raise ValueError(f"column {j} of flow does not sum to Q(j)")
    cost = sum(flow[i][j] * Fraction(cost_matrix[i][j]) for i in range(m) for j in range(n))
    if cost != claimed_distance:
        raise ValueError("claimed distance does not equal flow cost")
    return True


def emd_general(p_counts, q_counts, cost_matrix):
    """Transportation distance under an arbitrary integer ``cost_matrix``.

    A genuine min-cost flow is solved.  The 1-D cumulative formula is only
    valid for the line metric and must NOT be reused here.

    Returns ``(distance, flow, per_source_bucket_contributions)`` where
    ``distance`` is an exact Fraction, ``flow`` is a Fraction transport matrix
    and ``per_source_bucket_contributions[i] = sum_j flow[i,j] * c[i,j]``.
    """
    _check_compatible(p_counts, q_counts)
    m, n = len(p_counts), len(q_counts)
    n_p = sum(p_counts)
    n_q = sum(q_counts)
    if n_p <= 0 or n_q <= 0:
        raise ValueError("distance is undefined for an empty distribution")
    if len(cost_matrix) != m or any(len(row) != n for row in cost_matrix):
        raise ValueError("cost matrix shape must match the support")

    # Scale both count vectors to the same integral total n_p * n_q.
    supplies = [c * n_q for c in p_counts]
    demands = [c * n_p for c in q_counts]
    int_flow, total_cost = min_cost_flow(supplies, demands, cost_matrix)
    scale = n_p * n_q
    flow = [[Fraction(int_flow[i][j], scale) for j in range(n)] for i in range(m)]
    distance = Fraction(total_cost, scale)
    bucket_contribs = [
        sum(Fraction(int_flow[i][j] * cost_matrix[i][j], scale) for j in range(n))
        for i in range(m)
    ]
    return distance, flow, bucket_contribs


def emd_general_exhaustive(p_counts, q_counts, cost_matrix):
    """Brute-force optimum over ALL integral transport plans.

    Only intended for tiny cross-check instances.  Returns the exact Fraction
    distance, using the same integer scaling as :func:`emd_general`.
    """
    _check_compatible(p_counts, q_counts)
    n_p = sum(p_counts)
    n_q = sum(q_counts)
    if n_p <= 0 or n_q <= 0:
        raise ValueError("distance is undefined for an empty distribution")
    supplies = [c * n_q for c in p_counts]
    demands = [c * n_p for c in q_counts]
    m, n = len(supplies), len(demands)
    cost = [[int(c) for c in row] for row in cost_matrix]

    best = [None]
    rem_row = list(supplies)
    rem_col = list(demands)

    def rec(i, j, acc):
        if best[0] is not None and acc >= best[0]:
            return
        if i == m:
            if all(x == 0 for x in rem_col):
                best[0] = acc
            return
        if j == n:
            if rem_row[i] == 0:
                rec(i + 1, 0, acc)
            return
        is_last_col = j == n - 1
        is_last_row = i == m - 1
        if is_last_col:
            value = rem_row[i]
            if value > rem_col[j]:
                return
            rem_row[i] -= value
            rem_col[j] -= value
            rec(i, j + 1, acc + value * cost[i][j])
            rem_row[i] += value
            rem_col[j] += value
            return
        if is_last_row:
            value = rem_col[j]
            if value > rem_row[i]:
                return
            rem_row[i] -= value
            rem_col[j] -= value
            rec(i, j + 1, acc + value * cost[i][j])
            rem_row[i] += value
            rem_col[j] += value
            return
        upper = min(rem_row[i], rem_col[j])
        for value in range(upper + 1):
            rem_row[i] -= value
            rem_col[j] -= value
            rec(i, j + 1, acc + value * cost[i][j])
            rem_row[i] += value
            rem_col[j] += value

    rec(0, 0, 0)
    if best[0] is None:
        raise ValueError("infeasible transportation problem")
    return Fraction(best[0], n_p * n_q)
