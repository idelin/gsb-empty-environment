"""Local JSON inputs: build declarations and the event log."""

from __future__ import annotations

import json
from dataclasses import dataclass, field
from pathlib import Path

from .registry import PackageRegistry
from .versions import Version, parse_version


class InputError(ValueError):
    """Malformed build input or event data."""


@dataclass(frozen=True)
class NodeSpec:
    node: str
    package: str
    version: Version
    required_artifacts: tuple[str, ...]
    require_commit: bool = True
    include_prerelease: bool = False


@dataclass(frozen=True)
class BuildInput:
    nodes: dict[str, NodeSpec]

    def required_artifacts(self, node: str) -> tuple[str, ...]:
        return self.nodes[node].required_artifacts


@dataclass(frozen=True)
class Artifact:
    name: str
    package: str
    version: Version
    sha256: str


@dataclass(frozen=True)
class Event:
    event_id: str
    seq: int
    attempt: int
    node: str
    kind: str  # submitted | artifact_written | committed | succeeded | failed
    timestamp: str
    package: str | None = None
    version: Version | None = None
    artifacts: tuple[Artifact, ...] = field(default_factory=tuple)
    commit: str | None = None
    reason: str | None = None


_KINDS = {"submitted", "artifact_written", "committed", "succeeded", "failed"}


def _expect_object(data: object, label: str) -> dict:
    if not isinstance(data, dict):
        raise InputError(f"{label} must be a JSON object")
    return data


def load_build_input(path: str | Path, registry: PackageRegistry) -> BuildInput:
    with open(path, "r", encoding="utf-8") as handle:
        data = json.load(handle)
    root = _expect_object(data, "build input")
    raw_nodes = root.get("nodes")
    if not isinstance(raw_nodes, list) or not raw_nodes:
        raise InputError("build input must contain a non-empty 'nodes' list")

    nodes: dict[str, NodeSpec] = {}
    for entry in raw_nodes:
        entry = _expect_object(entry, "node entry")
        node = entry.get("node")
        package = entry.get("package")
        version_spec = entry.get("version")
        if not isinstance(node, str) or not node:
            raise InputError(f"node entry missing non-empty 'node': {entry!r}")
        if node in nodes:
            raise InputError(f"duplicate node {node!r}")
        if not isinstance(package, str) or not package:
            raise InputError(f"node {node!r} missing non-empty 'package'")
        if not isinstance(version_spec, str) or not version_spec:
            raise InputError(f"node {node!r} missing non-empty 'version'")
        include_pre = bool(entry.get("include_prerelease", False))
        version = registry.resolve(package, version_spec, allow_prerelease=include_pre)

        required = entry.get("required_artifacts")
        if not isinstance(required, list) or not required:
            raise InputError(f"node {node!r} needs a non-empty 'required_artifacts' list")
        names: list[str] = []
        for name in required:
            if not isinstance(name, str) or not name:
                raise InputError(f"node {node!r} has an invalid artifact name: {name!r}")
            if name in names:
                raise InputError(f"node {node!r} lists artifact {name!r} twice")
            names.append(name)
        require_commit = bool(entry.get("require_commit", True))
        nodes[node] = NodeSpec(
            node=node,
            package=package,
            version=version,
            required_artifacts=tuple(names),
            require_commit=require_commit,
            include_prerelease=include_pre,
        )
    return BuildInput(nodes=nodes)


def _parse_artifact(raw: object, context: str) -> Artifact:
    raw = _expect_object(raw, context)
    name = raw.get("name")
    package = raw.get("package")
    version_text = raw.get("version")
    sha = raw.get("sha256")
    if not all(isinstance(v, str) and v for v in (name, package, version_text, sha)):
        raise InputError(f"{context} needs non-empty name/package/version/sha256 strings")
    return Artifact(
        name=name,
        package=package,
        version=parse_version(version_text),
        sha256=sha,
    )


def load_events(path: str | Path) -> list[Event]:
    with open(path, "r", encoding="utf-8") as handle:
        data = json.load(handle)
    root = _expect_object(data, "event log")
    raw_events = root.get("events")
    if not isinstance(raw_events, list):
        raise InputError("event log must contain an 'events' list")

    events: list[Event] = []
    for raw in raw_events:
        raw = _expect_object(raw, "event")
        event_id = raw.get("event_id")
        seq = raw.get("seq")
        attempt = raw.get("attempt")
        node = raw.get("node")
        kind = raw.get("kind")
        timestamp = raw.get("timestamp", "")
        if not isinstance(event_id, str) or not event_id:
            raise InputError(f"event missing 'event_id': {raw!r}")
        if not isinstance(seq, int) or isinstance(seq, bool) or seq < 0:
            raise InputError(f"event {event_id!r}: 'seq' must be a non-negative integer")
        if not isinstance(attempt, int) or isinstance(attempt, bool) or attempt < 1:
            raise InputError(f"event {event_id!r}: 'attempt' must be a positive integer")
        if not isinstance(node, str) or not node:
            raise InputError(f"event {event_id!r}: missing 'node'")
        if kind not in _KINDS:
            raise InputError(f"event {event_id!r}: unknown kind {kind!r}")
        if not isinstance(timestamp, str):
            raise InputError(f"event {event_id!r}: 'timestamp' must be a string")

        package = raw.get("package")
        version = parse_version(raw["version"]) if raw.get("version") is not None else None
        artifacts: tuple[Artifact, ...] = ()
        if "artifacts" in raw and raw["artifacts"] is not None:
            raw_artifacts = raw["artifacts"]
            if not isinstance(raw_artifacts, list):
                raise InputError(f"event {event_id!r}: 'artifacts' must be a list")
            artifacts = tuple(
                _parse_artifact(item, f"artifact in event {event_id!r}")
                for item in raw_artifacts
            )
        commit = raw.get("commit")
        reason = raw.get("reason")
        if commit is not None and (not isinstance(commit, str) or not commit):
            raise InputError(f"event {event_id!r}: 'commit' must be a non-empty string")
        if reason is not None and not isinstance(reason, str):
            raise InputError(f"event {event_id!r}: 'reason' must be a string")
        events.append(
            Event(
                event_id=event_id,
                seq=seq,
                attempt=attempt,
                node=node,
                kind=kind,
                timestamp=timestamp,
                package=package if isinstance(package, str) else None,
                version=version,
                artifacts=artifacts,
                commit=commit,
                reason=reason,
            )
        )
    return events
