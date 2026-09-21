"""Core data model: package identity, metadata, canonical serialization.

Target packages live on a concrete target platform (e.g. ``linux-x86_64``);
host build tools always live on :data:`HOST_PLATFORM`.
"""
from __future__ import annotations

import hashlib
import json
from dataclasses import dataclass
from typing import Iterable
from urllib.parse import quote, unquote

HOST_PLATFORM = "host"


@dataclass(frozen=True, order=True)
class Ident:
    """A package identity: name + exact version + target platform."""

    name: str
    version: str
    platform: str

    @property
    def key(self) -> str:
        return f"{quote(self.name, safe='')}=={quote(self.version, safe='')}@{quote(self.platform, safe='')}"

    @classmethod
    def from_key(cls, key: str) -> "Ident":
        if key.count("@") != 1 or key.count("==") != 1:
            raise ValueError(f"bad identity key: {key!r}")
        n_v, platform = key.rsplit("@", 1)
        name, version = n_v.split("==", 1)
        return cls(unquote(name), unquote(version), unquote(platform))


@dataclass(frozen=True, order=True)
class Dep:
    name: str
    version: str


@dataclass(frozen=True)
class Metadata:
    ident: Ident
    archive_digest: str
    archive_length: int
    runtime_deps: tuple[Dep, ...] = ()
    build_deps: tuple[Dep, ...] = ()

    def to_dict(self) -> dict:
        return {
            "name": self.ident.name,
            "version": self.ident.version,
            "platform": self.ident.platform,
            "archive_digest": self.archive_digest,
            "archive_length": self.archive_length,
            "runtime_deps": [[d.name, d.version] for d in sorted(self.runtime_deps)],
            "build_deps": [[d.name, d.version] for d in sorted(self.build_deps)],
        }

    @classmethod
    def from_dict(cls, data: dict) -> "Metadata":
        return cls(
            ident=Ident(data["name"], data["version"], data["platform"]),
            archive_digest=data["archive_digest"],
            archive_length=int(data["archive_length"]),
            runtime_deps=tuple(Dep(n, v) for n, v in data.get("runtime_deps", [])),
            build_deps=tuple(Dep(n, v) for n, v in data.get("build_deps", [])),
        )


def canonical_json(obj) -> bytes:
    """Deterministic JSON encoding used for manifests, metadata and journals."""
    return json.dumps(obj, sort_keys=True, separators=(",", ":")).encode("utf-8")


def digest_metadata(meta: Metadata) -> str:
    return hashlib.sha256(canonical_json(meta.to_dict())).hexdigest()


def deps_from_pairs(pairs: Iterable) -> tuple[Dep, ...]:
    return tuple(Dep(n, v) for n, v in pairs)
