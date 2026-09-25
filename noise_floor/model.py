"""Rolling noise-floor model maintained per (scene, band).

Semantics implemented here:

* Samples are VALID, TRANSIENT or MISSING; only valid, in-window samples
  contribute to the noise floor.
* Late marks retract (or restore) contributions already incorporated, so
  an extreme peak first ingested and later marked transient leaves the
  baseline exactly as if it had never been included.
* A window with zero valid samples is NOT ESTIMABLE: estimates and
  thresholds are None, never 0.
* Every estimate/threshold is bound to the model version that produced it;
  detections recorded against a stale version can be corrected after late
  marks change the model.
* Resource reclamation is bounded by the active query watermark: retained
  history never reaches below min(query watermark, rolling window edge).
"""

from __future__ import annotations

import heapq
from dataclasses import dataclass

from .quantile import SortedMultiset

VALID = "valid"
TRANSIENT = "transient"
MISSING = "missing"
STATES = (VALID, TRANSIENT, MISSING)

#: add()/mark() return statuses
MARK_APPLIED = "applied"
MARK_NOOP = "noop"
MARK_UNKNOWN = "unknown"


@dataclass
class Sample:
    sample_id: int
    frame: int
    value: float | None
    state: str = VALID

    def to_dict(self) -> dict:
        return {
            "sample_id": self.sample_id,
            "frame": self.frame,
            "value": self.value,
            "state": self.state,
        }

    @classmethod
    def from_dict(cls, data: dict) -> "Sample":
        return cls(
            sample_id=data["sample_id"],
            frame=data["frame"],
            value=data["value"],
            state=data["state"],
        )


@dataclass(frozen=True)
class Estimate:
    """Noise-floor estimate; value is None when not estimable."""

    value: float | None
    n_valid: int
    version: int


@dataclass(frozen=True)
class Threshold:
    """Detection threshold bound to the model version that produced it."""

    value: float | None
    version: int
    quantile: float
    margin: float


@dataclass
class Detection:
    frame: int
    value: float
    threshold_version: int
    detected: bool

    def to_dict(self) -> dict:
        return {
            "frame": self.frame,
            "value": self.value,
            "threshold_version": self.threshold_version,
            "detected": self.detected,
        }

    @classmethod
    def from_dict(cls, data: dict) -> "Detection":
        return cls(
            frame=data["frame"],
            value=data["value"],
            threshold_version=data["threshold_version"],
            detected=data["detected"],
        )


@dataclass(frozen=True)
class Correction:
    frame: int
    value: float
    was_detected: bool
    now_detected: bool
    old_version: int
    new_version: int


class BandModel:
    """Rolling noise-floor model for one (scene, band) pair."""

    def __init__(self, window_frames: int, quantile: float = 0.5, margin: float = 6.0):
        if window_frames < 1:
            raise ValueError("window_frames must be >= 1")
        if not 0.0 <= quantile <= 1.0:
            raise ValueError("quantile must be in [0, 1]")
        self.window_frames = window_frames
        self.quantile_q = quantile
        self.margin = margin
        self.version = 0
        # All retained samples (live + expired-but-markable history).
        self._samples: dict[int, Sample] = {}
        # Live (in-window) membership only, for exact expiry.
        self._live_by_frame: dict[int, list[int]] = {}
        self._live_frames: list[int] = []  # heap of live frames
        # Values of valid, in-window samples.
        self._active = SortedMultiset()
        self._max_frame = -1
        self._watermark = 0
        self._detections: list[Detection] = []

    # -- ingestion ------------------------------------------------------

    def add(self, sample_id: int, frame: int, value: float | None,
            state: str = VALID) -> str:
        """Ingest one sample; frames may arrive out of order or in chunks."""
        if state not in STATES:
            raise ValueError(f"unknown state {state!r}")
        if state == VALID and value is None:
            raise ValueError("valid samples require a value")
        existing = self._samples.get(sample_id)
        if existing is not None:
            if (existing.frame, existing.value, existing.state) == (frame, value, state):
                return MARK_NOOP
            raise ValueError(f"conflicting re-add of sample {sample_id}")
        sample = Sample(sample_id=sample_id, frame=frame, value=value, state=state)
        self._samples[sample_id] = sample
        if frame > self._max_frame:
            self._max_frame = frame
            self._expire()
        if self._in_window(frame):
            self._live_by_frame.setdefault(frame, []).append(sample_id)
            heapq.heappush(self._live_frames, frame)
            if state == VALID:
                self._active.add(value)
        self.version += 1
        return MARK_APPLIED

    def _in_window(self, frame: int) -> bool:
        return frame > self._max_frame - self.window_frames

    def _expire(self) -> None:
        edge = self._max_frame - self.window_frames
        heap = self._live_frames
        while heap and heap[0] <= edge:
            frame = heapq.heappop(heap)
            ids = self._live_by_frame.pop(frame, [])
            for sid in ids:
                sample = self._samples[sid]
                if sample.state == VALID:
                    self._active.discard(sample.value)
                # Expired samples stay in _samples (still late-markable)
                # until the query watermark reclaims them.

    # -- marking ----------------------------------------------------------

    def mark(self, sample_id: int, state: str) -> str:
        """Mark a sample; idempotent, retracts/restores its contribution."""
        if state not in STATES:
            raise ValueError(f"unknown state {state!r}")
        sample = self._samples.get(sample_id)
        if sample is None:
            return MARK_UNKNOWN  # never seen or already reclaimed
        if sample.state == state:
            return MARK_NOOP
        was_valid = sample.state == VALID
        becomes_valid = state == VALID
        if becomes_valid and sample.value is None:
            raise ValueError("cannot mark a valueless sample valid")
        sample.state = state
        if self._in_window(sample.frame):
            if was_valid and not becomes_valid:
                self._active.discard(sample.value)
            elif becomes_valid and not was_valid:
                self._active.add(sample.value)
        self.version += 1
        return MARK_APPLIED

    # -- estimation -------------------------------------------------------

    def estimate(self) -> Estimate:
        return Estimate(
            value=self._active.quantile(self.quantile_q),
            n_valid=len(self._active),
            version=self.version,
        )

    def threshold(self) -> Threshold:
        est = self.estimate()
        value = None if est.value is None else est.value + self.margin
        return Threshold(value=value, version=self.version,
                         quantile=self.quantile_q, margin=self.margin)

    # -- detection --------------------------------------------------------

    def detect(self, frame: int, value: float) -> bool:
        th = self.threshold()
        detected = th.value is not None and value > th.value
        self._detections.append(Detection(
            frame=frame, value=value,
            threshold_version=th.version, detected=detected))
        return detected

    def correct_detections(self) -> list[Correction]:
        """Re-evaluate recorded detections against the current model.

        Detections bound to a stale model version are re-decided with the
        current threshold and rebound to the current version.
        """
        th = self.threshold()
        corrections: list[Correction] = []
        for det in self._detections:
            if det.threshold_version == th.version:
                continue
            now = th.value is not None and det.value > th.value
            if now != det.detected:
                corrections.append(Correction(
                    frame=det.frame, value=det.value,
                    was_detected=det.detected, now_detected=now,
                    old_version=det.threshold_version, new_version=th.version))
            det.detected = now
            det.threshold_version = th.version
        return corrections

    @property
    def detections(self) -> tuple[Detection, ...]:
        return tuple(self._detections)

    def sample_ids_at(self, frame: int) -> list[int]:
        """Retained sample ids at a frame (live or expired history)."""
        return [sid for sid, s in self._samples.items() if s.frame == frame]

    # -- reclamation ------------------------------------------------------

    @property
    def watermark(self) -> int:
        return self._watermark

    @property
    def retained(self) -> int:
        return len(self._samples)

    def set_watermark(self, frame: int) -> int:
        """Advance the active query watermark; reclaim history below it.

        Reclamation is bounded by min(watermark, window edge) so samples
        still contributing to the rolling estimate are never dropped.
        Returns the number of reclaimed samples.
        """
        self._watermark = max(self._watermark, frame)
        bound = min(self._watermark, self._max_frame - self.window_frames + 1)
        doomed = [sid for sid, s in self._samples.items() if s.frame < bound]
        for sid in doomed:
            del self._samples[sid]
        return len(doomed)

    # -- persistence --------------------------------------------------------

    def to_dict(self) -> dict:
        return {
            "window_frames": self.window_frames,
            "quantile": self.quantile_q,
            "margin": self.margin,
            "version": self.version,
            "max_frame": self._max_frame,
            "watermark": self._watermark,
            "samples": [s.to_dict() for s in self._samples.values()],
            "detections": [d.to_dict() for d in self._detections],
        }

    @classmethod
    def from_dict(cls, data: dict) -> "BandModel":
        model = cls(window_frames=data["window_frames"],
                    quantile=data["quantile"], margin=data["margin"])
        model.version = data["version"]
        model._max_frame = data["max_frame"]
        model._watermark = data["watermark"]
        for sdata in data["samples"]:
            sample = Sample.from_dict(sdata)
            model._samples[sample.sample_id] = sample
            if model._in_window(sample.frame):
                model._live_by_frame.setdefault(sample.frame, []).append(
                    sample.sample_id)
                model._live_frames.append(sample.frame)
                if sample.state == VALID:
                    model._active.add(sample.value)
        heapq.heapify(model._live_frames)
        model._detections = [Detection.from_dict(d) for d in data["detections"]]
        return model


class NoiseFloorBank:
    """Lazily created per-(scene, band) models with scene switching.

    Switching scenes never disturbs other scenes: each (scene, band) keeps
    its own baseline, version history and retained samples.
    """

    def __init__(self, window_frames: int, quantile: float = 0.5,
                 margin: float = 6.0):
        self.window_frames = window_frames
        self.quantile = quantile
        self.margin = margin
        self._models: dict[tuple[str, str], BandModel] = {}
        self.scene: str | None = None

    def set_scene(self, scene: str) -> None:
        self.scene = scene

    def model(self, band: str, scene: str | None = None) -> BandModel:
        scene = self.scene if scene is None else scene
        if scene is None:
            raise ValueError("no scene selected")
        key = (scene, band)
        if key not in self._models:
            self._models[key] = BandModel(
                window_frames=self.window_frames,
                quantile=self.quantile, margin=self.margin)
        return self._models[key]

    def scenes(self) -> list[str]:
        return sorted({scene for scene, _ in self._models})

    def to_dict(self) -> dict:
        return {
            "window_frames": self.window_frames,
            "quantile": self.quantile,
            "margin": self.margin,
            "scene": self.scene,
            "models": {f"{scene}|{band}": m.to_dict()
                       for (scene, band), m in self._models.items()},
        }

    @classmethod
    def from_dict(cls, data: dict) -> "NoiseFloorBank":
        bank = cls(window_frames=data["window_frames"],
                   quantile=data["quantile"], margin=data["margin"])
        bank.scene = data["scene"]
        for key, mdata in data["models"].items():
            scene, band = key.split("|", 1)
            bank._models[(scene, band)] = BandModel.from_dict(mdata)
        return bank
