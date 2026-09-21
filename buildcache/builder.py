"""Build runner: verifies every output digest before publishing."""

from __future__ import annotations

import hashlib
import shutil
import tempfile
import threading
from pathlib import Path

from .cache import CacheEntry, CacheStore
from .manifest import Node, output_content


class BuildError(Exception):
    pass


class SummaryMismatchError(BuildError):
    pass


def deterministic_build(node: Node, staging: str) -> None:
    for spec in node.outputs:
        target = Path(staging) / spec.path
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_bytes(output_content(node.id, node.inputs, spec.path))


class Builder:
    def __init__(self, store: CacheStore, workdir: str | Path, build_fn=deterministic_build):
        self.store = store
        self.workdir = Path(workdir)
        self.workdir.mkdir(parents=True, exist_ok=True)
        self.build_fn = build_fn
        self._lock = threading.Lock()
        self._inflight: dict[str, threading.Event] = {}

    def build(self, node: Node, force: bool = False) -> CacheEntry:
        if not force:
            existing = self.store.get(node.key)
            if existing is not None:
                return existing
        with self._lock:
            if not force:
                existing = self.store.get(node.key)
                if existing is not None:
                    return existing
            event = self._inflight.get(node.key)
            if event is None:
                event = threading.Event()
                self._inflight[node.key] = event
                leader = True
            else:
                leader = False
        if not leader:
            event.wait()
            entry = self.store.get(node.key)
            if entry is None:
                raise BuildError(f"concurrent build for {node.id} failed")
            return entry
        try:
            return self._build_and_publish(node)
        finally:
            with self._lock:
                self._inflight.pop(node.key, None)
                event.set()

    def _build_and_publish(self, node: Node) -> CacheEntry:
        staging = tempfile.mkdtemp(prefix=f"{node.id}-", dir=self.workdir)
        try:
            self.build_fn(node, staging)
            artifacts: dict[str, bytes] = {}
            outputs: dict[str, str] = {}
            for spec in node.outputs:
                produced = Path(staging) / spec.path
                if not produced.is_file():
                    raise SummaryMismatchError(
                        f"node {node.id}: missing output {spec.path}"
                    )
                data = produced.read_bytes()
                digest = hashlib.sha256(data).hexdigest()
                if digest != spec.sha256:
                    raise SummaryMismatchError(
                        f"node {node.id}: digest mismatch for {spec.path}: "
                        f"expected {spec.sha256[:12]}..., got {digest[:12]}..."
                    )
                artifacts[spec.path] = data
                outputs[spec.path] = digest
            combined = hashlib.sha256(
                "".join(f"{path}:{digest}\n" for path, digest in sorted(outputs.items())).encode("utf-8")
            ).hexdigest()
            entry = CacheEntry(key=node.key, digest=combined, outputs=outputs)
            return self.store.publish(entry, artifacts)
        finally:
            shutil.rmtree(staging, ignore_errors=True)
