"""Self-contained dense reference implementation (no NumPy/SciPy).

Used to cross-check the indexed engine on small node sets.  All sizes and
index lists are explicit inputs: ``n`` must match the array lengths, and the
inverse query takes an explicit sample count.  Nothing here is shared with
the indexed implementation, so agreement is meaningful.
"""

from __future__ import annotations


def _check_size(xs, los, his, n):
    if not (n == len(xs) == len(los) == len(his)):
        raise ValueError(
            f"explicit size n={n} disagrees with lengths "
            f"({len(xs)}, {len(los)}, {len(his)})")
    if n == 0:
        raise ValueError("empty reference arrays")
    for i in range(n - 1):
        if not xs[i] < xs[i + 1]:
            raise ValueError("xs must be strictly increasing")


def dense_evaluate(xs, los, his, n, x):
    """Linear scan interpolation; returns (lo, hi) at ``x``."""
    _check_size(xs, los, his, n)
    if x < xs[0] or x > xs[n - 1]:
        raise ValueError(f"x={x!r} outside [{xs[0]!r}, {xs[n - 1]!r}]")
    for i in range(n):
        if xs[i] == x:
            return (los[i], his[i])
    for i in range(n - 1):
        if xs[i] < x < xs[i + 1]:
            t = (x - xs[i]) / (xs[i + 1] - xs[i])
            return (los[i] + t * (los[i + 1] - los[i]),
                    his[i] + t * (his[i + 1] - his[i]))
    raise AssertionError("unreachable")


def _bisect_boundary(pred, outside_x, inside_x, iters=80):
    for _ in range(iters):
        mid = 0.5 * (outside_x + inside_x)
        if pred(mid):
            inside_x = mid
        else:
            outside_x = mid
    return 0.5 * (outside_x + inside_x)


def dense_invert(xs, los, his, n, y_lo, y_hi, num_samples):
    """Brute-force inverse: dense sampling + bisection refinement.

    Returns a sorted list of (a, b) intervals where the value band meets
    [y_lo, y_hi].  Breakpoints are always added to the sample grid.
    """
    _check_size(xs, los, his, n)
    if num_samples < 2:
        raise ValueError("num_samples must be >= 2")
    if n == 1:
        return [(xs[0], xs[0])] if los[0] <= y_hi and his[0] >= y_lo else []

    def pred(x):
        lo, hi = dense_evaluate(xs, los, his, n, x)
        return lo <= y_hi and hi >= y_lo

    step = (xs[n - 1] - xs[0]) / (num_samples - 1)
    grid = sorted({xs[0] + k * step for k in range(num_samples)} | set(xs))
    flags = [pred(g) for g in grid]
    intervals = []
    i = 0
    m = len(grid)
    while i < m:
        if not flags[i]:
            i += 1
            continue
        j = i
        while j + 1 < m and flags[j + 1]:
            j += 1
        a = grid[i] if i == 0 else _bisect_boundary(pred, grid[i - 1], grid[i])
        b = grid[j] if j == m - 1 else _bisect_boundary(pred, grid[j + 1], grid[j])
        intervals.append((a, b))
        i = j + 1
    return intervals
