"""Declared numerical tolerances and latency for the offline loudness planner.

Everything in this module is part of the public contract: plans embed these
values so that a serialized plan can be checked against the constants of the
code that applies it.
"""

#: RMS (linear, full scale = 1.0, i.e. -80 dBFS) below which a segment is
#: treated as silence. Silent segments are never amplified.
SILENCE_RMS_THRESHOLD = 1e-4

#: A non-silent segment whose achieved RMS falls short of its target by more
#: than this many dB is reported as a conflict. The planner never claims a
#: segment is normalized when the shortfall exceeds this tolerance.
GAIN_TOLERANCE_DB = 0.1

#: Absolute linear tolerance used when checking the rendered output peak
#: against the global peak ceiling.
PEAK_TOLERANCE = 1e-6

#: Maximum number of repair iterations used when a crossfade region pushes
#: the rendered peak above the ceiling.
MAX_REPAIR_ITERATIONS = 16

#: Serialization format version for plans.
PLAN_FORMAT_VERSION = 1


def processing_latency_samples(fade_len: int) -> int:
    """Declared processing latency introduced when *applying* a plan.

    Planning itself is offline and requires the whole signal up front.
    Applying a plan only needs a lookahead of half the crossfade length
    around each segment boundary, so the steady-state processing latency
    is ``fade_len // 2`` samples.
    """
    return max(0, fade_len // 2)
