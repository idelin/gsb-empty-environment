"""Deterministic replay fixture: a small package universe.

The fixture is content-addressed at load time (digests computed from the
recorded bytes), so it stays a static, reviewable JSON document while still
exercising real SHA-256 verification.
"""
from __future__ import annotations

import base64
import hashlib
import json
import os

from .model import HOST_PLATFORM, Dep, Ident

TARGET = "linux-x86_64"

# name, version, platform, payload-text, runtime deps, build deps
_SPEC = [
    ("gcc", "1.0.0", HOST_PLATFORM, b"GNU C compiler host tool v1\n", [], []),
    ("gcc", "1.1.0", HOST_PLATFORM, b"GNU C compiler host tool v1.1\n", [], []),
    ("libc", "1.0.0", TARGET, b"minimal libc runtime\n", [], []),
    ("oldlib", "1.0.0", TARGET, b"legacy library removed later\n", [], []),
    ("newlib", "1.0.0", TARGET, b"shiny new library\n", [], []),
    ("app", "1.0.0", TARGET, b"root application version one\n",
     [("libc", "1.0.0"), ("oldlib", "1.0.0")], [("gcc", "1.0.0")]),
    ("app", "2.0.0", TARGET, b"root application version two\n",
     [("libc", "1.0.0"), ("newlib", "1.0.0")], [("gcc", "1.1.0")]),
]


def build_world():
    """Return ``{Ident: PackageSpec}`` with computed digests."""
    world = {}
    for name, version, platform, payload, rt, bd in _SPEC:
        digest = hashlib.sha256(payload).hexdigest()
        world[Ident(name, version, platform)] = {
            "payload": payload,
            "digest": digest,
            "length": len(payload),
            "runtime_deps": [Dep(n, v) for n, v in rt],
            "build_deps": [Dep(n, v) for n, v in bd],
        }
    return world


def export_json(path: str) -> None:
    docs = []
    for ident, spec in build_world().items():
        docs.append({
            "name": ident.name, "version": ident.version, "platform": ident.platform,
            "payload_b64": base64.b64encode(spec["payload"]).decode("ascii"),
            "runtime_deps": [[d.name, d.version] for d in spec["runtime_deps"]],
            "build_deps": [[d.name, d.version] for d in spec["build_deps"]],
        })
    with open(path, "w", encoding="utf-8") as fh:
        json.dump({"packages": docs}, fh, indent=2, sort_keys=True)
        fh.write("\n")


def load_json(path: str):
    with open(path, "rb") as fh:
        data = json.loads(fh.read())
    world = {}
    for row in data["packages"]:
        payload = base64.b64decode(row["payload_b64"])
        ident = Ident(row["name"], row["version"], row["platform"])
        world[ident] = {
            "payload": payload,
            "digest": hashlib.sha256(payload).hexdigest(),
            "length": len(payload),
            "runtime_deps": [Dep(n, v) for n, v in row["runtime_deps"]],
            "build_deps": [Dep(n, v) for n, v in row["build_deps"]],
        }
    return world


def fixture_path() -> str:
    return os.path.join(os.path.dirname(__file__), "..", "fixtures", "scenario.json")
