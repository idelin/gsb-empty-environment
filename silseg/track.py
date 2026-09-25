"""Track: a named mono sample sequence that may contain gaps (missing data)."""

import math


class Track:
    """Samples addressed by absolute index; regions never written are gaps.

    A gap means "no data" (distinct from true silence, which is real 0.0
    samples). Gaps are exposed to exporters as None entries.
    """

    def __init__(self, name, sample_rate):
        self.name = name
        self.sample_rate = sample_rate
        self._chunks = []  # list of (start, [float]), sorted, non-overlapping

    def add_samples(self, start, samples):
        samples = list(samples)
        if not samples:
            return
        if start < 0:
            raise ValueError("start must be >= 0")
        end = start + len(samples)
        for cstart, csamples in self._chunks:
            cend = cstart + len(csamples)
            if start < cend and cstart < end:
                raise ValueError(
                    "chunk [%d, %d) overlaps existing [%d, %d)"
                    % (start, end, cstart, cend)
                )
        self._chunks.append((start, samples))
        self._chunks.sort(key=lambda c: c[0])

    @property
    def extent(self):
        """End index (exclusive) of the last written sample, 0 if empty."""
        return max((s + len(d) for s, d in self._chunks), default=0)

    def samples_in(self, start, end):
        """Samples in [start, end); gap positions are None."""
        out = []
        for pos in range(start, end):
            value = None
            for cstart, csamples in self._chunks:
                if cstart <= pos < cstart + len(csamples):
                    value = csamples[pos - cstart]
                    break
            out.append(value)
        return out

    def rms_in(self, start, end):
        """RMS over available samples in [start, end); None if no data."""
        total = 0.0
        count = 0
        for cstart, csamples in self._chunks:
            lo = max(start, cstart)
            hi = min(end, cstart + len(csamples))
            for i in range(lo, hi):
                v = csamples[i - cstart]
                total += v * v
                count += 1
        if count == 0:
            return None
        return math.sqrt(total / count)
