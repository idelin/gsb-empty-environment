from __future__ import annotations

from dataclasses import dataclass, field, replace
from math import isfinite
from typing import Any, Final, Literal, Mapping


INSERT: Final = "insert"
RETRACT: Final = "retract"
UPSERT: Final = "upsert"
DELETE: Final = "delete"

RevisionOperation = Literal["insert", "retract"]
RevisionAction = Literal["upsert", "delete"]
IntervalStatus = Literal["definite", "reset", "unknown_gap"]


class CounterAnalysisError(ValueError):
    """Base class for invalid counter analysis input."""


class DuplicateSampleError(CounterAnalysisError):
    pass


class MissingSampleError(KeyError):
    pass


class UnknownSourceError(CounterAnalysisError):
    pass


@dataclass(frozen=True, slots=True)
class SourceSpec:
    """Counter width and the maximum interval with a decidable increment."""

    width: int
    max_gap: float | int

    def __post_init__(self) -> None:
        if not isinstance(self.width, int) or isinstance(self.width, bool):
            raise CounterAnalysisError("source width must be an integer")
        if not 1 <= self.width <= 1024:
            raise CounterAnalysisError("source width must be between 1 and 1024")
        if not isinstance(self.max_gap, (int, float)) or isinstance(self.max_gap, bool):
            raise CounterAnalysisError("source max_gap must be numeric")
        if not isfinite(self.max_gap) or self.max_gap <= 0:
            raise CounterAnalysisError("source max_gap must be positive")

    @property
    def modulus(self) -> int:
        return 1 << self.width

    @property
    def max_value(self) -> int:
        return self.modulus - 1


@dataclass(frozen=True, slots=True)
class CounterConfig:
    sources: Mapping[str, SourceSpec]
    version: str = "v1"

    def __post_init__(self) -> None:
        if not isinstance(self.version, str) or not self.version:
            raise CounterAnalysisError("config version must be a non-empty string")
        if not isinstance(self.sources, Mapping) or not self.sources:
            raise CounterAnalysisError("at least one source is required")
        for name, spec in self.sources.items():
            if not isinstance(name, str) or not name:
                raise CounterAnalysisError("source names must be non-empty strings")
            if not isinstance(spec, SourceSpec):
                raise CounterAnalysisError(f"source {name!r} must be a SourceSpec")

    def source(self, source: str) -> SourceSpec:
        try:
            return self.sources[source]
        except KeyError:
            raise UnknownSourceError(f"unknown source {source!r}") from None


@dataclass(frozen=True, slots=True)
class Sample:
    sample_id: str
    source: str
    event_time: float | int
    count: int
    seq: int = 0
    reset: bool = False

    @classmethod
    def from_dict(cls, value: Mapping[str, Any]) -> "Sample":
        return cls(
            sample_id=value["sample_id"],
            source=value["source"],
            event_time=value["event_time"],
            count=value["count"],
            seq=value.get("seq", 0),
            reset=value.get("reset", False),
        )

    def __post_init__(self) -> None:
        if not isinstance(self.sample_id, str) or not self.sample_id:
            raise CounterAnalysisError("sample_id must be a non-empty string")
        if not isinstance(self.source, str) or not self.source:
            raise CounterAnalysisError("source must be a non-empty string")
        if (
            not isinstance(self.event_time, (int, float))
            or isinstance(self.event_time, bool)
            or not isfinite(self.event_time)
        ):
            raise CounterAnalysisError("event_time must be a finite number")
        if (
            not isinstance(self.count, int)
            or isinstance(self.count, bool)
            or self.count < 0
        ):
            raise CounterAnalysisError("count must be a non-negative integer")
        if not isinstance(self.seq, int) or isinstance(self.seq, bool):
            raise CounterAnalysisError("seq must be an integer")
        if not isinstance(self.reset, bool):
            raise CounterAnalysisError("reset must be boolean")

    def with_config(self, config: CounterConfig) -> "Sample":
        spec = config.source(self.source)
        if self.count > spec.max_value:
            raise CounterAnalysisError(
                f"sample {self.sample_id!r} count {self.count} does not fit "
                f"{spec.width}-bit source {self.source!r}"
            )
        return self


@dataclass(frozen=True, slots=True)
class IntervalResult:
    interval_id: str
    source: str
    left_sample_id: str
    right_sample_id: str
    left_time: float | int
    right_time: float | int
    left_count: int
    right_count: int
    delta: int | None
    status: IntervalStatus
    config_version: str


@dataclass(frozen=True, slots=True)
class Revision:
    revision_id: int
    operation: RevisionOperation
    trigger_sample_id: str
    source: str
    action: RevisionAction
    interval: IntervalResult | None
    supersedes: tuple[str, ...]
    config_version: str


@dataclass(frozen=True, slots=True)
class RunSummary:
    run_id: str
    start_sample_id: str | None
    end_sample_id: str | None
    known_delta: int
    definite_interval_ids: tuple[str, ...]
    unknown_interval_ids: tuple[str, ...]
    exact: bool


@dataclass(frozen=True, slots=True)
class SourceSummary:
    source: str
    known_delta: int
    exact: bool
    reset_boundaries: int
    unknown_interval_ids: tuple[str, ...]
    definite_interval_ids: tuple[str, ...]
    runs: tuple[RunSummary, ...]
    config_version: str


@dataclass(frozen=True, slots=True)
class CounterSummary:
    known_delta: int
    exact: bool
    sources: tuple[SourceSummary, ...]


@dataclass(frozen=True, slots=True)
class ReferenceDiff:
    matches: bool
    missing_interval_ids: tuple[str, ...] = field(default_factory=tuple)
    unexpected_interval_ids: tuple[str, ...] = field(default_factory=tuple)
    changed_interval_ids: tuple[str, ...] = field(default_factory=tuple)
    summary_known_delta_delta: int = 0


def sample_order_key(sample: Sample) -> tuple[str, float | int, int, str]:
    return sample.source, sample.event_time, sample.seq, sample.sample_id


def as_sample(value: Sample | Mapping[str, Any]) -> Sample:
    if isinstance(value, Sample):
        return value
    if isinstance(value, Mapping):
        return Sample.from_dict(value)
    raise CounterAnalysisError("sample must be a Sample or a mapping")


def copy_config_sources(
    sources: Mapping[str, SourceSpec | tuple[int, float | int]],
) -> dict[str, SourceSpec]:
    result: dict[str, SourceSpec] = {}
    for name, spec in sources.items():
        result[name] = spec if isinstance(spec, SourceSpec) else SourceSpec(*spec)
    return result


def with_config_version(config: CounterConfig, version: str) -> CounterConfig:
    return replace(config, version=version)
