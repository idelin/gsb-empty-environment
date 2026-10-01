"""JSON loading and saving for scenarios (synthetic int/rational data)."""
from __future__ import annotations

import json
from pathlib import Path
from typing import Union

from .model import Scenario, parse_scenario


def load_scenario(source: Union[str, Path]) -> Scenario:
    """Load a scenario from a JSON file path."""
    path = Path(source)
    with path.open("r", encoding="utf-8") as handle:
        data = json.load(handle)
    return parse_scenario(data)


def scenario_to_jsonable(scenario: Scenario) -> dict:
    """Render a scenario back to a JSON-compatible dict (exact strings)."""
    from .model import fmt_number

    return {
        "now": fmt_number(scenario.now),
        "tasks": [
            {
                "id": t.id,
                "machine": t.machine,
                "duration": fmt_number(t.duration),
                "start": fmt_number(t.start),
                "executed": t.executed,
            }
            for t in scenario.tasks
        ],
        "new_tasks": [
            {
                "id": t.id,
                "machine": t.machine,
                "duration": fmt_number(t.duration),
            }
            for t in scenario.new_tasks
        ],
        "failures": [
            {
                "machine": f.machine,
                "start": fmt_number(f.start),
                "end": fmt_number(f.end),
            }
            for f in scenario.failures
        ],
        "budget": {
            "subset": scenario.subset_budget,
            "placement": scenario.placement_budget,
        },
    }
