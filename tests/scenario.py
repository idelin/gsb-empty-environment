"""Shared fault-injection scenario used by the parity test and the demo."""

from gradacc.model import init_params

LR = 0.05
MOMENTUM = 0.9
SEED = 7


def make_dataset():
    xs = [[0.5, 1.0], [1.5, -0.5], [-1.0, 0.5], [2.0, 1.0],
          [0.0, -1.0], [1.0, 1.5], [-0.5, -0.5], [2.5, 0.0]]
    return [(x, [2.0 * x[0] - x[1] + 0.5]) for x in xs]


def make_init_params():
    return init_params(in_dim=2, out_dim=1, seed=SEED)


BATCHES = {
    "A": {"m1": [0, 1], "m2": [2], "m3": [3], "m9": [4]},
    "B": {"m1": [4], "m2": [5]},
    "C": {"m1": [6, 7]},
    "D": {"m1": [0]},
    "E": {"m1": [1], "m2": [2, 3]},
}

EVENTS = [
    ("begin", "A"), ("begin", "D"),
    ("micro", "A", "m2"),            # out-of-order delivery
    ("micro", "A", "m1"),
    ("corrupt", "A", "m3"),          # bad copy of m3 ...
    ("micro", "A", "m3"),            # ... replaced by a clean retry
    ("micro", "A", "m9"),
    ("revoke", "A", "m9"),           # revoked before commit
    ("micro", "D", "m1"),
    ("commit", "A"),
    ("commit", "A"),                 # duplicate commit: idempotent
    ("commit", "D"),                 # stale: rejected
    ("begin", "B"),
    ("micro", "B", "m1"),
    ("corrupt", "B", "m2", "nan"),   # non-finite gradient
    ("commit", "B"),                 # whole batch skipped
    ("begin", "C"),
    ("micro", "C", "m1"),
    ("arm_crash",),
    ("commit", "C"),                 # crash after prepare; recover; publish
    ("begin", "E"),
    ("micro", "E", "m1"),
    ("zero", "E"),                   # cleared before refill
    ("micro", "E", "m2"),
    ("commit", "E"),
]
