"""Artifact cache layout: host tools and target artifacts are cached in
separate trees, so two targets on one host share the same host tools."""

from __future__ import annotations

import json
from dataclasses import dataclass
from pathlib import Path

from .model import BuildInput
from .resolver import Resolution


@dataclass
class CacheEntry:
    domain: str  # "host" or "target"
    name: str
    version: str
    path: Path
    status: str  # "created" or "cached"

    def to_json(self) -> dict:
        return {
            "domain": self.domain,
            "name": self.name,
            "version": self.version,
            "path": str(self.path),
            "status": self.status,
        }


class Cache:
    """Directory layout: <root>/<platform-key>/<name>/<version>/manifest.json

    platform-key is the host key (e.g. ``host-macos-arm64``) for host-domain
    packages and the target key (e.g. ``target-linux-x86_64``) for
    target-domain packages.
    """

    def __init__(self, root):
        self.root = Path(root)

    def entry_dir(self, platform_key: str, name: str, version) -> Path:
        return self.root / platform_key / name / str(version)

    def materialize(self, resolution: Resolution, build_input: BuildInput) -> list:
        """Create cache entries for every selected package. Idempotent."""
        keys = {"host": build_input.host_key, "target": build_input.target_key}
        entries = []
        for domain, name in resolution.build_order:
            version = resolution.selected[(domain, name)]
            path = self.entry_dir(keys[domain], name, version)
            status = "cached" if path.exists() else "created"
            path.mkdir(parents=True, exist_ok=True)
            manifest = {
                "name": name,
                "version": str(version),
                "domain": domain,
                "platform": keys[domain],
                "stage": resolution.context.stage,
            }
            (path / "manifest.json").write_text(
                json.dumps(manifest, indent=2) + "\n", encoding="utf-8"
            )
            entries.append(CacheEntry(domain, name, str(version), path, status))
        return entries
