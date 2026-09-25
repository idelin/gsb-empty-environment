"""Windowed RMS + hysteresis loudness detection over multiple tracks.

Windows are aligned to absolute sample indices, so detection results do not
depend on how the audio is chunked during delivery.
"""

import math

from .constants import RMS_ABS_TOL, RMS_REL_TOL

STRATEGY_ANY = "any"  # combined loud if ANY track with data is loud
STRATEGY_ALL = "all"  # combined loud only if ALL tracks with data are loud


def _clearly_above(value, threshold):
    return value > threshold and not math.isclose(
        value, threshold, rel_tol=RMS_REL_TOL, abs_tol=RMS_ABS_TOL
    )


def _clearly_below(value, threshold):
    return value < threshold and not math.isclose(
        value, threshold, rel_tol=RMS_REL_TOL, abs_tol=RMS_ABS_TOL
    )


def hysteresis_update(rms, previous, low, high):
    """Per-track loudness state machine.

    Declared rules:
    - rms clearly above `high`  -> loud
    - rms clearly below `low`   -> silent
    - between thresholds, or within tolerance of either -> keep previous state
    - no data (rms is None)     -> keep previous state
    """
    if rms is None:
        return previous
    if _clearly_above(rms, high):
        return True
    if _clearly_below(rms, low):
        return False
    return previous


class StreamingDetector:
    """Emits one combined loud/silent boolean per window, incrementally.

    Gap semantics (declared): a track with no data in a window casts no vote;
    if no track votes, the window is silent. The detector keeps a per-window
    state history so it can rewind and reprocess when late audio lands in an
    already-processed region.
    """

    def __init__(self, tracks, window, low_threshold, high_threshold,
                 strategy=STRATEGY_ANY):
        if window <= 0:
            raise ValueError("window must be > 0")
        if not 0 <= low_threshold <= high_threshold:
            raise ValueError("require 0 <= low_threshold <= high_threshold")
        if strategy not in (STRATEGY_ANY, STRATEGY_ALL):
            raise ValueError("strategy must be 'any' or 'all'")
        rates = {t.sample_rate for t in tracks}
        if len(rates) > 1:
            raise ValueError("all tracks must share one sample rate")
        self._tracks = {t.name: t for t in tracks}
        self.window = window
        self.low = low_threshold
        self.high = high_threshold
        self.strategy = strategy
        self._states = {t.name: False for t in tracks}
        self.loud = []       # combined loud flag per processed window
        self._history = []   # per-window per-track states, for rewinding

    @property
    def processed_samples(self):
        return len(self.loud) * self.window

    def _process_window(self, index, clip=None):
        start = index * self.window
        end = start + self.window
        if clip is not None:
            end = min(end, clip)
        self._history.append(dict(self._states))
        votes = []
        for name, track in self._tracks.items():
            rms = track.rms_in(start, end)
            new_state = hysteresis_update(rms, self._states[name],
                                          self.low, self.high)
            self._states[name] = new_state
            if rms is not None:
                votes.append(new_state)
        if not votes:
            combined = False
        elif self.strategy == STRATEGY_ANY:
            combined = any(votes)
        else:
            combined = all(votes)
        self.loud.append(combined)

    def process_until(self, sample_end):
        """Process all complete windows below sample_end."""
        n = sample_end // self.window
        while len(self.loud) < n:
            self._process_window(len(self.loud))

    def finalize(self, total_samples):
        """Process the trailing partial window (RMS over available samples)."""
        self.process_until(total_samples)
        if total_samples % self.window and len(self.loud) == total_samples // self.window:
            self._process_window(len(self.loud), clip=total_samples)

    def rewind_to_window(self, index):
        """Drop windows >= index and restore the state from before window index."""
        index = max(0, min(index, len(self.loud)))
        if index < len(self.loud):
            self._states = dict(self._history[index])
            del self.loud[index:]
            del self._history[index:]

    def get_state(self):
        """JSON-serializable checkpoint of the detector."""
        return {
            "states": dict(self._states),
            "loud": list(self.loud),
            "history": [dict(h) for h in self._history],
        }

    def set_state(self, state):
        self._states = dict(state["states"])
        self.loud = [bool(v) for v in state["loud"]]
        self._history = [dict(h) for h in state["history"]]
