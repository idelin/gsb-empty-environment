"""Batched matrix multiplication with broadcasting, gradients, JVP and HVP.

Design notes
------------
* Inputs are read strictly through their strides; broadcast (stride-0) and
  transposed/sliced views are handled in place and are NEVER materialized
  into a full contiguous copy to "simplify" the kernel.
* The forward kernel is blocked.  The tile buffers (A-tile + B-tile + C-tile)
  are the only workspace and their total size is capped by
  ``workspace_budget`` floats; block sizes are derived from the budget.
* Backward accumulates contributions directly into the *physical* storage
  cells of the input layouts.  Hence reduction over broadcast batch dims and
  any other form of shared physical storage (stride-0 views, overlapping
  slices) is handled by the same mechanism: contributions to one physical
  cell sum together.
"""

from .tensor import Tensor, iter_indices

DEFAULT_WORKSPACE_BUDGET = 4096  # floats


# ----------------------------------------------------------------------
# shape helpers
# ----------------------------------------------------------------------
def broadcast_shape(s1, s2):
    """Numpy-style broadcast of two shape tuples."""
    n = max(len(s1), len(s2))
    s1 = (1,) * (n - len(s1)) + tuple(s1)
    s2 = (1,) * (n - len(s2)) + tuple(s2)
    out = []
    for x, y in zip(s1, s2):
        if x == y or x == 1 or y == 1:
            out.append(max(x, y))
        else:
            raise ValueError(f"shapes not broadcastable: {s1} vs {s2}")
    return tuple(out)


def _promote_pair(a, b):
    """Numpy matmul 1-D promotion: (n,)->(1,n) on the left, (n,)->(n,1) on
    the right.  Size-1 axes get stride 0, so the view touches exactly the
    same physical cells as the original vector."""
    if a.ndim < 1 or b.ndim < 1:
        raise ValueError("matmul requires operands with ndim >= 1")
    a_vec = a.ndim == 1
    b_vec = b.ndim == 1
    a2 = a.view((1,) + a.shape, (0,) + a.strides) if a_vec else a
    b2 = b.view(b.shape + (1,), b.strides + (0,)) if b_vec else b
    return a2, b2, a_vec, b_vec


def _insert_axis(t, pos):
    shape = list(t.shape)
    strides = list(t.strides)
    shape.insert(pos, 1)
    strides.insert(pos, 0)
    return t.view(tuple(shape), tuple(strides))


def _drop_axis(t, pos):
    pos %= t.ndim
    if t.shape[pos] != 1:
        raise ValueError("can only drop a size-1 axis")
    shape = list(t.shape)
    strides = list(t.strides)
    del shape[pos]
    del strides[pos]
    return t.view(tuple(shape), tuple(strides))


def _batch_offset(t2, bidx, batch_ndim):
    """Physical offset of the 2-D core of ``t2`` at broadcast batch index
    ``bidx``.  Size-1 (broadcast) batch axes map to index 0."""
    off = t2.offset
    nb = t2.ndim - 2
    pad = batch_ndim - nb
    for ax in range(nb):
        idx = 0 if t2.shape[ax] == 1 else bidx[pad + ax]
        off += idx * t2.strides[ax]
    return off


def _block_size(workspace_budget):
    """Largest square block edge bs with 3*bs*bs <= budget (A, B and C
    tiles live in the workspace simultaneously)."""
    if workspace_budget < 3:
        raise ValueError(
            f"workspace budget {workspace_budget} too small: need >= 3 floats")
    return max(1, int((workspace_budget // 3) ** 0.5))


# ----------------------------------------------------------------------
# blocked 2-D kernel: C[m,n] = A[m,k] @ B[k,n], all given as raw buffers +
# strides + base offsets.  Workspace usage <= 3*bs*bs floats.
# ----------------------------------------------------------------------
def _blocked_2d(a_data, asi, asj, aoff,
                b_data, bsi, bsj, boff,
                c_data, csi, csj, coff,
                m, k, n, bs):
    for i0 in range(0, m, bs):
        bi = min(bs, m - i0)
        for j0 in range(0, n, bs):
            bj = min(bs, n - j0)
            ctile = [0.0] * (bi * bj)              # workspace: bi*bj
            for k0 in range(0, k, bs):
                bk = min(bs, k - k0)
                atile = [a_data[aoff + (i0 + ii) * asi + (k0 + kk) * asj]
                         for ii in range(bi) for kk in range(bk)]   # bi*bk
                btile = [b_data[boff + (k0 + kk) * bsi + (j0 + jj) * bsj]
                         for kk in range(bk) for jj in range(bj)]   # bk*bj
                for ii in range(bi):
                    arow = atile[ii * bk:(ii + 1) * bk]
                    crow = ctile[ii * bj:(ii + 1) * bj]
                    for kk in range(bk):
                        av = arow[kk]
                        if av == 0.0:
                            continue
                        brow = btile[kk * bj:(kk + 1) * bj]
                        for jj in range(bj):
                            crow[jj] += av * brow[jj]
                    ctile[ii * bj:(ii + 1) * bj] = crow
            for ii in range(bi):
                base = coff + (i0 + ii) * csi
                for jj in range(bj):
                    c_data[base + (j0 + jj) * csj] = ctile[ii * bj + jj]


# ----------------------------------------------------------------------
# forward
# ----------------------------------------------------------------------
def matmul(a, b, workspace_budget=DEFAULT_WORKSPACE_BUDGET):
    """Batched matmul with numpy semantics: broadcasting batch dims and
    1-D vector promotion.  Inner dimension 0 is defined (yields zeros)."""
    a2, b2, a_vec, b_vec = _promote_pair(a, b)
    m, k = a2.shape[-2], a2.shape[-1]
    k2, n = b2.shape[-2], b2.shape[-1]
    if k != k2:
        raise ValueError(f"inner dimension mismatch: {k} vs {k2}")
    batch = broadcast_shape(a2.shape[:-2], b2.shape[:-2])
    nb = len(batch)
    out = Tensor.zeros(batch + (m, n))
    bs = _block_size(workspace_budget)
    for bidx in iter_indices(batch):
        _blocked_2d(a2.data, a2.strides[-2], a2.strides[-1],
                    _batch_offset(a2, bidx, nb),
                    b2.data, b2.strides[-2], b2.strides[-1],
                    _batch_offset(b2, bidx, nb),
                    out.data, out.strides[-2], out.strides[-1],
                    _batch_offset(out, bidx, nb),
                    m, k, n, bs)
    if a_vec:
        out = _drop_axis(out, -2)
    if b_vec:
        out = _drop_axis(out, -1)
    return out


# ----------------------------------------------------------------------
# elementwise helpers
# ----------------------------------------------------------------------
def add(x, y):
    if x.shape != y.shape:
        raise ValueError(f"shape mismatch: {x.shape} vs {y.shape}")
    out = Tensor.zeros(x.shape)
    for idx in iter_indices(x.shape):
        out.set_at(idx, x.value_at(idx) + y.value_at(idx))
    return out


def inner(x, y):
    """<x, y> = sum of elementwise products over the logical view."""
    if x.shape != y.shape:
        raise ValueError(f"shape mismatch: {x.shape} vs {y.shape}")
    return sum(x.value_at(idx) * y.value_at(idx)
               for idx in iter_indices(x.shape))


# ----------------------------------------------------------------------
# backward (VJP) of L = <seed, A @ B>
# ----------------------------------------------------------------------
def matmul_backward(seed, a, b):
    """Gradients of L = <seed, matmul(a, b)> w.r.t. ``a`` and ``b``.

    The returned gradient tensors share the *layout* (shape/strides/offset)
    of ``a`` and ``b`` over freshly allocated buffers sized to the inputs'
    storage, so contributions to physically shared cells accumulate.
    Broadcast batch dims are therefore reduced automatically.
    """
    a2, b2, a_vec, b_vec = _promote_pair(a, b)
    g2 = seed
    if a_vec:
        g2 = _insert_axis(g2, g2.ndim - 1)
    if b_vec:
        g2 = _insert_axis(g2, g2.ndim)
    m, k = a2.shape[-2], a2.shape[-1]
    n = b2.shape[-1]
    batch = broadcast_shape(a2.shape[:-2], b2.shape[:-2])
    if g2.shape != batch + (m, n):
        raise ValueError(
            f"seed shape {g2.shape} != output shape {batch + (m, n)}")
    nb = len(batch)

    grad_a = Tensor([0.0] * len(a.data), a.shape, a.strides, a.offset)
    grad_b = Tensor([0.0] * len(b.data), b.shape, b.strides, b.offset)
    ga2, gb2, _, _ = _promote_pair(grad_a, grad_b)

    gsi, gsj = g2.strides[-2], g2.strides[-1]
    for bidx in iter_indices(batch):
        aoff = _batch_offset(a2, bidx, nb)
        boff = _batch_offset(b2, bidx, nb)
        goff = _batch_offset(g2, bidx, nb)
        gaoff = _batch_offset(ga2, bidx, nb)
        gboff = _batch_offset(gb2, bidx, nb)
        for i in range(m):
            for j in range(n):
                g = g2.data[goff + i * gsi + j * gsj]
                if g == 0.0:
                    continue
                ga_row = gaoff + i * ga2.strides[-2]
                a_row = aoff + i * a2.strides[-2]
                gb_col = gboff + j * gb2.strides[-1]
                b_col = boff + j * b2.strides[-1]
                for kk in range(k):
                    ga2.data[ga_row + kk * ga2.strides[-1]] += \
                        g * b2.data[b_col + kk * b2.strides[-2]]
                    gb2.data[gb_col + kk * gb2.strides[-2]] += \
                        g * a2.data[a_row + kk * a2.strides[-1]]
    return grad_a, grad_b


# ----------------------------------------------------------------------
# JVP: directional derivative of matmul along (da, db)
# ----------------------------------------------------------------------
def matmul_jvp(a, b, da, db, workspace_budget=DEFAULT_WORKSPACE_BUDGET):
    """d(A@B)[dA, dB] = dA@B + A@dB.  Tangents only need the logical shape
    of the primal; any strides/broadcast views compose transparently."""
    if da.shape != a.shape or db.shape != b.shape:
        raise ValueError("tangent shapes must match primals")
    return add(matmul(da, b, workspace_budget),
               matmul(a, db, workspace_budget))


# ----------------------------------------------------------------------
# HVP of the scalarized loss L(A,B) = <seed, A@B>
# ----------------------------------------------------------------------
def matmul_hvp(seed, a, b, u, v):
    """Hessian-vector product along (u, v).

    g_A = S@B^T, g_B = A^T@S, hence
        H.(u,v) = (S@V^T, U^T@S)
    which is exactly the VJP machinery applied to (a, v) and (u, b), so
    broadcasting, vector promotion and view layouts compose here too.
    """
    if u.shape != a.shape or v.shape != b.shape:
        raise ValueError("direction shapes must match primals")
    h_a, _ = matmul_backward(seed, a, v)
    _, h_b = matmul_backward(seed, u, b)
    return h_a, h_b
