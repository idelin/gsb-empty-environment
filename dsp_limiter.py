"""Lookahead limiter with hot-updatable threshold and linked channels.

Design notes
------------
* Audio is delayed by exactly `lookahead` samples at all times, including
  while bypassed, so toggling enable/disable never changes latency.
* The required gain is computed at *output* time from the upcoming window
  [t, t+lookahead] using the *current* (smoothed) threshold. A threshold
  change therefore also applies to peaks already inside the lookahead
  region, so a hot update mid-lookahead cannot miss a peak.
* Threshold updates are ramped linearly in dB over `threshold_smooth_ms`
  to avoid zipper noise; the smoothed threshold trajectory is recorded so
  over-limit evidence can be checked against it.
* Gain smoothing: instant attack (the lookahead covers it), exponential
  release toward the required gain.
* Channels share one control gain (the most conservative), audio stays
  independent. A NaN anywhere in the window marks the gain unknown (NaN).
* Over-limit evidence records segments where the un-limited peak would
  exceed the current threshold; the waveform itself is never hard-clipped.
"""

import math
from collections import deque


def _db_to_lin(db):
    return 10.0 ** (db / 20.0)


class LookaheadLimiter:
    def __init__(self, sample_rate, channels, lookahead_ms=5.0,
                 threshold_db=-1.0, release_ms=50.0,
                 threshold_smooth_ms=10.0):
        self.sample_rate = sample_rate
        self.channels = channels
        self.lookahead = max(1, int(round(lookahead_ms * sample_rate / 1000.0)))
        self.release_ms = release_ms
        self._rel = math.exp(-1.0 / (sample_rate * release_ms / 1000.0))
        self._thr_smooth_len = max(
            1, int(round(threshold_smooth_ms * sample_rate / 1000.0))
        )
        self.threshold_db = float(threshold_db)
        self._thr_target = float(threshold_db)
        self._thr_step = 0.0
        self._thr_remaining = 0
        self.enabled = True
        # Delay lines are pre-filled with `lookahead` zeros so that every
        # input block yields an equal-length output block and the content
        # of input sample k always lands at output index k + lookahead.
        self._buffers = [
            deque([0.0] * self.lookahead) for _ in range(channels)
        ]
        self._gain = 1.0
        self.sample_index = 0
        self.evidence = []
        self._open = None

    @property
    def latency_samples(self):
        return self.lookahead

    def set_threshold_db(self, threshold_db):
        """Hot-update the threshold; ramps smoothly in the dB domain."""
        self._thr_target = float(threshold_db)
        self._thr_remaining = self._thr_smooth_len
        self._thr_step = (self._thr_target - self.threshold_db) / self._thr_smooth_len

    def set_enabled(self, enabled):
        """Bypass/restore. Latency is unaffected; gain ramps smoothly."""
        self.enabled = bool(enabled)

    def _update_threshold(self):
        if self._thr_remaining > 0:
            self.threshold_db += self._thr_step
            self._thr_remaining -= 1
            if self._thr_remaining == 0:
                self.threshold_db = self._thr_target

    def _window_peak(self):
        peak = 0.0
        unknown = False
        for buf in self._buffers:
            for i, v in enumerate(buf):
                if i > self.lookahead:
                    break
                if math.isnan(v):
                    unknown = True
                elif abs(v) > peak:
                    peak = abs(v)
        return peak, unknown

    def _record_evidence(self, peak, unknown, thr_lin, gain):
        over_limit = (not unknown) and peak > thr_lin
        if over_limit:
            if self._open is None:
                self._open = {
                    "start": self.sample_index,
                    "end": self.sample_index,
                    "peak": peak,
                    "threshold_db": self.threshold_db,
                    "min_gain": gain,
                }
            else:
                self._open["end"] = self.sample_index
                self._open["peak"] = max(self._open["peak"], peak)
                self._open["min_gain"] = min(self._open["min_gain"], gain)
        elif self._open is not None:
            self.evidence.append(self._open)
            self._open = None

    def _step(self):
        self._update_threshold()
        thr_lin = _db_to_lin(self.threshold_db)
        peak, unknown = self._window_peak()
        if unknown:
            g_req = float("nan")
        elif not self.enabled or peak <= thr_lin:
            g_req = 1.0
        else:
            g_req = thr_lin / peak
        if math.isnan(g_req):
            gain = float("nan")
        elif math.isnan(self._gain) or g_req < self._gain:
            gain = g_req
        else:
            gain = g_req + (self._gain - g_req) * self._rel
        self._gain = gain
        self._record_evidence(peak, unknown, thr_lin, g_req)
        out = [buf.popleft() * gain for buf in self._buffers]
        self.sample_index += 1
        return out, gain, self.threshold_db, g_req

    def process_block(self, blocks):
        """Consume one block per channel; returns (outs, gains, thrs, g_reqs)."""
        n = len(blocks[0])
        if any(len(b) != n for b in blocks):
            raise ValueError("channel blocks must have equal length")
        for buf, block in zip(self._buffers, blocks):
            buf.extend(block)
        outs = [[] for _ in range(self.channels)]
        gains, thrs, g_reqs = [], [], []
        for _ in range(n):
            samples, gain, thr, g_req = self._step()
            for ch, s in enumerate(samples):
                outs[ch].append(s)
            gains.append(gain)
            thrs.append(thr)
            g_reqs.append(g_req)
        return outs, gains, thrs, g_reqs

    def flush(self):
        """Emit the final `lookahead` tail samples; call once at stream end."""
        for buf in self._buffers:
            buf.extend([0.0] * self.lookahead)
        outs = [[] for _ in range(self.channels)]
        gains, thrs, g_reqs = [], [], []
        for _ in range(self.lookahead):
            samples, gain, thr, g_req = self._step()
            for ch, s in enumerate(samples):
                outs[ch].append(s)
            gains.append(gain)
            thrs.append(thr)
            g_reqs.append(g_req)
        if self._open is not None:
            self.evidence.append(self._open)
            self._open = None
        return outs, gains, thrs, g_reqs
