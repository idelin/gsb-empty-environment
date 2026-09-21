"""Build node specifications, loaded from local JSON only."""
from __future__ import annotations

from dataclasses import dataclass

from .version import Version


@dataclass(frozen=True)
class NodeSpec:
    """A buildable unit: one package at one version with required artifacts."""

    package: str
    version: Version
    required_artifacts: tuple[str, ...]

    @property
    def node_id(self) -> str:
        return f"{self.package}@{self.version}"
