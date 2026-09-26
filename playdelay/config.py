"""Declared latency budget and numerical tolerances (single source of truth).

Declared playout latency
------------------------
target_delay = BASE_TARGET_DELAY + headroom, with headroom clamped to
[MIN_HEADROOM, MAX_HEADROOM]; the resulting buffer target in samples is
additionally clamped to (BUFFER_CAPACITY - 2) packets so the target is always
reachable inside the fixed-size buffer.

Numerical tolerances
--------------------
TIME_TOL       : absolute tolerance when comparing event/tick times.
RATE_SNAP_TOL  : estimated clock slope within this of 1.0 is snapped to exactly
                 1.0 (sub-tolerance timing noise must not perturb the schedule).
SLOPE_TEST_TOL : tolerance used by tests when checking the estimated drift.
"""

SAMPLE_RATE = 8000                 # Hz, provided by the local fixture
PACKET_SAMPLES = 160               # samples per packet (20 ms)
PACKET_PERIOD = PACKET_SAMPLES / SAMPLE_RATE
TICK = PACKET_PERIOD               # playout service period (receiver clock)

BASE_TARGET_DELAY = 0.060          # s, fixed component of target delay
MIN_HEADROOM = 0.040               # s, lower clamp of adaptive headroom
MAX_HEADROOM = 0.200               # s, upper clamp of adaptive headroom
K_JITTER = 4.0                     # headroom = K_JITTER * jitter-EWMA

BUFFER_CAPACITY = 16               # packets, fixed memory
SEEN_WINDOW_BITS = 4096            # fixed-size duplicate-detection bitset

CLOCK_FORGETTING = 0.999           # discounted-regression forgetting factor
MIN_CLOCK_SAMPLES = 100            # packets needed before the estimate is used
JITTER_ALPHA = 0.02                # EWMA for mean transit time
JITTER_BETA = 0.05                 # EWMA for |transit - mean|

LEVEL_HYSTERESIS = 2 * PACKET_SAMPLES  # dead-band around the target level
MAX_DROP_PER_TICK = 2              # packets, shrink policy rate limit
MAX_GROW_PER_TICK = 8              # samples, grow policy rate limit

TIME_TOL = 1e-9
RATE_SNAP_TOL = 2e-3
SLOPE_TEST_TOL = 5e-4
