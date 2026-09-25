"""Confidence-range calibration via synthetic occlusion experiments, plus a
model registry with version rollback.

A synthetic occlusion experiment hides known segments of a clean signal,
repairs them, and scores the repaired values against the withheld truth.
The resulting calibration rescales the declared error estimates so the
confidence intervals reach the target coverage. Calibrated models are
registered as versions; a version that validates worse than its
predecessor is rolled back.
"""

from __future__ import annotations

import math
from dataclasses import dataclass, replace

from .core import NUMERICAL_TOLERANCE, RepairConfig, repair_all


@dataclass(frozen=True)
class ModelVersion:
    name: str
    error_scale: float
    metrics: dict


class ModelRegistry:
    """Ordered registry of model versions with rollback support."""

    def __init__(self):
        self._versions: list[ModelVersion] = []
        self._active: ModelVersion | None = None

    @property
    def active(self) -> ModelVersion | None:
        return self._active

    @property
    def versions(self) -> list[ModelVersion]:
        return list(self._versions)

    def register(self, version: ModelVersion, activate: bool = True) -> ModelVersion:
        if any(v.name == version.name for v in self._versions):
            raise ValueError(f"duplicate version {version.name!r}")
        self._versions.append(version)
        if activate or self._active is None:
            self._active = version
        return version

    def rollback(self, name: str | None = None) -> ModelVersion:
        """Revert the active version to a previously registered one.

        Without ``name`` this steps back to the version registered just
        before the current active one.
        """
        if self._active is None:
            raise RuntimeError("no active version to roll back from")
        if name is None:
            index = self._versions.index(self._active)
            if index == 0:
                raise RuntimeError("no earlier version to roll back to")
            self._active = self._versions[index - 1]
        else:
            target = next((v for v in self._versions if v.name == name), None)
            if target is None:
                raise KeyError(f"unknown version {name!r}")
            self._active = target
        return self._active


def synthetic_occlusion_experiment(clean_samples, config: RepairConfig, occlusions):
    """Occlude known segments one at a time, repair, and score vs. truth.

    ``occlusions`` is a list of ``(start, length)`` pairs applied to a copy
    of ``clean_samples``. Returns coverage of the declared confidence
    intervals and the sorted |error|/estimate ratios used for calibration.
    """
    abs_errors = []
    estimates = []
    for start, length in occlusions:
        occluded = list(clean_samples)
        for i in range(start, start + length):
            occluded[i] = float("nan")
        result = repair_all(occluded, config)
        for report in result.gaps:
            if report.start != start or report.error_estimates is None:
                continue
            for k, est in enumerate(report.error_estimates):
                truth = clean_samples[start + k]
                pred = result.samples[start + k]
                abs_errors.append(abs(pred - truth))
                estimates.append(est)
    if not estimates:
        return {
            "samples": 0,
            "coverage": None,
            "mean_absolute_error": None,
            "ratios": [],
        }
    z = config.confidence_z
    hits = sum(1 for e, s in zip(abs_errors, estimates) if e <= z * s)
    ratios = sorted(e / s for e, s in zip(abs_errors, estimates) if s > 0.0)
    return {
        "samples": len(estimates),
        "coverage": hits / len(estimates),
        "mean_absolute_error": sum(abs_errors) / len(abs_errors),
        "ratios": ratios,
    }


def calibrate_error_scale(clean_samples, config: RepairConfig, occlusions,
                          target_coverage: float = 0.95):
    """Compute the error scale that reaches ``target_coverage``.

    The config used here should carry ``error_scale == 1.0`` so the raw
    estimates are calibrated exactly once.
    """
    metrics = synthetic_occlusion_experiment(clean_samples, config, occlusions)
    ratios = metrics["ratios"]
    if not ratios:
        return 1.0, metrics
    index = min(len(ratios) - 1, math.ceil(target_coverage * len(ratios)) - 1)
    scale = ratios[index] / config.confidence_z
    # Absorb floating-point rounding so the boundary sample is covered.
    scale *= 1.0 + 1e-9
    return max(scale, 1e-6), metrics


def calibrate_and_register(clean_samples, base_config: RepairConfig,
                           train_occlusions, val_occlusions,
                           registry: ModelRegistry, new_name: str,
                           target_coverage: float = 0.95):
    """Calibrate a new error scale, register it, and roll back if it
    validates worse than the previously active version."""
    raw_config = replace(base_config, error_scale=1.0)
    scale, train_metrics = calibrate_error_scale(
        clean_samples, raw_config, train_occlusions, target_coverage
    )
    candidate_config = replace(base_config, error_scale=scale)
    train_calibrated = synthetic_occlusion_experiment(
        clean_samples, candidate_config, train_occlusions
    )
    val_metrics = synthetic_occlusion_experiment(
        clean_samples, candidate_config, val_occlusions
    )
    candidate = ModelVersion(
        name=new_name,
        error_scale=scale,
        metrics={
            "train_coverage": train_calibrated["coverage"],
            "train_coverage_raw": train_metrics["coverage"],
            "val_coverage": val_metrics["coverage"],
            "target_coverage": target_coverage,
        },
    )
    previous = registry.active
    registry.register(candidate, activate=True)
    rolled_back = False
    if previous is not None:
        previous_config = replace(base_config, error_scale=previous.error_scale)
        previous_val = synthetic_occlusion_experiment(
            clean_samples, previous_config, val_occlusions
        )
        if (
            previous_val["coverage"] is not None
            and val_metrics["coverage"] is not None
            and val_metrics["coverage"] < previous_val["coverage"] - NUMERICAL_TOLERANCE
        ):
            registry.rollback(previous.name)
            rolled_back = True
    return {
        "active": registry.active,
        "candidate": candidate,
        "rolled_back": rolled_back,
        "train_coverage": train_calibrated["coverage"],
        "train_coverage_raw": train_metrics["coverage"],
        "val_coverage": val_metrics["coverage"],
    }
