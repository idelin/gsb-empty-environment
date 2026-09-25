"""Frame ingestion engine: versioning, corrections, checkpoints.

Rules that keep versions from mixing:
  * Every IntegrationResult is tagged with the frame version and the
    band-set version that produced it; historical results are never
    re-tagged.
  * A frame correction (same seq, new frame version) is always diffed
    against the *old frame re-integrated under the band set currently
    in force*, so a delta never straddles two band versions.  The
    provenance record states all three versions explicitly.
"""

import json
import math
from dataclasses import dataclass, field

from .bands import BandSet
from .integrator import integrate
from .spectrum import power_spectrum


@dataclass(frozen=True)
class SpectrumFrame:
    seq: int
    version: int
    samples: tuple
    sample_rate: float

    def __post_init__(self):
        object.__setattr__(self, "samples", tuple(float(s) for s in self.samples))
        if self.version < 1:
            raise ValueError("frame version must be >= 1")

    def to_dict(self):
        return {
            "seq": self.seq,
            "version": self.version,
            "samples": list(self.samples),
            "sample_rate": self.sample_rate,
        }

    @classmethod
    def from_dict(cls, data):
        return cls(data["seq"], data["version"], data["samples"], data["sample_rate"])


@dataclass(frozen=True)
class Correction:
    """Incremental correction emitted when a frame version is replaced."""

    seq: int
    old_frame_version: int
    new_frame_version: int
    band_version: str
    deltas: dict  # band name -> new absolute energy - old absolute energy
    provenance: dict

    def to_dict(self):
        return {
            "seq": self.seq,
            "old_frame_version": self.old_frame_version,
            "new_frame_version": self.new_frame_version,
            "band_version": self.band_version,
            "deltas": self.deltas,
            "provenance": self.provenance,
        }


class Engine:
    """Stateful integrator over a versioned frame stream."""

    def __init__(self, band_set: BandSet):
        self.band_set = band_set
        self.frames = {}    # seq -> latest SpectrumFrame
        self.results = {}   # seq -> latest IntegrationResult
        self.history = []   # every IntegrationResult ever produced (evidence)
        self.corrections = []
        self._events = [("bands", band_set.to_dict())]

    # -- band-set versioning -------------------------------------------------
    def switch_band_set(self, band_set: BandSet):
        if band_set.version == self.band_set.version:
            raise ValueError(f"band set {band_set.version!r} already in force")
        self.band_set = band_set
        self._events.append(("bands", band_set.to_dict()))

    # -- frame ingestion -----------------------------------------------------
    def ingest(self, frame: SpectrumFrame):
        """Ingest a frame; returns (result, correction-or-None)."""
        result = self._integrate(frame)
        correction = None
        old_frame = self.frames.get(frame.seq)
        if old_frame is not None and old_frame.version != frame.version:
            if frame.version < old_frame.version:
                raise ValueError(
                    f"seq {frame.seq}: frame version regressed "
                    f"({old_frame.version} -> {frame.version})"
                )
            correction = self._make_correction(old_frame, frame, result)
            self.corrections.append(correction)
        self.frames[frame.seq] = frame
        self.results[frame.seq] = result
        self.history.append(result)
        self._events.append(("ingest", frame.to_dict()))
        return result, correction

    def _integrate(self, frame):
        spectrum = power_spectrum(frame.samples)
        return integrate(
            spectrum,
            sample_rate=frame.sample_rate,
            n_samples=len(frame.samples),
            band_set=self.band_set,
            seq=frame.seq,
            frame_version=frame.version,
        )

    def _make_correction(self, old_frame, new_frame, new_result):
        # Old frame re-integrated under the band set *currently in force*:
        # the delta never mixes band versions.
        old_result = self._integrate(old_frame)
        deltas = {
            r.name: new_result.report(r.name).absolute_energy - r.absolute_energy
            for r in old_result.reports
        }
        provenance = {
            "evidence": "frame-correction",
            "seq": new_frame.seq,
            "old_frame_version": old_frame.version,
            "new_frame_version": new_frame.version,
            "band_version_used_for_both_sides": self.band_set.version,
            "old_side_recomputed": True,
        }
        return Correction(
            seq=new_frame.seq,
            old_frame_version=old_frame.version,
            new_frame_version=new_frame.version,
            band_version=self.band_set.version,
            deltas=deltas,
            provenance=provenance,
        )

    # -- missing-frame statistics -------------------------------------------
    def missing_intervals(self):
        """Inclusive (start, end) seq intervals with no frame, within span."""
        seqs = sorted(self.frames)
        intervals = []
        for a, b in zip(seqs, seqs[1:]):
            if b > a + 1:
                intervals.append((a + 1, b - 1))
        return intervals

    def missing_stats(self):
        intervals = self.missing_intervals()
        missing = sum(b - a + 1 for a, b in intervals)
        longest = max((b - a + 1 for a, b in intervals), default=0)
        if self.frames:
            span = max(self.frames) - min(self.frames) + 1
            coverage = len(self.frames) / span
        else:
            span = 0
            coverage = 0.0
        return {
            "intervals": [list(i) for i in intervals],
            "missing_count": missing,
            "longest_interval": longest,
            "span": span,
            "frame_coverage": coverage,
        }

    # -- checkpoint / restore -------------------------------------------------
    def checkpoint(self):
        """Serialise the full event log; restore() replays it exactly."""
        return json.dumps({"events": self._events}, sort_keys=True)

    @classmethod
    def restore(cls, payload):
        data = json.loads(payload)
        engine = None
        for kind, body in data["events"]:
            if kind == "bands":
                band_set = BandSet.from_dict(body)
                if engine is None:
                    engine = cls(band_set)
                else:
                    engine.switch_band_set(band_set)
            elif kind == "ingest":
                engine.ingest(SpectrumFrame.from_dict(body))
            else:  # pragma: no cover - defensive
                raise ValueError(f"unknown event kind {kind!r}")
        if engine is None:
            raise ValueError("checkpoint contains no band-set event")
        return engine

    # -- evidence snapshot -----------------------------------------------------
    def snapshot(self):
        """Canonical JSON of current results, corrections and gap stats."""
        return json.dumps(
            {
                "band_version": self.band_set.version,
                "results": {
                    str(seq): {
                        "frame_version": r.frame_version,
                        "band_version": r.band_version,
                        "energies": r.energy_map(),
                        "total_energy": r.total_energy,
                        "shared_energy": r.shared_energy,
                    }
                    for seq, r in sorted(self.results.items())
                },
                "corrections": [c.to_dict() for c in self.corrections],
                "missing": self.missing_stats(),
            },
            sort_keys=True,
        )
