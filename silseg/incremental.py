"""Incremental segmentation with stable IDs, patch events and checkpoints.

Patch event vocabulary (plain dicts, JSON-serializable):
  {"op": "add",    "id": ..., "start": s, "end": e}
  {"op": "revise", "id": ..., "old": [s, e], "new": [s2, e2]}  # corrects an
                                                               # exported segment
  {"op": "remove", "id": ..., "reason": "merged", "merged_into": id}
  {"op": "remove", "id": ..., "reason": "retracted"}
"""

from .detector import STRATEGY_ANY, StreamingDetector
from .segments import ExportedSegment, Segment, extract_segments, to_windows


class IncrementalSegmenter:
    """Re-segments locally when audio is appended/filled or silence markers
    are revised, and reports the change as a minimal patch of events."""

    def __init__(self, tracks, *, window, low_threshold, high_threshold,
                 strategy=STRATEGY_ANY, min_silence=0, min_sound=0,
                 pre_pad=0, post_pad=0):
        for name, value in (("min_silence", min_silence), ("min_sound", min_sound),
                            ("pre_pad", pre_pad), ("post_pad", post_pad)):
            if value < 0:
                raise ValueError("%s must be >= 0" % name)
        self._tracks = {t.name: t for t in tracks}
        self._detector = StreamingDetector(
            list(self._tracks.values()), window,
            low_threshold, high_threshold, strategy,
        )
        self.window = window
        self.low_threshold = low_threshold
        self.high_threshold = high_threshold
        self.strategy = strategy
        self.min_silence = min_silence
        self.min_sound = min_sound
        self.pre_pad = pre_pad
        self.post_pad = post_pad
        self._markers = []   # (start, end, is_silence)
        self._segments = []  # list[Segment]
        self._next_id = 1
        self._total = 0

    # ------------------------------------------------------------------ API

    @property
    def segments(self):
        return list(self._segments)

    @property
    def latency_samples(self):
        """Declared latency: one RMS window, plus the silence run required to
        confirm a segment end (min_silence rounded up to whole windows), plus
        the post-pad."""
        return (self.window
                + to_windows(self.min_silence, self.window) * self.window
                + self.post_pad)

    def finalize(self):
        """Run the initial/full segmentation; returns the patch events."""
        return self._refresh()

    def add_audio(self, track_name, start, samples):
        """Append or gap-fill audio, re-segment locally, return patch events."""
        track = self._tracks[track_name]
        track.add_samples(start, samples)
        if start < self._detector.processed_samples:
            self._detector.rewind_to_window(start // self.window)
        return self._refresh()

    def revise_silence(self, start, end, is_silence=True):
        """Record a silence/sound marker overriding detection on the windows
        it intersects, re-segment locally, return patch events."""
        self._markers.append((start, end, bool(is_silence)))
        return self._refresh()

    def export(self):
        """Export current segments with per-track samples (gaps as None).

        Segments are pairwise disjoint by construction, so padded regions
        that overlap are exported exactly once (no duplicated samples).
        """
        return [
            ExportedSegment(
                seg.seg_id, seg.start, seg.end,
                {name: t.samples_in(seg.start, seg.end)
                 for name, t in self._tracks.items()},
            )
            for seg in self._segments
        ]

    def checkpoint(self):
        """JSON-serializable snapshot; safe to take mid candidate-silence."""
        return {
            "params": {
                "window": self.window,
                "low_threshold": self.low_threshold,
                "high_threshold": self.high_threshold,
                "strategy": self.strategy,
                "min_silence": self.min_silence,
                "min_sound": self.min_sound,
                "pre_pad": self.pre_pad,
                "post_pad": self.post_pad,
            },
            "detector": self._detector.get_state(),
            "segments": [[s.seg_id, s.start, s.end] for s in self._segments],
            "next_id": self._next_id,
            "markers": [list(m) for m in self._markers],
            "total": self._total,
        }

    @classmethod
    def restore(cls, tracks, state):
        """Rebuild a segmenter from a checkpoint. `tracks` must hold the same
        audio that had been added when the checkpoint was taken."""
        obj = cls(tracks, **state["params"])
        obj._detector.set_state(state["detector"])
        obj._segments = [Segment(sid, s, e) for sid, s, e in state["segments"]]
        obj._next_id = state["next_id"]
        obj._markers = [tuple(m) for m in state["markers"]]
        obj._total = state["total"]
        return obj

    # ------------------------------------------------------------- internal

    def _alloc_id(self):
        seg_id = "S%04d" % self._next_id
        self._next_id += 1
        return seg_id

    def _effective_loud(self):
        eff = list(self._detector.loud)
        for mstart, mend, is_silence in self._markers:
            w0 = mstart // self.window
            w1 = to_windows(mend, self.window)
            for i in range(w0, min(w1, len(eff))):
                eff[i] = not is_silence
        return eff

    def _refresh(self):
        self._total = max((t.extent for t in self._tracks.values()), default=0)
        self._detector.finalize(self._total)
        bounds = extract_segments(
            self._effective_loud(), self.window, self._total,
            min_silence=self.min_silence, min_sound=self.min_sound,
            pre_pad=self.pre_pad, post_pad=self.post_pad,
        )
        events, self._segments = self._diff(self._segments, bounds)
        return events

    def _diff(self, old, new_bounds):
        """Match old segments to new bounds by sample overlap; keep IDs stable.

        Keeper choice: maximum overlap, ties broken by segment order. Old
        segments left unclaimed are removed ('merged' if they overlap a kept
        successor, else 'retracted'); new bounds without a keeper are added
        with fresh IDs.
        """
        pairs = []
        for ni, (s, e) in enumerate(new_bounds):
            for oi, o in enumerate(old):
                overlap = min(e, o.end) - max(s, o.start)
                if overlap > 0:
                    pairs.append((overlap, ni, oi))
        pairs.sort(key=lambda p: (-p[0], p[1], p[2]))
        keep = {}
        claimed = {}
        for _overlap, ni, oi in pairs:
            if ni in keep or oi in claimed:
                continue
            keep[ni] = oi
            claimed[oi] = ni
        events = []
        result = []
        for ni, (s, e) in enumerate(new_bounds):
            if ni in keep:
                o = old[keep[ni]]
                seg = Segment(o.seg_id, s, e)
                if (o.start, o.end) != (s, e):
                    events.append({"op": "revise", "id": o.seg_id,
                                   "old": [o.start, o.end], "new": [s, e]})
            else:
                seg = Segment(self._alloc_id(), s, e)
                events.append({"op": "add", "id": seg.seg_id,
                               "start": s, "end": e})
            result.append(seg)
        for oi, o in enumerate(old):
            if oi in claimed:
                continue
            into = None
            for ni, (s, e) in enumerate(new_bounds):
                if min(e, o.end) - max(s, o.start) > 0:
                    into = result[ni].seg_id
                    break
            event = {"op": "remove", "id": o.seg_id,
                     "reason": "merged" if into else "retracted"}
            if into:
                event["merged_into"] = into
            events.append(event)
        return events, result


def segment_tracks(tracks, **params):
    """Batch helper: segment fully-populated tracks in one shot."""
    segmenter = IncrementalSegmenter(tracks, **params)
    segmenter.finalize()
    return segmenter.segments
