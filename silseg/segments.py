"""Segment extraction from a per-window loudness sequence."""

from dataclasses import dataclass


@dataclass
class Segment:
    """A sound segment with a stable, traceable ID. Bounds are sample indices."""

    seg_id: str
    start: int
    end: int

    @property
    def duration(self):
        return self.end - self.start


@dataclass
class ExportedSegment:
    """A segment plus its per-track samples (gaps exported as None)."""

    seg_id: str
    start: int
    end: int
    tracks: dict

    @property
    def duration(self):
        return self.end - self.start


def to_windows(samples, window):
    """Declared rounding: sample durations round UP to whole windows."""
    return -(-samples // window)


def _runs(seq, value):
    runs = []
    start = None
    for i, v in enumerate(seq):
        if v == value and start is None:
            start = i
        elif v != value and start is not None:
            runs.append((start, i))
            start = None
    if start is not None:
        runs.append((start, len(seq)))
    return runs


def extract_segments(loud, window, total_samples, *,
                     min_silence=0, min_sound=0, pre_pad=0, post_pad=0):
    """Turn a per-window loud sequence into padded, merged (start, end) bounds.

    Order of operations (declared):
    1. drop loud runs shorter than min_sound (short noise is absorbed);
    2. fill silent runs shorter than min_silence (short gaps are bridged);
    3. expand each remaining loud run by pre_pad / post_pad (samples);
    4. merge overlapping or touching intervals; clip to [0, total_samples].
    """
    seq = list(loud)
    min_sound_w = to_windows(min_sound, window)
    if min_sound_w:
        for a, b in _runs(seq, True):
            if b - a < min_sound_w:
                for i in range(a, b):
                    seq[i] = False
    min_silence_w = to_windows(min_silence, window)
    if min_silence_w:
        for a, b in _runs(seq, False):
            if b - a < min_silence_w:
                for i in range(a, b):
                    seq[i] = True
    segments = []
    for a, b in _runs(seq, True):
        s = max(0, a * window - pre_pad)
        e = min(total_samples, b * window + post_pad)
        if segments and s <= segments[-1][1]:
            prev_s, prev_e = segments[-1]
            segments[-1] = (prev_s, max(prev_e, e))
        else:
            segments.append((s, e))
    return segments
