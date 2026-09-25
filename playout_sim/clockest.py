"""Source/local clock drift estimation.

For each accepted arrival we observe
    offset = recv_time - src_pos / fs
which equals (network delay) - (drift accumulation). Network jitter is a
non-negative spike on top of a slowly moving floor, so the *floor* of
the offsets tracks the clock drift and is immune to jitter bursts.

We bin offsets in time, take a low percentile per bin (robust floor),
and fit a line with the Theil-Sen median-slope estimator (robust to the
few bins that a burst corrupts). slope < 0 means the source clock runs
fast. Memory is fixed: at most window_s/bin_s + 1 bins are kept.
"""

import statistics


class DriftEstimator:
    def __init__(self, bin_s=0.25, window_s=6.0, min_bins=12,
                 max_drift_ppm=2000.0, floor_percentile=0.1):
        self.bin_s = bin_s
        self.window_bins = max(1, int(round(window_s / bin_s)))
        self.min_bins = min_bins
        self.max_drift = max_drift_ppm * 1e-6
        self.floor_percentile = floor_percentile
        self.bins = {}          # bin index -> [offsets]
        self.slope = 0.0        # seconds of offset drift per second
        self.intercept = 0.0
        self.have_fit = False
        self._dirty = False

    def reset_bins(self):
        """Epoch change: offsets jump, but the clock itself is unchanged,
        so keep the last fit and only drop the observations."""
        self.bins = {}
        self._dirty = False

    def add(self, recv_time, offset):
        b = int(recv_time / self.bin_s)
        self.bins.setdefault(b, []).append(offset)
        cutoff = b - self.window_bins
        for old in [k for k in self.bins if k < cutoff]:
            del self.bins[old]
        self._dirty = True

    def ensure_fit(self):
        if self._dirty and len(self.bins) >= self.min_bins:
            self._refit()
            self._dirty = False

    def _floor_points(self):
        points = []
        for b, values in sorted(self.bins.items()):
            values = sorted(values)
            k = min(len(values) - 1, int(self.floor_percentile * len(values)))
            points.append(((b + 0.5) * self.bin_s, values[k]))
        return points

    def _refit(self):
        points = self._floor_points()
        slope, intercept = self._theil_sen(points)
        # Second stage: reject bins corrupted by jitter bursts (large
        # positive residual against the floor fit) and refit on the
        # clean floor, so bursts cannot bias the drift estimate.
        residuals = [y - (slope * x + intercept) for x, y in points]
        mad = statistics.median(abs(r) for r in residuals)
        limit = max(0.004, 6.0 * mad)
        clean = [p for p, r in zip(points, residuals) if r <= limit]
        if len(clean) >= max(self.min_bins, len(points) // 2):
            slope, intercept = self._theil_sen(clean)
        self.slope = slope
        self.intercept = intercept
        self.have_fit = True

    def _theil_sen(self, points):
        slopes = []
        for i in range(len(points)):
            xi, yi = points[i]
            for j in range(i + 1, len(points)):
                xj, yj = points[j]
                dx = xj - xi
                if dx > 0.0:
                    slopes.append((yj - yi) / dx)
        if not slopes:
            return self.slope, self.intercept
        slope = statistics.median(slopes)
        slope = max(-self.max_drift, min(self.max_drift, slope))
        intercept = statistics.median(y - slope * x for x, y in points)
        return slope, intercept

    @property
    def drift_ppm(self):
        """Estimated source clock drift relative to the local clock."""
        return -self.slope * 1e6

    @property
    def rate_ratio(self):
        """Source samples produced per local (render) sample."""
        return 1.0 - self.slope

    def residual(self, recv_time, offset):
        """Offset above the fitted floor: the jitter component."""
        return offset - (self.slope * recv_time + self.intercept)

    def to_state(self):
        return {
            "bins": {str(k): list(v) for k, v in self.bins.items()},
            "slope": self.slope,
            "intercept": self.intercept,
            "have_fit": self.have_fit,
            "dirty": self._dirty,
        }

    def load_state(self, state):
        self.bins = {int(k): list(v) for k, v in state["bins"].items()}
        self.slope = state["slope"]
        self.intercept = state["intercept"]
        self.have_fit = state["have_fit"]
        self._dirty = state["dirty"]
