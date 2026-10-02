"""2D bilinear sampling with hand-written gradients (no autodiff, no tensor libs).

Conventions
-----------
Coordinates ``(u, v)`` are normalized to ``[-1, 1]`` and mapped to continuous
pixel indices ``p`` along an axis of length ``N``:

* ``align="corners"``: ``p = (u + 1) * (N - 1) / 2``   (pixel centers at +-1)
* ``align="centers"``: ``p = ((u + 1) * N - 1) / 2``   (pixel corners at +-1)

Boundary handling maps ``p`` to the sampling coordinate ``q``:

* ``boundary="clamp"``:   ``q = min(max(p, 0), N - 1)``
* ``boundary="reflect"``: mirror reflection with period ``2 * (N - 1)``
* ``boundary="zeros"``:   ``q = p`` and a per-axis in-range mask
  ``0 <= p <= N - 1``; out-of-range samples return 0 and mask 0.

Bilinear interpolation uses the four taps around ``q`` with weights from the
fractional part.  Taps whose index falls outside ``[0, N - 1]`` get zero
weight (this only matters for ``zeros`` padding and degenerate axes).

Explicit subgradient policy (non-differentiable points)
-------------------------------------------------------
The function is piecewise bilinear in the coordinates, so kinks occur at
integer pixel nodes and at boundary turning points.  The backward pass uses
these deterministic rules:

* integer node ``q == k`` (``0 <= k < N - 1``): the *right* cell ``[k, k+1]``
  is used, i.e. the right derivative;
* last node ``q == N - 1``: the *last* cell ``[N-2, N-1]`` is used, i.e. the
  left derivative (consistent with clamp's interior-side rule);
* clamp: ``dq/dp = 1`` for ``0 <= p <= N - 1`` (endpoints included, interior
  side) and ``0`` strictly outside;
* reflect: with ``t = p mod 2(N-1)``, ``dq/dp = +1`` when ``t <= N - 1`` and
  ``-1`` otherwise, so turning points take the ``+1`` branch;
* zeros: the mask is treated as locally constant, ``dmask/dp = 0`` everywhere
  (including the jumps at ``0`` and ``N - 1``); the mask itself is 1 on the
  closed interval ``[0, N - 1]``;
* zero-length axis (``N == 1``): the axis contributes a single tap with
  weight 1 and zero coordinate derivative.

All gradient contributions are accumulated with ``+=`` so repeated sampling
of the same position (view sharing) sums correctly.
"""

import math

ALIGN_CORNERS = "corners"
ALIGN_CENTERS = "centers"

BOUNDARY_CLAMP = "clamp"
BOUNDARY_REFLECT = "reflect"
BOUNDARY_ZEROS = "zeros"

_ALIGNS = (ALIGN_CORNERS, ALIGN_CENTERS)
_BOUNDARIES = (BOUNDARY_CLAMP, BOUNDARY_REFLECT, BOUNDARY_ZEROS)


def _check_data(data):
    if not data or not data[0]:
        raise ValueError("data must be a non-empty rectangular nested list")
    width = len(data[0])
    for row in data:
        if len(row) != width:
            raise ValueError("data rows must all have the same length")
    return len(data), width


def _to_pixel(u, n, align):
    """Normalized coordinate -> continuous pixel index and derivative."""
    if align == ALIGN_CORNERS:
        if n == 1:
            return 0.0, 0.0
        return (u + 1.0) * (n - 1) / 2.0, (n - 1) / 2.0
    # ALIGN_CENTERS
    return ((u + 1.0) * n - 1.0) / 2.0, n / 2.0


def _apply_boundary(p, n, boundary):
    """Pixel index -> (sampling coordinate q, dq/dp, axis mask)."""
    if boundary == BOUNDARY_CLAMP:
        if n == 1:
            return 0.0, 0.0, 1.0
        if p < 0.0:
            return 0.0, 0.0, 1.0
        if p > n - 1.0:
            return n - 1.0, 0.0, 1.0
        return p, 1.0, 1.0
    if boundary == BOUNDARY_REFLECT:
        if n == 1:
            return 0.0, 0.0, 1.0
        period = 2.0 * (n - 1)
        t = p % period
        if t <= n - 1.0:
            return t, 1.0, 1.0
        return period - t, -1.0, 1.0
    # BOUNDARY_ZEROS
    mask = 1.0 if 0.0 <= p <= n - 1.0 else 0.0
    return p, 1.0, mask


def _axis_taps(q, n):
    """Valid bilinear taps for one axis: tuple of (index, weight, d/dq).

    Taps outside ``[0, n - 1]`` are dropped entirely (zero weight and zero
    derivative); a tap with weight 0 is kept when its derivative is needed
    (e.g. at the last node).
    """
    if n == 1:
        return ((0, 1.0, 0.0),)
    i0 = math.floor(q)
    if i0 >= n - 1:
        # Last node (or beyond, under zeros padding): use the last cell.
        i0, frac = n - 2, 1.0
    else:
        frac = q - i0
    taps = []
    for index, weight, dweight in ((i0, 1.0 - frac, -1.0),
                                   (i0 + 1, frac, 1.0)):
        if 0 <= index < n and (weight != 0.0 or dweight != 0.0):
            taps.append((index, weight, dweight))
    return tuple(taps)


def _sample_point(data, height, width, u, v, align, boundary):
    """Forward for one coordinate; returns (value, mask, point cache)."""
    px, dpx_du = _to_pixel(u, width, align)
    py, dpy_dv = _to_pixel(v, height, align)
    qx, dqx, mask_x = _apply_boundary(px, width, boundary)
    qy, dqy, mask_y = _apply_boundary(py, height, boundary)
    x_taps = _axis_taps(qx, width)
    y_taps = _axis_taps(qy, height)
    mask = mask_x * mask_y
    value = mask * sum(
        wx * wy * data[iy][ix]
        for ix, wx, _ in x_taps
        for iy, wy, _ in y_taps
    )
    cache = (dpx_du * dqx, dpy_dv * dqy, mask, x_taps, y_taps)
    return value, mask, cache


def _backward_point(data, grad_out, point_cache, grad_data):
    """Accumulate one sample's gradient; returns (grad_u, grad_v)."""
    dpx_du, dpy_dv, mask, x_taps, y_taps = point_cache
    g = grad_out * mask
    for ix, wx, _ in x_taps:
        for iy, wy, _ in y_taps:
            grad_data[iy][ix] += g * wx * wy
    grad_u = g * dpx_du * sum(
        dwx * wy * data[iy][ix]
        for ix, _, dwx in x_taps
        for iy, wy, _ in y_taps
    )
    grad_v = g * dpy_dv * sum(
        wx * dwy * data[iy][ix]
        for ix, wx, _ in x_taps
        for iy, _, dwy in y_taps
    )
    return grad_u, grad_v


def sample(data, coords, align=ALIGN_CORNERS, boundary=BOUNDARY_CLAMP,
           checkpoint=False):
    """Sample ``data`` at normalized ``coords`` (list of ``(u, v)``).

    Returns ``(values, masks, cache)``.  With ``checkpoint=True`` the cache
    stores only the inputs and ``sample_backward`` recomputes the
    intermediates (same results, less memory).
    """
    if align not in _ALIGNS:
        raise ValueError("unknown align mode: %r" % (align,))
    if boundary not in _BOUNDARIES:
        raise ValueError("unknown boundary mode: %r" % (boundary,))
    height, width = _check_data(data)
    coords = [(float(u), float(v)) for u, v in coords]
    values, masks, point_caches = [], [], []
    for u, v in coords:
        value, mask, point_cache = _sample_point(
            data, height, width, u, v, align, boundary)
        values.append(value)
        masks.append(mask)
        point_caches.append(point_cache)
    cache = {
        "coords": coords,
        "align": align,
        "boundary": boundary,
        "points": None if checkpoint else point_caches,
    }
    return values, masks, cache


def sample_backward(data, grad_out, cache):
    """Backward of :func:`sample`.

    Returns ``(grad_data, grad_coords)`` where ``grad_data`` has the same
    shape as ``data`` (contributions of all samples accumulated) and
    ``grad_coords`` is a list of ``(grad_u, grad_v)`` per input coordinate.
    """
    height, width = _check_data(data)
    coords = cache["coords"]
    if len(grad_out) != len(coords):
        raise ValueError("grad_out length does not match number of coords")
    grad_data = [[0.0] * width for _ in range(height)]
    grad_coords = []
    point_caches = cache["points"]
    for k, (u, v) in enumerate(coords):
        if point_caches is None:
            # Checkpoint mode: recompute intermediates from stored inputs.
            _, _, point_cache = _sample_point(
                data, height, width, u, v, cache["align"], cache["boundary"])
        else:
            point_cache = point_caches[k]
        grad_u, grad_v = _backward_point(data, grad_out[k], point_cache,
                                         grad_data)
        grad_coords.append((grad_u, grad_v))
    return grad_data, grad_coords
