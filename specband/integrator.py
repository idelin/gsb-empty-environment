"""Integrate a spectrum frame into a versioned set of frequency bands.

Declared budgets (see README):
  * DECLARED_FRAME_LATENCY_MS: per-frame integration latency budget on
    the fixture grids (naive DFT + O(bands x bins) weighting).
  * NUMERICAL_TOLERANCE: absolute tolerance for energy comparisons;
    fsum-based accumulation keeps error far below this on fixture grids.

Overlapping bands are never renormalised: band energies are reported
against the *spectrum* total, so overlapping band energies may sum to
more than the total.  The repeated-coverage contribution is reported
explicitly instead of being hidden by normalisation.
"""

import math
from dataclasses import dataclass
from typing import Optional

from .bands import BandSet
from .coverage import band_weights, bin_width

NUMERICAL_TOLERANCE = 1e-9
DECLARED_FRAME_LATENCY_MS = 2.0


@dataclass(frozen=True)
class BandReport:
    name: str
    absolute_energy: float
    # None when the frame's total energy is zero (relative value unknown).
    relative_energy: Optional[float]
    # Energy of this band lying in bins also covered by >= 1 other band.
    repeated_coverage_energy: float


@dataclass(frozen=True)
class IntegrationResult:
    seq: Optional[int]
    frame_version: Optional[int]
    band_version: str
    reports: tuple
    total_energy: float
    # Energy in bins covered by two or more bands (double-counted by
    # design in the per-band absolutes, never normalised away).
    shared_energy: float

    def report(self, name):
        for r in self.reports:
            if r.name == name:
                return r
        raise KeyError(name)

    def energy_map(self):
        return {r.name: r.absolute_energy for r in self.reports}


def integrate(spectrum, *, sample_rate, n_samples, band_set: BandSet,
              seq=None, frame_version=None):
    """Integrate ``spectrum`` into every band of ``band_set``."""
    if not isinstance(band_set, BandSet):
        raise TypeError("band_set must be a BandSet")
    n_bins = len(spectrum)
    df = bin_width(sample_rate, n_samples)
    weights = {
        b.name: band_weights(b.lo, b.hi, n_bins, df) for b in band_set.bands
    }
    cover_count = [
        sum(1 for b in band_set.bands if weights[b.name][k] > 0.0)
        for k in range(n_bins)
    ]
    total = math.fsum(spectrum)
    reports = []
    for band in band_set.bands:
        w = weights[band.name]
        absolute = math.fsum(spectrum[k] * w[k] for k in range(n_bins))
        repeated = math.fsum(
            spectrum[k] * w[k] for k in range(n_bins) if cover_count[k] >= 2
        )
        relative = absolute / total if total > 0.0 else None
        reports.append(BandReport(band.name, absolute, relative, repeated))
    shared = math.fsum(
        spectrum[k] for k in range(n_bins) if cover_count[k] >= 2
    )
    return IntegrationResult(
        seq=seq,
        frame_version=frame_version,
        band_version=band_set.version,
        reports=tuple(reports),
        total_energy=total,
        shared_energy=shared,
    )
