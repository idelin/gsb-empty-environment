"""Load change streams from local JSON fixture files."""

from __future__ import annotations

import json
from pathlib import Path
from typing import Any

from .model import Change


def load_fixture(path: str | Path) -> tuple[dict[str, Any], list[Change]]:
    """Return ``(fixture, changes)`` from a local fixture document.

    Expected shape::

        {
          "join": {"left_key": "k", "right_key": "k",
                   "right_columns": ["rv"]},
          "changes": [
            {"txn_id": "T1", "commit_seq": 1, "table": "left",
             "pk": [1], "after": {"k": "A", "lv": 1}},
            ...
          ]
        }
    """
    data = json.loads(Path(path).read_text(encoding="utf-8"))
    join = data.get("join", {})
    changes = [Change.from_dict(raw) for raw in data.get("changes", [])]
    return join, changes
