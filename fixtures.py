"""Local fixtures: explicit FIR coefficients and deterministic test signals.

No audio devices, no randomness, no I/O. Coefficients are computed here once
and then handed to the processor as plain explicit lists; the DSP modules
never design filters themselves.
"""

import math

from dsp_crossover import CrossoverBand
from dsp_dynamics import BandCompressor
from pipeline import MultibandProcessor

SAMPLE_RATE = 48000

# Declared numeric tolerances (also re-exported by the tests).
TOL_RECON = 1e-9        # bypass recombination vs delayed reference
TOL_PEAK = 1e-6         # limited peak vs smoothed threshold
TOL_GAIN_RATE = 1e-9    # gain trajectory vs analytic smoothing curve


def lowpass(cutoff_hz, numtaps, sample_rate=SAMPLE_RATE):
    """Windowed-sinc lowpass (Hamming), unity DC gain."""
    if numtaps % 2 == 0:
        raise ValueError("numtaps must be odd for an integer group delay")
    fc = cutoff_hz / sample_rate
    mid = (numtaps - 1) / 2.0
    h = []
    for n in range(numtaps):
        k = n - mid
        sinc = 2.0 * fc if k == 0 else math.sin(2.0 * math.pi * fc * k) / (math.pi * k)
        window = 0.54 - 0.46 * math.cos(2.0 * math.pi * n / (numtaps - 1))
        h.append(sinc * window)
    s = sum(h)
    return [c / s for c in h]


def bandpass(lo_hz, hi_hz, numtaps, sample_rate=SAMPLE_RATE):
    hi = lowpass(hi_hz, numtaps, sample_rate)
    lo = lowpass(lo_hz, numtaps, sample_rate)
    return [h - l for h, l in zip(hi, lo)]


def three_band_bands(sample_rate=SAMPLE_RATE, f_lo=500.0, f_hi=4000.0,
                     n_low=63, n_mid=31):
    """Complementary 3-band bank with *different* group delays per branch.

    low:  n_low taps (gd = (n_low-1)/2)
    mid:  n_mid taps (gd = (n_mid-1)/2)
    high: delta(max_gd) - low - delay(mid), so the aligned bands sum to a
          pure delay exactly (up to float rounding).
    """
    low = lowpass(f_lo, n_low, sample_rate)
    mid = bandpass(f_lo, f_hi, n_mid, sample_rate)
    gd_max = (n_low - 1) // 2
    shift = gd_max - (n_mid - 1) // 2
    high = []
    for n in range(n_low):
        d = 1.0 if n == gd_max else 0.0
        m = mid[n - shift] if 0 <= n - shift < n_mid else 0.0
        high.append(d - low[n] - m)
    return [
        CrossoverBand("low", low),
        CrossoverBand("mid", mid),
        CrossoverBand("high", high),
    ]


def make_processor(sample_rate=SAMPLE_RATE, channels=2,
                   comp_threshold_db=-20.0, ratio=4.0,
                   attack_ms=5.0, release_ms=80.0,
                   limiter_threshold_db=-6.0, lookahead_ms=5.0,
                   limiter_release_ms=50.0, threshold_smooth_ms=10.0,
                   n_low=63, n_mid=31):
    bands = three_band_bands(sample_rate, n_low=n_low, n_mid=n_mid)
    compressors = [
        BandCompressor(b.name, sample_rate, channels,
                       threshold_db=comp_threshold_db, ratio=ratio,
                       attack_ms=attack_ms, release_ms=release_ms)
        for b in bands
    ]
    limiter_params = {
        "lookahead_ms": lookahead_ms,
        "threshold_db": limiter_threshold_db,
        "release_ms": limiter_release_ms,
        "threshold_smooth_ms": threshold_smooth_ms,
    }
    return MultibandProcessor(sample_rate, bands, compressors,
                              limiter_params, channels=channels)


# ---------------------------------------------------------------- signals

def impulse(n, pos, amp=1.0):
    x = [0.0] * n
    x[pos] = amp
    return x


def multitone(n, freqs, amps, sample_rate=SAMPLE_RATE):
    return [
        sum(a * math.sin(2.0 * math.pi * f * i / sample_rate)
            for f, a in zip(freqs, amps))
        for i in range(n)
    ]


def stereo_right_spike(n, pos, amp=0.9):
    left = [0.0] * n
    right = impulse(n, pos, amp)
    return [left, right]


def with_unknown(x, positions):
    y = list(x)
    for p in positions:
        y[p] = float("nan")
    return y


def run_blocks(processor, blocks_per_channel, block_size):
    """Feed channel signals through a processor in fixed-size blocks."""
    n = len(blocks_per_channel[0])
    outs = [[] for _ in blocks_per_channel]
    for start in range(0, n, block_size):
        chunk = [ch[start:start + block_size] for ch in blocks_per_channel]
        got = processor.process_block(chunk)
        for ch, o in enumerate(got):
            outs[ch].extend(o)
    tail = processor.flush()
    for ch, o in enumerate(tail):
        outs[ch].extend(o)
    return outs
