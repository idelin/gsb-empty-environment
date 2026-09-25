"""Declared numerical tolerances and latency model for the repair engine."""

NUMERIC_TOLERANCE = 1e-9
CONDITION_TOLERANCE = 1e-9


def online_latency_samples(gap_length: int) -> int:
    """Algorithmic latency of an online repair, in samples.

    A gap of ``gap_length`` samples is only recognised once the first
    confirmed sample after it arrives, so an online repair trails the
    signal by ``gap_length + 1`` samples. Offline repair has no such
    latency constraint because it may look at both sides of the gap.
    """
    if gap_length < 1:
        raise ValueError("gap_length must be >= 1")
    return gap_length + 1
