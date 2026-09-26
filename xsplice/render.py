"""Plan execution.

Rendering is a pure function of the compiled plan: output sample ``t``
depends only on source samples at the mapped positions (zero look-ahead,
zero declared latency).  Any chunking of the output timeline therefore
yields bit-identical results, which is what makes pre-generated plans
safe to execute in random-sized blocks.
"""

from __future__ import annotations

from .plan import Plan, gain_at, src_index_at


def render_range(plan: Plan, start: int, end: int) -> list[float]:
    """Render output samples [start, end), clamped to the plan."""
    start = max(0, start)
    end = min(end, plan.total)
    if start >= end:
        return []
    out = [0.0] * (end - start)
    for cseg in plan.segments:
        lo = max(start, cseg.offset)
        hi = min(end, cseg.end)
        if lo >= hi:
            continue
        src = plan.sources[cseg.seg.src_id]
        for t in range(lo, hi):
            local = t - cseg.offset
            out[t - start] += gain_at(cseg, local) * src[src_index_at(cseg, local)]
    return out


def render(plan: Plan) -> list[float]:
    """Render the whole plan in one pass."""
    return render_range(plan, 0, plan.total)


def render_chunked(plan: Plan, chunk_sizes) -> list[float]:
    """Render in consecutive chunks of the given sizes.

    Chunk boundaries never affect the result; a short final chunk is
    clamped and any remainder after the sizes run out is rendered as one
    last chunk.  Equivalent to :func:`render` bit for bit.
    """
    out: list[float] = []
    pos = 0
    for size in chunk_sizes:
        if pos >= plan.total:
            break
        if size <= 0:
            raise ValueError(f"chunk size must be positive, got {size}")
        n = min(size, plan.total - pos)
        out.extend(render_range(plan, pos, pos + n))
        pos += n
    if pos < plan.total:
        out.extend(render_range(plan, pos, plan.total))
    return out
