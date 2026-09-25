"""N-band FIR crossover with group-delay-compensated recombination.

Each band is an explicitly supplied FIR. Bands may have different group
delays; at recombination every band is aligned to the slowest band with an
integer delay line, so the summed output is a pure delayed copy of whatever
the band filters sum to (for a complementary bank: the input itself).

Declared stage latency = max band group delay (integer samples).
"""

from dsp_fir import FIRFilter, IntegerDelay, detect_linear_phase_group_delay


class CrossoverBand:
    def __init__(self, name, coeffs, group_delay=None):
        self.name = name
        self.coeffs = [float(c) for c in coeffs]
        if group_delay is None:
            group_delay = detect_linear_phase_group_delay(self.coeffs)
            if group_delay is None:
                raise ValueError(
                    "band %r: not linear phase, supply group_delay explicitly" % name
                )
        if group_delay != int(group_delay):
            raise ValueError(
                "band %r: fractional group delay %s not supported" % (name, group_delay)
            )
        self.group_delay = int(group_delay)


class Crossover:
    """Splits one channel into aligned bands; summing the bands is exact."""

    def __init__(self, bands):
        if not bands:
            raise ValueError("need at least one band")
        self.bands = list(bands)
        self.max_group_delay = max(b.group_delay for b in self.bands)
        self._filters = [FIRFilter(b.coeffs) for b in self.bands]
        self._align = [
            IntegerDelay(self.max_group_delay - b.group_delay) for b in self.bands
        ]

    @property
    def latency_samples(self):
        return self.max_group_delay

    def process(self, block):
        """Return a list of aligned band blocks (same length as input)."""
        return [
            align.process(fir.process(block))
            for fir, align in zip(self._filters, self._align)
        ]
