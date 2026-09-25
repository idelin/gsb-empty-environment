from __future__ import annotations

import hashlib
import json
from dataclasses import asdict, dataclass
from typing import Any

DFT_MODEL_VERSION = "real-dft-energy-v1"
COVERAGE_MODEL_VERSION = "dft-voronoi-v1"
ENERGY_ABSOLUTE_TOLERANCE = 1e-12
ENERGY_RELATIVE_TOLERANCE = 1e-12


@dataclass(frozen=True)
class Band:
    name: str
    low_hz: float
    high_hz: float


@dataclass(frozen=True)
class BandRuleSet:
    band_version: str
    bands: tuple[Band, ...]
    coverage_model_version: str = COVERAGE_MODEL_VERSION


@dataclass(frozen=True)
class SpectrumFrame:
    frame_id: str
    start_sample: int
    samples: tuple[float, ...]
    sample_rate: float
    frame_version: str = "frame-v1"
    source: str = "local-fixture"


@dataclass(frozen=True)
class SpectrumEstimate:
    frequencies: tuple[float, ...]
    energy_density: tuple[float, ...]
    resolution_hz: float
    total_energy: float
    dft_model_version: str = DFT_MODEL_VERSION


@dataclass(frozen=True)
class BandEnergy:
    name: str
    low_hz: float
    high_hz: float
    absolute_energy: float
    relative_energy: float | None
    coverage_weights: tuple[float, ...]


@dataclass(frozen=True)
class SourceEvidence:
    frame_id: str
    frame_version: str
    frame_source: str
    start_sample: int
    end_sample: int
    sample_rate: float
    frame_length: int
    frame_checksum_sha256: str
    bands_version: str
    band_checksum_sha256: str
    dft_model_version: str
    coverage_model_version: str
    frequency_resolution_hz: float
    dft_frequency_count: int


@dataclass
class FrameResult:
    event_id: str
    event_type: str
    frame_id: str
    start_sample: int
    end_sample: int
    frame_version: str
    bands_version: str
    bands: tuple[BandEnergy, ...]
    total_energy: float
    gross_coverage_energy: float
    union_energy: float
    duplicate_coverage_energy: float
    relative_energy: dict[str, float | None]
    duplicate_relative_energy: float | None
    source_evidence: SourceEvidence
    previous_event_id: str | None = None
    correction_reasons: tuple[str, ...] = ()
    absolute_energy_delta: dict[str, float] | None = None
    relative_energy_delta: dict[str, float | None] | None = None
    total_energy_delta: float | None = None
    duplicate_coverage_energy_delta: float | None = None


@dataclass(frozen=True)
class MissingInterval:
    start_sample: int
    end_sample: int
    length_samples: int
    missing_frame_starts: tuple[int, ...]


def canonical_json(value: Any) -> bytes:
    return json.dumps(value, sort_keys=True, separators=(",", ":")).encode("utf-8")


def sha256_json(value: Any) -> str:
    return hashlib.sha256(canonical_json(value)).hexdigest()


def frame_checksum(frame: SpectrumFrame) -> str:
    return sha256_json(
        {
            "frame_id": frame.frame_id,
            "start_sample": frame.start_sample,
            "sample_rate": frame.sample_rate,
            "frame_version": frame.frame_version,
            "source": frame.source,
            "samples": list(frame.samples),
        }
    )


def band_checksum(rules: BandRuleSet) -> str:
    return sha256_json(
        {
            "band_version": rules.band_version,
            "coverage_model_version": rules.coverage_model_version,
            "bands": [
                {"name": band.name, "low_hz": band.low_hz, "high_hz": band.high_hz}
                for band in rules.bands
            ],
        }
    )


def frame_to_dict(frame: SpectrumFrame) -> dict[str, Any]:
    data = asdict(frame)
    data["samples"] = list(frame.samples)
    return data


def frame_from_dict(data: dict[str, Any]) -> SpectrumFrame:
    return SpectrumFrame(
        frame_id=data["frame_id"],
        start_sample=data["start_sample"],
        samples=tuple(data["samples"]),
        sample_rate=data["sample_rate"],
        frame_version=data["frame_version"],
        source=data["source"],
    )


def rules_to_dict(rules: BandRuleSet) -> dict[str, Any]:
    return {
        "band_version": rules.band_version,
        "coverage_model_version": rules.coverage_model_version,
        "bands": [asdict(band) for band in rules.bands],
    }


def rules_from_dict(data: dict[str, Any]) -> BandRuleSet:
    return BandRuleSet(
        band_version=data["band_version"],
        bands=tuple(Band(**band) for band in data["bands"]),
        coverage_model_version=data["coverage_model_version"],
    )
