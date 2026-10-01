"""Stopping-evidence persistence.

Every estimation run produces an evidence record: the pre-declared
parameters, every staged checkpoint (sample size, estimate, radius,
per-stage error budget, decision), the final status (MET / NOT_MET), the
seed and sample counts, and whether each conclusion is ``exact`` or
``empirical``.  Records are JSON-serialisable and are meant to be kept,
in particular when the budget is exhausted before the error target is
reached -- the run must then be reported as NOT met, never as converged.
"""
from __future__ import annotations

import json
from dataclasses import asdict, is_dataclass
from datetime import datetime, timezone
from pathlib import Path

__all__ = ["write_evidence", "EXACT", "EMPIRICAL", "MET", "NOT_MET"]

EXACT = "exact"
EMPIRICAL = "empirical"
MET = "MET"
NOT_MET = "NOT_MET"


def _to_plain(obj):
    if is_dataclass(obj):
        return {k: _to_plain(v) for k, v in asdict(obj).items()}
    if isinstance(obj, dict):
        return {k: _to_plain(v) for k, v in obj.items()}
    if isinstance(obj, (list, tuple)):
        return [_to_plain(v) for v in obj]
    return obj


def write_evidence(record, path) -> Path:
    """Write an evidence record (dataclass or dict) to ``path`` as JSON."""
    path = Path(path)
    path.parent.mkdir(parents=True, exist_ok=True)
    payload = {
        "recorded_at_utc": datetime.now(timezone.utc).isoformat(),
        "evidence": _to_plain(record),
    }
    path.write_text(json.dumps(payload, indent=2, sort_keys=True) + "\n")
    return path
