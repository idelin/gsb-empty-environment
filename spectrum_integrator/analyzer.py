from __future__ import annotations

import json
import math
import os
from dataclasses import asdict
from typing import Any

from .core import integrate_bands, validate_band_rules
from .models import (
    DFT_MODEL_VERSION,
    BandEnergy,
    BandRuleSet,
    FrameResult,
    MissingInterval,
    SourceEvidence,
    SpectrumFrame,
    band_checksum,
    frame_checksum,
    frame_from_dict,
    frame_to_dict,
    rules_from_dict,
    rules_to_dict,
)

STATE_FORMAT_VERSION = "spectrum-integrator-state-v1"


class SpectrumIntegrator:
    """Append-only spectrum frame and band-version processor."""

    def __init__(
        self,
        sample_rate: float,
        frame_length: int,
        initial_bands: BandRuleSet,
        hop_samples: int,
    ) -> None:
        if frame_length < 2 or frame_length % 2:
            raise ValueError("frame_length must be even and at least two")
        if not math.isfinite(sample_rate) or sample_rate <= 0:
            raise ValueError("sample_rate must be positive")
        if hop_samples <= 0:
            raise ValueError("hop_samples must be positive")
        nyquist = sample_rate / 2.0
        validate_band_rules(initial_bands, nyquist)
        self.sample_rate = float(sample_rate)
        self.frame_length = frame_length
        self.hop_samples = hop_samples
        self.initial_bands = initial_bands
        self._band_timeline: list[tuple[int, BandRuleSet]] = [(0, initial_bands)]
        self._frames: dict[str, SpectrumFrame] = {}
        self._frame_ids_by_start: dict[int, str] = {}
        self._latest_results: dict[str, FrameResult] = {}
        self._events: list[FrameResult] = []

    @property
    def events(self) -> tuple[FrameResult, ...]:
        return tuple(self._events)

    @property
    def latest_results(self) -> dict[str, FrameResult]:
        return dict(self._latest_results)

    def set_band_rules(
        self, rules: BandRuleSet, effective_from_sample: int
    ) -> list[FrameResult]:
        if effective_from_sample < 0:
            raise ValueError("effective_from_sample cannot be negative")
        if effective_from_sample < self._band_timeline[-1][0]:
            raise ValueError("band versions must be added in increasing sample order")
        validate_band_rules(rules, self.sample_rate / 2.0)
        if any(
            rules.band_version == existing.band_version
            for _, existing in self._band_timeline
        ):
            raise ValueError(f"band version already exists: {rules.band_version}")
        self._band_timeline.append((effective_from_sample, rules))
        self._band_timeline.sort(key=lambda item: item[0])

        corrections: list[FrameResult] = []
        next_effective = self._next_band_effective_sample(effective_from_sample)
        for start in sorted(self._frame_ids_by_start):
            if start < effective_from_sample:
                continue
            if next_effective is not None and start >= next_effective:
                continue
            frame_id = self._frame_ids_by_start[start]
            previous = self._latest_results[frame_id]
            if previous.bands_version == rules.band_version:
                continue
            correction = self._calculate_event(
                self._frames[frame_id], ("band-rules-version-changed",), previous
            )
            self._latest_results[frame_id] = correction
            corrections.append(correction)
        return corrections

    def add_frame(self, frame: SpectrumFrame) -> FrameResult:
        self._validate_frame(frame)
        if frame.frame_id in self._frames:
            raise ValueError(f"frame already exists, use correct_frame: {frame.frame_id}")
        if frame.start_sample in self._frame_ids_by_start:
            raise ValueError(f"a frame already starts at sample {frame.start_sample}")
        result = self._calculate_event(frame, (), None)
        self._store_frame(frame)
        self._latest_results[frame.frame_id] = result
        return result

    def correct_frame(
        self,
        frame: SpectrumFrame,
        reasons: tuple[str, ...] = ("spectrum-frame-corrected",),
    ) -> FrameResult:
        self._validate_frame(frame)
        previous = self._latest_results.get(frame.frame_id)
        if previous is None:
            raise ValueError(f"cannot correct an unknown frame: {frame.frame_id}")
        if frame.start_sample != previous.start_sample:
            raise ValueError("frame corrections must preserve start_sample")
        if frame.start_sample not in self._frame_ids_by_start:
            raise RuntimeError("frame start index is inconsistent with processor state")
        result = self._calculate_event(frame, reasons, previous)
        self._store_frame(frame)
        self._latest_results[frame.frame_id] = result
        return result

    def missing_intervals(
        self,
        expected_through_sample: int | None = None,
    ) -> tuple[MissingInterval, ...]:
        if expected_through_sample is None:
            if not self._frame_ids_by_start:
                return ()
            horizon = max(
                start + self.frame_length for start in self._frame_ids_by_start
            )
        else:
            if expected_through_sample < 0:
                raise ValueError("expected_through_sample cannot be negative")
            horizon = expected_through_sample

        missing_starts: list[int] = []
        start = 0
        while start < horizon:
            if start not in self._frame_ids_by_start:
                missing_starts.append(start)
            start += self.hop_samples

        intervals: list[MissingInterval] = []
        index = 0
        while index < len(missing_starts):
            first = missing_starts[index]
            last = first
            index += 1
            while (
                index < len(missing_starts)
                and missing_starts[index] == last + self.hop_samples
            ):
                last = missing_starts[index]
                index += 1
            intervals.append(
                MissingInterval(
                    start_sample=first,
                    end_sample=last + self.frame_length,
                    length_samples=last + self.frame_length - first,
                    missing_frame_starts=tuple(
                        range(first, last + 1, self.hop_samples)
                    ),
                )
            )
        return tuple(intervals)

    def save_checkpoint(self, path: str) -> None:
        state = self.to_dict()
        temporary_path = f"{path}.tmp"
        with open(temporary_path, "w", encoding="utf-8") as handle:
            json.dump(state, handle, sort_keys=True, indent=2)
            handle.write("\n")
        os.replace(temporary_path, path)

    @classmethod
    def load_checkpoint(cls, path: str) -> SpectrumIntegrator:
        with open(path, "r", encoding="utf-8") as handle:
            state = json.load(handle)
        return cls.from_dict(state)

    def to_dict(self) -> dict[str, Any]:
        return {
            "state_format_version": STATE_FORMAT_VERSION,
            "sample_rate": self.sample_rate,
            "frame_length": self.frame_length,
            "hop_samples": self.hop_samples,
            "initial_bands": rules_to_dict(self.initial_bands),
            "band_timeline": [
                {"effective_from_sample": start, "rules": rules_to_dict(rules)}
                for start, rules in self._band_timeline
            ],
            "frames": [
                frame_to_dict(frame)
                for frame in sorted(self._frames.values(), key=lambda item: item.frame_id)
            ],
            "events": [asdict(event) for event in self._events],
        }

    @classmethod
    def from_dict(cls, state: dict[str, Any]) -> SpectrumIntegrator:
        if state["state_format_version"] != STATE_FORMAT_VERSION:
            raise ValueError("unsupported checkpoint state version")
        initial = rules_from_dict(state["initial_bands"])
        processor = cls(
            sample_rate=state["sample_rate"],
            frame_length=state["frame_length"],
            initial_bands=initial,
            hop_samples=state["hop_samples"],
        )
        timeline = [
            (entry["effective_from_sample"], rules_from_dict(entry["rules"]))
            for entry in state["band_timeline"]
        ]
        processor._band_timeline = timeline
        for frame_data in state["frames"]:
            frame = frame_from_dict(frame_data)
            processor._validate_frame(frame)
            processor._store_frame(frame)
        processor._events = [
            _event_from_dict(event_data) for event_data in state["events"]
        ]
        for event in processor._events:
            processor._latest_results[event.frame_id] = event
        return processor

    def _validate_frame(self, frame: SpectrumFrame) -> None:
        if not frame.frame_id:
            raise ValueError("frame_id must be non-empty")
        if frame.start_sample < 0:
            raise ValueError("start_sample cannot be negative")
        if frame.start_sample % self.hop_samples:
            raise ValueError("frame start_sample must align to hop_samples")
        if frame.sample_rate != self.sample_rate:
            raise ValueError("sample_rate differs from processor configuration")
        if len(frame.samples) != self.frame_length:
            raise ValueError("frame samples length differs from processor configuration")
        if not all(isinstance(value, (int, float)) for value in frame.samples):
            raise TypeError("frame samples must contain only real numbers")

    def _store_frame(self, frame: SpectrumFrame) -> None:
        self._frames[frame.frame_id] = frame
        self._frame_ids_by_start[frame.start_sample] = frame.frame_id

    def _next_band_effective_sample(self, effective_from_sample: int) -> int | None:
        for start, _rules in self._band_timeline:
            if start > effective_from_sample:
                return start
        return None

    def _rules_for_sample(self, start_sample: int) -> BandRuleSet:
        selected = self._band_timeline[0][1]
        for effective_from, rules in self._band_timeline:
            if start_sample >= effective_from:
                selected = rules
        return selected

    def _calculate_event(
        self,
        frame: SpectrumFrame,
        reasons: tuple[str, ...],
        previous: FrameResult | None,
    ) -> FrameResult:
        rules = self._rules_for_sample(frame.start_sample)
        spectrum, bands, gross, union, duplicate = integrate_bands(
            frame.samples, frame.sample_rate, rules
        )
        relative = {band.name: band.relative_energy for band in bands}
        duplicate_relative = (
            duplicate / spectrum.total_energy if spectrum.total_energy != 0.0 else None
        )
        evidence = SourceEvidence(
            frame_id=frame.frame_id,
            frame_version=frame.frame_version,
            frame_source=frame.source,
            start_sample=frame.start_sample,
            end_sample=frame.start_sample + len(frame.samples),
            sample_rate=frame.sample_rate,
            frame_length=len(frame.samples),
            frame_checksum_sha256=frame_checksum(frame),
            bands_version=rules.band_version,
            band_checksum_sha256=band_checksum(rules),
            dft_model_version=DFT_MODEL_VERSION,
            coverage_model_version=rules.coverage_model_version,
            frequency_resolution_hz=spectrum.resolution_hz,
            dft_frequency_count=len(spectrum.frequencies),
        )
        event_id = f"event-{len(self._events) + 1:06d}"
        if previous is not None:
            previous_event_id = previous.event_id
        else:
            previous_event_id = self._events[-1].event_id if self._events else None
        result = FrameResult(
            event_id=event_id,
            event_type="correction" if previous is not None else "frame",
            frame_id=frame.frame_id,
            start_sample=frame.start_sample,
            end_sample=frame.start_sample + len(frame.samples),
            frame_version=frame.frame_version,
            bands_version=rules.band_version,
            bands=bands,
            total_energy=spectrum.total_energy,
            gross_coverage_energy=gross,
            union_energy=union,
            duplicate_coverage_energy=duplicate,
            relative_energy=relative,
            duplicate_relative_energy=duplicate_relative,
            source_evidence=evidence,
            previous_event_id=previous_event_id,
            correction_reasons=tuple(reasons),
        )
        if previous is not None:
            old_bands = {band.name: band for band in previous.bands}
            new_bands = {band.name: band for band in bands}
            absolute_delta = {}
            relative_delta = {}
            ordered_names = list(old_bands)
            ordered_names.extend(name for name in new_bands if name not in old_bands)
            for name in ordered_names:
                old = old_bands.get(name)
                new = new_bands.get(name)
                old_absolute = old.absolute_energy if old is not None else 0.0
                new_absolute = new.absolute_energy if new is not None else 0.0
                absolute_delta[name] = new_absolute - old_absolute
                if (
                    old is None
                    or new is None
                    or old.relative_energy is None
                    or new.relative_energy is None
                ):
                    relative_delta[name] = None
                else:
                    relative_delta[name] = new.relative_energy - old.relative_energy
            result.absolute_energy_delta = absolute_delta
            result.relative_energy_delta = relative_delta
            result.total_energy_delta = result.total_energy - previous.total_energy
            result.duplicate_coverage_energy_delta = (
                result.duplicate_coverage_energy - previous.duplicate_coverage_energy
            )
        self._events.append(result)
        return result


def _event_from_dict(data: dict[str, Any]) -> FrameResult:
    bands = tuple(
        BandEnergy(
            name=band["name"],
            low_hz=band["low_hz"],
            high_hz=band["high_hz"],
            absolute_energy=band["absolute_energy"],
            relative_energy=band["relative_energy"],
            coverage_weights=tuple(band["coverage_weights"]),
        )
        for band in data["bands"]
    )
    evidence_data = data["source_evidence"]
    evidence = SourceEvidence(**evidence_data)
    return FrameResult(
        event_id=data["event_id"],
        event_type=data["event_type"],
        frame_id=data["frame_id"],
        start_sample=data["start_sample"],
        end_sample=data["end_sample"],
        frame_version=data["frame_version"],
        bands_version=data["bands_version"],
        bands=bands,
        total_energy=data["total_energy"],
        gross_coverage_energy=data["gross_coverage_energy"],
        union_energy=data["union_energy"],
        duplicate_coverage_energy=data["duplicate_coverage_energy"],
        relative_energy=dict(data["relative_energy"]),
        duplicate_relative_energy=data["duplicate_relative_energy"],
        source_evidence=evidence,
        previous_event_id=data["previous_event_id"],
        correction_reasons=tuple(data["correction_reasons"]),
        absolute_energy_delta=(
            dict(data["absolute_energy_delta"])
            if data["absolute_energy_delta"] is not None
            else None
        ),
        relative_energy_delta=(
            dict(data["relative_energy_delta"])
            if data["relative_energy_delta"] is not None
            else None
        ),
        total_energy_delta=data["total_energy_delta"],
        duplicate_coverage_energy_delta=data["duplicate_coverage_energy_delta"],
    )
