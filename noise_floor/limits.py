"""Declared latency and numeric-tolerance contract for the noise-floor model."""

from dataclasses import dataclass


@dataclass(frozen=True)
class Limits:
    #: Two quantile estimates are considered equal within this absolute
    #: tolerance (linear interpolation of float magnitudes around 1e2).
    quantile_abs_tol: float = 1e-9
    #: Update cost per add/mark/expiry, in comparisons against window size n.
    #: A manual binary search is used; no full re-sort ever happens per frame.
    update_comparisons: str = "O(log n) comparisons per update; no per-frame full sort"
    #: Query cost for an exact quantile (index selection on sorted storage).
    query_cost: str = "O(1) index access on maintained sorted storage"
    #: Reclamation rule: retained history never extends below the active
    #: query watermark, and never younger than the rolling window edge.
    reclamation_bound: str = "frames < min(query_watermark, window_edge) are reclaimed"


LIMITS = Limits()
