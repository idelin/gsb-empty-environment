"""Independent event simulation.

Replays the alloc/free/read event stream emitted by the engine and
verifies, independently of the engine's own bookkeeping, that no
activation is read after it was freed, that nothing is freed twice, and
that the peak memory matches.  This validates that a checkpoint plan, as
executed, never touches released memory.
"""
from __future__ import annotations


class SimViolation(RuntimeError):
    pass


def simulate(events):
    live = {}
    current = 0
    peak = 0
    reads = 0
    for ev in events:
        kind, tag, size = ev
        if kind == "alloc":
            if tag in live:
                raise SimViolation(f"double alloc of {tag!r}")
            live[tag] = size
            current += size
            peak = max(peak, current)
        elif kind == "free":
            if tag not in live:
                raise SimViolation(f"free of unallocated {tag!r}")
            current -= live.pop(tag)
        elif kind == "read":
            if tag not in live:
                raise SimViolation(
                    f"read of freed/unallocated buffer {tag!r}")
            reads += 1
        else:
            raise SimViolation(f"unknown event {ev!r}")
    return {"peak": peak, "reads": reads, "live_at_end": sorted(live)}
