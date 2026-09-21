"""Shared builders for tests (not itself a test module)."""

from buildstate.events import Artifact, Event
from buildstate.model import (
    BuildDefinition,
    InputRef,
    NodeSpec,
    Package,
)
from buildstate.version import Version


def make_definition(nodes=None, packages=None):
    packages = packages or (Package("lib", Version.parse("1.2.3")),)
    package_map = {p.name: p for p in packages}
    node_specs = []
    for name, required in (nodes if nodes is not None else {"build": ("a.bin",)}).items():
        node_specs.append(
            NodeSpec(
                name=name,
                inputs=tuple(
                    InputRef(package=p.name, version=p.version)
                    for p in packages
                    if p.name == "lib"
                ),
                required_artifacts=tuple(required),
            )
        )
    return BuildDefinition(packages=tuple(packages), nodes=tuple(node_specs))


def event(
    event_id,
    node="build",
    attempt=1,
    kind="started",
    timestamp="2026-09-21T00:00:00Z",
    artifacts=(),
    reason="",
):
    return Event(
        event_id=event_id,
        node=node,
        attempt=attempt,
        kind=kind,
        timestamp=timestamp,
        artifacts=tuple(
            art if isinstance(art, Artifact) else Artifact(art[0], art[1])
            for art in artifacts
        ),
        reason=reason,
    )
