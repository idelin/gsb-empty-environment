"""Load node specs and events from local JSON files.  No network access."""
from __future__ import annotations

import json
from pathlib import Path
from typing import Any

from .events import Event, EventError
from .nodes import NodeSpec
from .version import Version, VersionError


class LoadError(ValueError):
    """Raised when an input JSON file is malformed."""


def _read_json(path: str | Path) -> Any:
    try:
        text = Path(path).read_text(encoding="utf-8")
    except OSError as exc:
        raise LoadError(f"cannot read {path}: {exc}") from exc
    try:
        return json.loads(text)
    except json.JSONDecodeError as exc:
        raise LoadError(f"{path}: invalid JSON: {exc}") from exc


def load_packages(path: str | Path) -> list[NodeSpec]:
    """Load node specs: {"nodes": [{package, version, required_artifacts}]}"""
    data = _read_json(path)
    nodes = data.get("nodes") if isinstance(data, dict) else None
    if not isinstance(nodes, list):
        raise LoadError(f"{path}: expected an object with a 'nodes' array")

    specs: list[NodeSpec] = []
    seen: set[str] = set()
    for index, raw in enumerate(nodes):
        where = f"{path}: nodes[{index}]"
        if not isinstance(raw, dict):
            raise LoadError(f"{where}: expected an object")
        package = raw.get("package")
        if not isinstance(package, str) or not package:
            raise LoadError(f"{where}: 'package' must be a non-empty string")
        try:
            version = Version.parse(raw.get("version"))
        except VersionError as exc:
            raise LoadError(f"{where}: {exc}") from exc
        artifacts = raw.get("required_artifacts")
        if (
            not isinstance(artifacts, list)
            or not artifacts
            or not all(isinstance(a, str) and a for a in artifacts)
        ):
            raise LoadError(
                f"{where}: 'required_artifacts' must be a non-empty array of strings"
            )
        spec = NodeSpec(
            package=package,
            version=version,
            required_artifacts=tuple(dict.fromkeys(artifacts)),
        )
        if spec.node_id in seen:
            raise LoadError(f"{where}: duplicate node {spec.node_id!r}")
        seen.add(spec.node_id)
        specs.append(spec)
    return specs


def load_events(path: str | Path) -> list[Event]:
    """Load events: {"events": [{event_id, type, node, attempt, payload?}]}"""
    data = _read_json(path)
    events = data.get("events") if isinstance(data, dict) else None
    if not isinstance(events, list):
        raise LoadError(f"{path}: expected an object with an 'events' array")

    parsed: list[Event] = []
    for index, raw in enumerate(events):
        try:
            parsed.append(Event.from_dict(raw))
        except EventError as exc:
            raise LoadError(f"{path}: events[{index}]: {exc}") from exc
    return parsed
