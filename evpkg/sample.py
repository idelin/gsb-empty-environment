"""Synthetic sample evidence package (deterministic, replayable)."""

from __future__ import annotations

import hashlib
from dataclasses import dataclass

from .hashing import canonical_bytes, sha256_hex
from .manifest import FileEntry, Manifest
from .model import Fact, Rule, Step, replay_digest

DEFAULT_CHUNK_SIZE = 65536


def synthetic_bytes(n: int, seed: bytes = b"synthetic-telemetry") -> bytes:
    """Deterministic pseudo-random bytes for synthetic large files."""
    out = bytearray()
    counter = 0
    while len(out) < n:
        out.extend(hashlib.sha256(seed + counter.to_bytes(8, "little")).digest())
        counter += 1
    return bytes(out[:n])


def _chunk(data: bytes, size: int) -> list:
    return [data[i : i + size] for i in range(0, len(data), size)]


@dataclass
class SamplePackage:
    manifest: Manifest
    chunks: dict  # file_id -> [chunk bytes, ...]


def build_sample_package(
    package_id: str = "PKG-SYNTH",
    version: int = 1,
    temperature: float = 72.5,
    telemetry_bytes: int = 0,
    chunk_size: int = DEFAULT_CHUNK_SIZE,
    reference_overrides: dict | None = None,
    replay_digest_override: str | None = None,
) -> SamplePackage:
    facts = [
        Fact("F-001", 1, {"temperature": temperature}),
        Fact("F-002", 1, {"humidity": 0.41}),
    ]
    rules = [
        Rule("R-001", 1, ("F-001",), "temperature > 70"),
        Rule("R-002", 1, ("F-002",), "humidity < 0.8"),
    ]
    steps = [Step("S-001", 1, ("R-001", "R-002"), "archive-evidence")]

    file_bytes = {
        "facts.json": canonical_bytes([f.to_dict() for f in facts]),
        "rules.json": canonical_bytes([r.to_dict() for r in rules]),
        "steps.json": canonical_bytes([s.to_dict() for s in steps]),
    }
    references = {
        "facts.json": (),
        "rules.json": ("facts.json",),
        "steps.json": ("facts.json", "rules.json"),
    }
    if telemetry_bytes:
        file_bytes["telemetry.bin"] = synthetic_bytes(telemetry_bytes)
        references["telemetry.bin"] = ("facts.json",)
    if reference_overrides:
        references.update({k: tuple(v) for k, v in reference_overrides.items()})

    chunks = {}
    entries = []
    for file_id in sorted(file_bytes):
        data = file_bytes[file_id]
        parts = _chunk(data, chunk_size)
        chunks[file_id] = parts
        entries.append(
            FileEntry(
                file_id=file_id,
                size=len(data),
                sha256=sha256_hex(data),
                chunk_size=chunk_size,
                chunk_digests=tuple(sha256_hex(p) for p in parts),
                references=references.get(file_id, ()),
            )
        )
    digest = replay_digest_override or replay_digest(facts, rules, steps)
    manifest = Manifest(package_id, version, digest, tuple(entries))
    return SamplePackage(manifest, chunks)
