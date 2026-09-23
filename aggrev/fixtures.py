"""Load change events from local fixture files (no message platform)."""
from __future__ import annotations

import json
from pathlib import Path

from .models import ChangeEvent


def load_events(path: str | Path) -> list[ChangeEvent]:
    with open(path, "r", encoding="utf-8") as fh:
        raw = json.load(fh)
    return [ChangeEvent.from_dict(item) for item in raw]
