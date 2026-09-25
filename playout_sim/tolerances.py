"""Declared numerical tolerances and latency constants used by tests."""

# Clock estimation
DRIFT_TOLERANCE_PPM = 30.0        # accepted error of the drift estimate

# Adaptive delay behaviour
TARGET_PRE_BURST_MARGIN_S = 0.012  # steady-state headroom above base margin
TARGET_BURST_RISE_S = 0.015        # min extra headroom expected during a burst
DELAY_SETTLE_TOLERANCE_S = 0.002   # post-burst target vs pre-burst target
LEVEL_SETTLE_TOLERANCE_SAMPLES = 600

# Playout integrity
MAX_CONCEALED_SAMPLES_DRIFT_TEST = 3000  # bounded concealment in test 1

# Declared latency budget (see README): base margin + adaptive headroom
DECLARED_BASE_MARGIN_S = 0.025
DECLARED_MIN_HEADROOM_S = 0.008
DECLARED_MAX_HEADROOM_S = 0.100
