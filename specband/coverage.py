"""Public frequency-bin coverage model (rectangular bins).

Model definition (public, versioned with this module):
  * A one-sided spectrum of ``n_bins`` bins derived from ``n_samples``
    time-domain samples at ``sample_rate`` Hz has bin width
    ``df = sample_rate / n_samples``.
  * Bin ``k`` owns the half-open interval ``[k*df, (k+1)*df)``.
  * A band ``[lo, hi)`` covers bin ``k`` with weight
    ``|[lo, hi) ∩ [k*df, (k+1)*df)| / df``, i.e. the fraction of the bin
    interval lying inside the band.  Weights are in ``[0, 1]``; a band
    boundary may cut through a bin, yielding a partial weight.
"""

COVERAGE_MODEL_VERSION = "rect-bins/1.0"


def bin_width(sample_rate, n_samples):
    """Bin width df in Hz."""
    if n_samples <= 0:
        raise ValueError("n_samples must be positive")
    if sample_rate <= 0:
        raise ValueError("sample_rate must be positive")
    return sample_rate / n_samples


def coverage_weight(lo, hi, k, df):
    """Fraction of bin k's interval covered by band [lo, hi)."""
    if not lo < hi:
        raise ValueError("band must satisfy lo < hi")
    bin_lo = k * df
    bin_hi = bin_lo + df
    overlap = min(hi, bin_hi) - max(lo, bin_lo)
    if overlap <= 0.0:
        return 0.0
    return overlap / df


def band_weights(lo, hi, n_bins, df):
    """Coverage weights of band [lo, hi) over bins 0..n_bins-1."""
    return [coverage_weight(lo, hi, k, df) for k in range(n_bins)]
