"""Independent per-sample reference implementation used by the tests.

This module deliberately re-derives everything from first principles
(math.sin / math.cos, explicit shorten arithmetic) instead of importing
the library's gain functions, so the tests check xsplice against an
independent oracle.
"""

import math


def ref_fade_gain(curve, kind, x):
    if curve == "linear":
        return x if kind == "in" else 1.0 - x
    if curve == "equal_power":
        return math.sin(0.5 * math.pi * x) if kind == "in" else math.cos(0.5 * math.pi * x)
    raise AssertionError(f"bad curve {curve!r}")


def ref_shortened_fades(duration, fade_in, fade_out):
    if fade_in + fade_out <= duration:
        return fade_in, fade_out
    scale = duration / (fade_in + fade_out)
    return math.floor(fade_in * scale), math.floor(fade_out * scale)


def ref_render(entries, sources, total):
    """entries: (offset, duration, fade_in, fade_out, curve, src_id,
    first_src_index, step) with *effective* fades already applied."""
    out = []
    for t in range(total):
        acc = 0.0
        for offset, duration, fi, fo, curve, src_id, first, step in entries:
            if not offset <= t < offset + duration:
                continue
            i = t - offset
            if fi and i < fi:
                g = ref_fade_gain(curve, "in", i / fi)
            elif fo and i >= duration - fo:
                g = ref_fade_gain(curve, "out", (i - (duration - fo)) / fo)
            else:
                g = 1.0
            acc += g * sources[src_id][first + i * step]
        out.append(acc)
    return out


def ref_entry(seg, fade_in=None, fade_out=None):
    """Build a reference entry from an xsplice Segment."""
    fi, fo = seg.fade_in, seg.fade_out
    if fade_in is None or fade_out is None:
        fi, fo = ref_shortened_fades(seg.duration, fi, fo)
    else:
        fi, fo = fade_in, fade_out
    first = seg.src_start if seg.src_end > seg.src_start else seg.src_start - 1
    step = 1 if seg.src_end > seg.src_start else -1
    return (seg.offset, seg.duration, fi, fo, seg.curve, seg.src_id, first, step)
