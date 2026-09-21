"""Evidence package (de)serialization.

The package is deterministic JSON: key order is fixed and no wall-clock
data is ever written, so an evidence package is itself reproducible.
"""

from __future__ import annotations

import json

from .model import EVIDENCE_FORMAT


def save_evidence(evidence: dict, path) -> None:
    with open(path, "w", encoding="utf-8") as handle:
        json.dump(evidence, handle, indent=2, sort_keys=True)
        handle.write("\n")


def load_evidence(path) -> dict:
    with open(path, "r", encoding="utf-8") as handle:
        data = json.load(handle)
    if not isinstance(data, dict) or data.get("format") != EVIDENCE_FORMAT:
        raise ValueError(f"not an {EVIDENCE_FORMAT} evidence package")
    return data
