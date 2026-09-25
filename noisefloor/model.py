"""Rolling noise-floor model maintained per scene and per frequency band.

Declared latency (W = window length, R = frames retained inside the watermark):

  - ``ingest`` / ``mark`` : O(log W) value comparisons + O(W) list shift.
  - ``estimate`` / ``detect`` : O(1).
  - ``reclaim`` : O(R).
  - No full sort ever happens per frame.  A full sort occurs only when a
    snapshot is restored (and inside the test oracle, by design).

Declared numerical tolerance: estimates are deterministic functions of the
retained samples.  Equivalent histories (chunked, out-of-order, save/restore)
produce bit-identical results; any cross-path comparison uses ``TOL``.
"""

from __future__ import annotations

import bisect
import math
from dataclasses import dataclass

VALID = "valid"
TRANSIENT = "transient"
MISSING = "missing"
STATUSES = (VALID, TRANSIENT, MISSING)

TOL = 1e-12


class _SortedMultiset:
    """Sorted list with lazy, comparison-counted binary-search updates."""

    __slots__ = ("data", "comparisons")

    def __init__(self, values=()):
        # sorted() is used only on the restore path, never per frame.
        self.data = sorted(values)
        self.comparisons = 0

    def _bisect_left(self, x):
        lo, hi = 0, len(self.data)
        while lo < hi:
            mid = (lo + hi) >> 1
            self.comparisons += 1
            if self.data[mid] < x:
                lo = mid + 1
            else:
                hi = mid
        return lo

    def add(self, x):
        self.data.insert(self._bisect_left(x), x)

    def discard(self, x):
        i = self._bisect_left(x)
        if i < len(self.data) and self.data[i] == x:
            self.data.pop(i)
            return True
        return False

    def quantile(self, q):
        """Exact linear-interpolation (type-7) quantile; duplicates safe."""
        n = len(self.data)
        if n == 0:
            return None
        if n == 1:
            return self.data[0]
        h = (n - 1) * q
        lo = int(h)
        frac = h - lo
        if frac == 0.0:
            return self.data[lo]
        return self.data[lo] + (self.data[lo + 1] - self.data[lo]) * frac

    def __len__(self):
        return len(self.data)


@dataclass(frozen=True)
class Estimate:
    """A threshold/baseline result bound to the model version that made it."""

    estimable: bool
    value: float | None
    n_valid: int
    quantile: float
    scene: str
    band: str
    version: int


@dataclass
class Detection:
    det_id: int
    scene: str
    band: str
    frame: int | None
    value: float
    factor: float
    baseline: float | None
    decision: bool | None
    version: int


@dataclass(frozen=True)
class Correction:
    det_id: int
    old_decision: bool | None
    new_decision: bool | None
    old_version: int
    new_version: int

    @property
    def changed(self):
        return self.old_decision != self.new_decision


class _BandState:
    """Rolling window over the last ``window`` valid frames of one band."""

    __slots__ = ("window", "frames", "values", "status", "sorted_values")

    def __init__(self, window):
        self.window = window
        self.frames = []          # sorted frame indices currently VALID
        self.values = {}          # retained frame -> float
        self.status = {}          # retained frame -> status
        self.sorted_values = _SortedMultiset()  # values inside the window

    @property
    def n_valid(self):
        return len(self.frames)

    def add_sample(self, frame, value, status):
        if frame in self.status:
            return False  # duplicate ingest is a no-op
        self.status[frame] = status
        if value is not None:
            self.values[frame] = float(value)
        if status == VALID:
            self._insert_valid(frame, float(value))
        return True

    def set_status(self, frame, status):
        old = self.status.get(frame)
        if old is None or old == status:
            return False  # unknown/reclaimed frame or idempotent re-mark
        if status == VALID and frame not in self.values:
            return False  # cannot validate a sample with no retained value
        self.status[frame] = status
        if old == VALID:
            self._remove_valid(frame)
        if status == VALID:
            self._insert_valid(frame, self.values[frame])
        return True

    def _insert_valid(self, frame, value):
        p = bisect.bisect_left(self.frames, frame)
        self.frames.insert(p, frame)
        L, W = len(self.frames), self.window
        if p >= L - W:  # newcomer lands inside the window
            if L - 1 >= W:  # window was full -> evict its former head
                self.sorted_values.discard(self.values[self.frames[L - 1 - W]])
            self.sorted_values.add(value)

    def _remove_valid(self, frame):
        p = bisect.bisect_left(self.frames, frame)
        L, W = len(self.frames), self.window
        in_window = p >= L - W
        if in_window:
            self.sorted_values.discard(self.values[frame])
        del self.frames[p]
        if in_window and L > W:
            # the frame just outside the window slides back in
            self.sorted_values.add(self.values[self.frames[L - 1 - W]])

    def reclaim(self, watermark):
        """Drop retained frames older than ``watermark`` and outside the window."""
        dropped = 0
        if self.frames:
            first_window = max(0, len(self.frames) - self.window)
            cut = 0
            while cut < first_window and self.frames[cut] < watermark:
                cut += 1
            if cut:
                for f in self.frames[:cut]:
                    self.status.pop(f, None)
                    self.values.pop(f, None)
                del self.frames[:cut]
                dropped += cut
        stale = [f for f, s in self.status.items()
                 if s != VALID and f < watermark]
        for f in stale:
            self.status.pop(f, None)
            self.values.pop(f, None)
            dropped += 1
        return dropped

    def to_state(self):
        return {
            "frames": list(self.frames),
            "values": [[f, v] for f, v in self.values.items()],
            "status": [[f, s] for f, s in self.status.items()],
        }

    @classmethod
    def from_state(cls, window, state):
        st = cls(window)
        st.frames = list(state["frames"])
        st.values = {f: v for f, v in state["values"]}
        st.status = {f: s for f, s in state["status"]}
        window_vals = [st.values[f] for f in st.frames[-window:]]
        st.sorted_values = _SortedMultiset(window_vals)
        return st


class NoiseFloorModel:
    """Per-scene, per-band rolling noise-floor model.

    Parameters
    ----------
    window : int
        Number of most recent *valid* frames kept in each band window.
    quantile : float in [0, 1]
        Noise-floor quantile (exact, linear interpolation).
    sample_rate, hop : int
        Fixture-provided sampling metadata; hop / sample_rate is the
        declared frame latency granularity.
    """

    def __init__(self, window=64, quantile=0.2, sample_rate=16000, hop=256):
        if not 0.0 <= quantile <= 1.0:
            raise ValueError("quantile must be in [0, 1]")
        if window < 1:
            raise ValueError("window must be >= 1")
        self.window = window
        self.quantile = quantile
        self.sample_rate = sample_rate
        self.hop = hop
        self.scene = "default"
        self.version = 0
        self._bands = {}
        self._queries = {}
        self._next_qid = 1
        self._detections = []
        self._next_det_id = 1

    # ------------------------------------------------------------------ scene
    def set_scene(self, scene):
        """Switch the active scene; every scene keeps its own baselines."""
        self.scene = scene

    def _state(self, scene, band):
        key = (scene, band)
        st = self._bands.get(key)
        if st is None:
            st = self._bands[key] = _BandState(self.window)
        return st

    # ----------------------------------------------------------------- ingest
    def ingest(self, frame, band, value, status=VALID, scene=None):
        """Add one sample; returns True if it changed the model."""
        if status not in STATUSES:
            raise ValueError(f"unknown status {status!r}")
        st = self._state(scene or self.scene, band)
        changed = st.add_sample(frame, value, status)
        if changed:
            self.version += 1
        return changed

    def ingest_sample(self, sample):
        return self.ingest(sample.frame, sample.band, sample.value,
                           sample.status, scene=sample.scene)

    def ingest_chunk(self, samples):
        """Chunked ingest; equivalent to per-sample ingest in any order."""
        return sum(1 for s in samples if self.ingest_sample(s))

    # ------------------------------------------------------------------- mark
    def mark(self, frame, band, status, scene=None):
        """(Late-)mark a sample; idempotent; may retract a live contribution."""
        if status not in STATUSES:
            raise ValueError(f"unknown status {status!r}")
        st = self._bands.get((scene or self.scene, band))
        if st is None:
            return False
        changed = st.set_status(frame, status)
        if changed:
            self.version += 1
        return changed

    # ---------------------------------------------------------------- queries
    def estimate(self, band, scene=None, q=None):
        scene = scene or self.scene
        q = self.quantile if q is None else q
        st = self._bands.get((scene, band))
        n = len(st.sorted_values) if st else 0
        if n == 0:
            return Estimate(False, None, 0, q, scene, band, self.version)
        return Estimate(True, st.sorted_values.quantile(q), n, q,
                        scene, band, self.version)

    def valid_count(self, band, scene=None):
        st = self._bands.get((scene or self.scene, band))
        return st.n_valid if st else 0

    def detect(self, band, value, frame=None, factor=3.0, scene=None):
        """Threshold test; the result is bound to the current model version."""
        scene = scene or self.scene
        est = self.estimate(band, scene)
        decision = None if not est.estimable else value > factor * est.value
        det = Detection(self._next_det_id, scene, band, frame, float(value),
                        factor, est.value, decision, self.version)
        self._next_det_id += 1
        self._detections.append(det)
        return det

    def correct_detections(self):
        """Re-evaluate detections whose bound version is stale.

        Returns the list of corrections; detections are updated in place.
        """
        corrections = []
        for det in self._detections:
            if det.version == self.version:
                continue
            est = self.estimate(det.band, det.scene)
            new = None if not est.estimable else det.value > det.factor * est.value
            corrections.append(Correction(det.det_id, det.decision, new,
                                          det.version, self.version))
            det.decision = new
            det.baseline = est.value
            det.version = self.version
        return corrections

    # ------------------------------------------------------- watermark/reclaim
    def acquire_query(self, oldest_frame):
        """Register an active query; frames >= oldest_frame stay retractable."""
        qid = self._next_qid
        self._next_qid += 1
        self._queries[qid] = oldest_frame
        return qid

    def release_query(self, qid):
        self._queries.pop(qid, None)

    def watermark(self):
        """Oldest frame that must remain correctable; +inf if unbounded."""
        if not self._queries:
            return math.inf
        return min(self._queries.values())

    def reclaim(self):
        """Reclaim history older than the active-query watermark.

        With no active queries everything outside the live windows is
        reclaimed.  Frames inside a live window are always kept.
        """
        wm = self.watermark()
        dropped = sum(st.reclaim(wm) for st in self._bands.values())
        if self._detections:
            self._detections = [d for d in self._detections
                                if d.frame is None or d.frame >= wm]
        return dropped

    def retained_frames(self, band, scene=None):
        st = self._bands.get((scene or self.scene, band))
        return sorted(st.status) if st else []

    def comparisons(self, band, scene=None):
        """Value-comparison counter of one band's window (test hook)."""
        st = self._bands.get((scene or self.scene, band))
        return st.sorted_values.comparisons if st else 0

    # ------------------------------------------------------------ save/restore
    def to_state(self):
        return {
            "window": self.window,
            "quantile": self.quantile,
            "sample_rate": self.sample_rate,
            "hop": self.hop,
            "scene": self.scene,
            "version": self.version,
            "bands": [
                {"scene": s, "band": b, **st.to_state()}
                for (s, b), st in self._bands.items()
            ],
            "detections": [vars(d) for d in self._detections],
            "next_det_id": self._next_det_id,
        }

    @classmethod
    def from_state(cls, state):
        m = cls(state["window"], state["quantile"],
                state["sample_rate"], state["hop"])
        m.scene = state["scene"]
        m.version = state["version"]
        for b in state["bands"]:
            m._bands[(b["scene"], b["band"])] = _BandState.from_state(
                state["window"], b)
        m._detections = [Detection(**d) for d in state["detections"]]
        m._next_det_id = state["next_det_id"]
        return m
