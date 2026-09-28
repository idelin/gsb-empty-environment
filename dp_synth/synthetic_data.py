"""Synthetic-only data container and deterministic synthetic generator.

The engine refuses anything that is not a :class:`SyntheticData` instance, so no
real personal data can ever flow through the privacy mechanisms.
"""

from __future__ import annotations

import hashlib
import json
import random
from types import MappingProxyType
from typing import Any, Dict, List, Mapping, Sequence, Tuple

CITY_VALUES: Tuple[str, ...] = ("Beijing", "Shanghai", "Guangzhou", "Shenzhen", "Hangzhou")
SEGMENT_VALUES: Tuple[str, ...] = ("free", "basic", "pro", "enterprise")


def canonical_value(value: Any) -> Tuple[str, str]:
    """Stable, order-independent encoding of a record field value."""
    if isinstance(value, bool):
        return "bool", json.dumps(value, sort_keys=True, separators=(",", ":"))
    if isinstance(value, int):
        return "int", json.dumps(value, sort_keys=True, separators=(",", ":"))
    if isinstance(value, float):
        return "float", json.dumps(value, sort_keys=True, separators=(",", ":"))
    if isinstance(value, str):
        return "str", json.dumps(value, sort_keys=True, separators=(",", ":"))
    if isinstance(value, Mapping):
        encoded = {str(k): canonical_value(v) for k, v in value.items()}
        return "dict", json.dumps(encoded, sort_keys=True, separators=(",", ":"))
    if isinstance(value, Sequence) and not isinstance(value, (str, bytes)):
        encoded = sorted((canonical_value(v) for v in value), key=lambda p: p[0] + ":" + p[1])
        return "list", json.dumps(encoded, sort_keys=True, separators=(",", ":"))
    raise TypeError(f"unsupported field value type: {type(value).__name__}")


def canonical_fingerprint(records: Sequence[Mapping[str, Any]]) -> str:
    """SHA-256 over a key-order-independent encoding of every record."""
    payload = [
        {field: canonical_value(value) for field, value in sorted(record.items())}
        for record in records
    ]
    blob = json.dumps(payload, sort_keys=True, separators=(",", ":"), ensure_ascii=False)
    return hashlib.sha256(blob.encode("utf-8")).hexdigest()


def make_synthetic_employee_data(n: int = 2000, seed: int = 20260929) -> "SyntheticData":
    """Generate deterministic, fully artificial employee-like records.

    The data is drawn from seeded PRNGs over fixed vocabularies; it does not
    describe any real individual.
    """
    if n < 0:
        raise ValueError("n must be non-negative")
    rng = random.Random(seed)
    records: List[Dict[str, Any]] = []
    for idx in range(n):
        city = rng.choice(CITY_VALUES)
        segment = rng.choices(SEGMENT_VALUES, weights=(50, 30, 15, 5), k=1)[0]
        salary = round(rng.uniform(5_000.0, 60_000.0), 2)
        years = rng.randint(0, 25)
        active = rng.random() < 0.82
        records.append(
            {
                "id": f"synth-{idx:06d}",
                "city": city,
                "segment": segment,
                "salary": salary,
                "years": years,
                "active": active,
            }
        )
    return SyntheticData(records, label=f"synthetic:employees:n={n}:seed={seed}")


class SyntheticData:
    """Immutable bundle of synthetic records plus a version fingerprint."""

    __slots__ = ("_records", "_label", "_version")

    def __init__(
        self,
        records: Sequence[Mapping[str, Any]],
        label: str = "synthetic",
        version: str | None = None,
    ) -> None:
        frozen = tuple(MappingProxyType(dict(record)) for record in records)
        self._records = frozen
        self._label = str(label)
        self._version = version or canonical_fingerprint(frozen)

    @property
    def records(self) -> Tuple[Mapping[str, Any], ...]:
        return self._records

    @property
    def label(self) -> str:
        return self._label

    @property
    def version(self) -> str:
        return self._version

    def __len__(self) -> int:
        return len(self._records)

    def __repr__(self) -> str:
        short = self._version[:12]
        return f"SyntheticData(label={self._label!r}, n={len(self._records)}, version={short})"
