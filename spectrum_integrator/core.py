from __future__ import annotations

import cmath
import math
from typing import Iterable

from .models import (
    COVERAGE_MODEL_VERSION,
    ENERGY_ABSOLUTE_TOLERANCE,
    Band,
    BandEnergy,
    BandRuleSet,
    SpectrumEstimate,
)

_WEIGHT_EPSILON = 1e-14


def validate_band_rules(rules: BandRuleSet, nyquist_hz: float) -> None:
    if rules.coverage_model_version != COVERAGE_MODEL_VERSION:
        raise ValueError(f"unsupported coverage model: {rules.coverage_model_version}")
    if not rules.bands:
        raise ValueError("at least one band is required")
    names: set[str] = set()
    for band in rules.bands:
        if not math.isfinite(band.low_hz) or not math.isfinite(band.high_hz):
            raise ValueError(f"non-finite band bounds: {band.name}")
        if not band.name:
            raise ValueError("band names must be non-empty")
        if band.name in names:
            raise ValueError(f"duplicate band name: {band.name}")
        names.add(band.name)
        if not 0.0 <= band.low_hz < band.high_hz <= nyquist_hz:
            raise ValueError(f"invalid band {band.name}: {band.low_hz}..{band.high_hz}")


def _dft_real(samples: tuple[float, ...]) -> list[complex]:
    length = len(samples)
    coefficients: list[complex] = []
    for k in range(length):
        coefficient = 0.0j
        angle = -2.0j * math.pi * k / length
        for index, sample in enumerate(samples):
            coefficient += sample * cmath.exp(angle * index)
        coefficients.append(coefficient)
    return coefficients


def estimate_spectrum(samples: Iterable[float], sample_rate: float) -> SpectrumEstimate:
    sample_tuple = tuple(float(sample) for sample in samples)
    if not all(math.isfinite(sample) for sample in sample_tuple):
        raise ValueError("frame samples must be finite")
    length = len(sample_tuple)
    if length < 2 or length % 2:
        raise ValueError("an even frame length of at least two is required")
    if not math.isfinite(sample_rate) or sample_rate <= 0:
        raise ValueError("sample_rate must be positive")

    coefficients = _dft_real(sample_tuple)
    nyquist_index = length // 2
    resolution = sample_rate / length
    frequencies = tuple(k * resolution for k in range(nyquist_index + 1))
    density = [0.0] * (nyquist_index + 1)
    density[0] = abs(coefficients[0]) ** 2 / sample_rate
    for k in range(1, nyquist_index):
        density[k] = 2.0 * abs(coefficients[k]) ** 2 / sample_rate
    density[nyquist_index] = abs(coefficients[nyquist_index]) ** 2 / sample_rate
    total_energy = resolution * sum(density)
    return SpectrumEstimate(
        frequencies=frequencies,
        energy_density=tuple(density),
        resolution_hz=resolution,
        total_energy=total_energy,
    )


def _bin_support(
    index: int, frequencies: tuple[float, ...], resolution: float
) -> tuple[float, float]:
    frequency = frequencies[index]
    if index == 0:
        return 0.0, resolution / 2.0
    if index == len(frequencies) - 1:
        return frequency - resolution / 2.0, frequency
    return frequency - resolution / 2.0, frequency + resolution / 2.0


def coverage_weights(
    frequencies: tuple[float, ...],
    resolution_hz: float,
    low_hz: float,
    high_hz: float,
) -> tuple[float, ...]:
    weights: list[float] = []
    for index in range(len(frequencies)):
        support_low, support_high = _bin_support(index, frequencies, resolution_hz)
        overlap = min(high_hz, support_high) - max(low_hz, support_low)
        support_width = support_high - support_low
        weight = max(0.0, overlap) / support_width
        if weight < _WEIGHT_EPSILON:
            weight = 0.0
        elif 1.0 - weight < _WEIGHT_EPSILON:
            weight = 1.0
        weights.append(weight)
    return tuple(weights)


def integrate_interval(
    spectrum: SpectrumEstimate,
    low_hz: float,
    high_hz: float,
) -> tuple[float, tuple[float, ...]]:
    weights = coverage_weights(
        spectrum.frequencies, spectrum.resolution_hz, low_hz, high_hz
    )
    energy = spectrum.resolution_hz * sum(
        value * weight for value, weight in zip(spectrum.energy_density, weights)
    )
    return energy, weights


def _merge_intervals(bands: tuple[Band, ...]) -> list[tuple[float, float]]:
    merged: list[tuple[float, float]] = []
    for low, high in sorted((band.low_hz, band.high_hz) for band in bands):
        if not merged or low > merged[-1][1]:
            merged.append((low, high))
        else:
            merged[-1] = (merged[-1][0], max(merged[-1][1], high))
    return merged


def integrate_bands(
    samples: Iterable[float],
    sample_rate: float,
    rules: BandRuleSet,
) -> tuple[SpectrumEstimate, tuple[BandEnergy, ...], float, float, float]:
    spectrum = estimate_spectrum(samples, sample_rate)
    validate_band_rules(rules, spectrum.frequencies[-1])

    band_results: list[BandEnergy] = []
    for band in rules.bands:
        energy, weights = integrate_interval(spectrum, band.low_hz, band.high_hz)
        relative = energy / spectrum.total_energy if spectrum.total_energy != 0.0 else None
        band_results.append(
            BandEnergy(
                name=band.name,
                low_hz=band.low_hz,
                high_hz=band.high_hz,
                absolute_energy=energy,
                relative_energy=relative,
                coverage_weights=weights,
            )
        )

    gross_energy = sum(band.absolute_energy for band in band_results)
    union_energy = sum(
        integrate_interval(spectrum, low, high)[0]
        for low, high in _merge_intervals(rules.bands)
    )
    duplicate_energy = gross_energy - union_energy
    if abs(duplicate_energy) <= ENERGY_ABSOLUTE_TOLERANCE:
        duplicate_energy = 0.0
    return spectrum, tuple(band_results), gross_energy, union_energy, duplicate_energy
