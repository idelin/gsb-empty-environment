"""Exact rational solvers for absorption problems on a Chain.

All arithmetic uses fractions.Fraction, so results are exact and the
residuals of the underlying linear equations can be verified to be zero.
"""

from fractions import Fraction

from .chain import Chain


def solve_linear(matrix, rhs):
    """Solve A x = b exactly by Gaussian elimination with Fractions.

    Raises ValueError if the system is singular.
    """
    n = len(matrix)
    a = [list(row) + [rhs[i]] for i, row in enumerate(matrix)]
    for col in range(n):
        pivot = None
        for r in range(col, n):
            if a[r][col] != 0:
                pivot = r
                break
        if pivot is None:
            raise ValueError("singular linear system")
        a[col], a[pivot] = a[pivot], a[col]
        inv = Fraction(1) / a[col][col]
        a[col] = [v * inv for v in a[col]]
        for r in range(n):
            if r != col and a[r][col] != 0:
                factor = a[r][col]
                a[r] = [v - factor * w for v, w in zip(a[r], a[col])]
    return [a[i][n] for i in range(n)]


def _class_of(state, classes):
    for idx, cls in enumerate(classes):
        if state in cls:
            return idx
    raise KeyError(state)


def absorption_probabilities(chain, target):
    """Exact P(hit `target` | start) for every state.

    `target` is a set of states forming a closed class (typically a single
    absorbing state).  Returns a dict {state: Fraction}.  States that can
    never reach the target get probability 0; the target itself gets 1.
    The probabilities over all closed classes of a finite chain sum to
    exactly 1 for every starting state.
    """
    target = frozenset(target)
    closed = chain.closed_classes()
    if target not in closed:
        raise ValueError(f"{sorted(target)} is not a closed class of the chain")
    transient = chain.transient_states()
    index = {s: k for k, s in enumerate(transient)}
    # h_i = sum_j P_ij h_j ; h = 1 on target, 0 on other closed classes.
    mat, rhs = [], []
    for i in transient:
        row = [Fraction(0)] * len(transient)
        row[index[i]] = Fraction(1)
        b = Fraction(0)
        for j in range(chain.n):
            p = chain.matrix[i][j]
            if p == 0:
                continue
            if j in target:
                b += p
            elif j in index:
                row[index[j]] -= p
            # other closed classes contribute p * 0
        mat.append(row)
        rhs.append(b)
    sol = solve_linear(mat, rhs) if transient else []
    probs = {s: sol[index[s]] for s in transient}
    for cls in closed:
        value = Fraction(1) if cls == target else Fraction(0)
        for s in cls:
            probs[s] = value
    return probs


def expected_hitting_times(chain, targets=None):
    """Exact expected number of steps to hit any state in `targets`.

    `targets` defaults to all absorbing states (P[i][i] == 1).  Returns a
    dict {state: Fraction or math.inf}.  States from which no target is
    reachable get math.inf ("never absorbed"); self-loop-1 states that are
    not targets therefore yield infinity, never a finite number.
    """
    import math
    if targets is None:
        targets = set(chain.absorbing_states())
    targets = set(targets)
    reach = {s for s in range(chain.n) if chain.reachable({s}) & targets}
    # states that can reach a target, excluding the targets themselves
    active = sorted(s for s in reach if s not in targets)
    index = {s: k for k, s in enumerate(active)}
    # t_i = 1 + sum_j P_ij t_j ; t = 0 on targets.
    mat, rhs = [], []
    for i in active:
        row = [Fraction(0)] * len(active)
        row[index[i]] = Fraction(1)
        for j in range(chain.n):
            p = chain.matrix[i][j]
            if p != 0 and j in index:
                row[index[j]] -= p
        mat.append(row)
        rhs.append(Fraction(1))
    sol = solve_linear(mat, rhs) if active else []
    times = {}
    for s in range(chain.n):
        if s in targets:
            times[s] = Fraction(0)
        elif s in index:
            times[s] = sol[index[s]]
        else:
            times[s] = math.inf
    return times


def conditional_expected_time(chain, target):
    """Exact E[steps to absorption | the chain is absorbed in `target`].

    Returns a dict {state: Fraction or None}.  The value is None (undefined,
    never 0) for states whose probability of hitting `target` is 0.
    """
    probs = absorption_probabilities(chain, target)
    closed = chain.closed_classes()
    transient = chain.transient_states()
    index = {s: k for k, s in enumerate(transient)}
    # u_i = E[ T * 1_{hit target} ] satisfies
    # u_i = sum_j P_ij (q_j + u_j), with q = hit probability,
    # u = 0 on every closed class.
    mat, rhs = [], []
    for i in transient:
        row = [Fraction(0)] * len(transient)
        row[index[i]] = Fraction(1)
        b = Fraction(0)
        for j in range(chain.n):
            p = chain.matrix[i][j]
            if p == 0:
                continue
            b += p * probs[j]
            if j in index:
                row[index[j]] -= p
        mat.append(row)
        rhs.append(b)
    sol = solve_linear(mat, rhs) if transient else []
    u = {s: sol[index[s]] for s in transient}
    for cls in closed:
        for s in cls:
            u[s] = Fraction(0)
    result = {}
    for s in range(chain.n):
        if probs[s] == 0:
            result[s] = None  # undefined: target hit probability is 0
        else:
            result[s] = u[s] / probs[s]
    return result


def absorption_residuals(chain, target, probs):
    """Residuals of the absorption-probability equations (should all be 0).

    For every state i: r_i = h_i - sum_j P_ij h_j, with h fixed to 1 on the
    target class and 0 on other closed classes.
    """
    target = frozenset(target)
    residuals = {}
    for i in range(chain.n):
        lhs = sum(chain.matrix[i][j] * probs[j] for j in range(chain.n))
        residuals[i] = probs[i] - lhs
    return residuals


def hitting_time_residuals(chain, targets, times):
    """Residuals of t_i = 1 + sum_j P_ij t_j for states with finite times."""
    import math
    targets = set(targets)
    residuals = {}
    for i in range(chain.n):
        if i in targets or times[i] == math.inf:
            continue
        lhs = Fraction(1) + sum(
            chain.matrix[i][j] * times[j]
            for j in range(chain.n) if times[j] != math.inf
        )
        residuals[i] = times[i] - lhs
    return residuals


def probability_sum_witness(chain, start):
    """Sum of absorption probabilities over all closed classes from `start`.

    For a finite chain this is exactly 1; useful as a checkable certificate.
    """
    total = Fraction(0)
    parts = {}
    for cls in chain.closed_classes():
        p = absorption_probabilities(chain, cls)[start]
        parts[frozenset(cls)] = p
        total += p
    return parts, total
