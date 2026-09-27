"""Candidate mechanisms under audit.

Every mechanism is "injectable": the caller supplies the dataset record and
the randomness source, so experiments are fully reproducible from a seed.

Datasets here are single-bit synthetic records; a pair of neighboring
datasets is simply ``0`` vs ``1`` (they differ in exactly one record).
"""

import math
import random


def randomized_response(record: int, eps: float, rng: random.Random) -> int:
    """eps-differentially-private randomized response on one bit.

    Returns the true bit with probability e^eps / (1 + e^eps), otherwise
    the flipped bit. For eps >= 0 this mechanism satisfies eps-DP.
    """
    if record not in (0, 1):
        raise ValueError("record must be 0 or 1")
    if eps < 0:
        raise ValueError("eps must be non-negative")
    p_truth = math.exp(eps) / (1.0 + math.exp(eps))
    if rng.random() < p_truth:
        return record
    return 1 - record


def deterministic_identity(record: int, eps: float, rng: random.Random) -> int:
    """Broken mechanism: returns the raw record, ignoring ``eps``.

    This mechanism satisfies NO finite eps-DP guarantee and is included so
    the auditor can demonstrate a detectable, reproducible violation.
    """
    if record not in (0, 1):
        raise ValueError("record must be 0 or 1")
    return record
