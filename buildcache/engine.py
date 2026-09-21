"""Action cache: single-flight builds, verified multi-output publishing."""

from __future__ import annotations

import threading
import uuid
from dataclasses import dataclass, field
from datetime import datetime, timezone
from typing import Protocol

from .manifest import BuildNode, Manifest
from .errors import NonDeterministicConflictError
from .store import ContentAddressableStore, StagingArea, sha256_hex


class Builder(Protocol):
    """Materializes a node's outputs into a fresh staging area."""

    def build(self, node: BuildNode, manifest: Manifest, staging: StagingArea) -> None:
        ...


@dataclass(slots=True)
class BuildResult:
    key: str
    attempt_id: str
    outputs: dict[str, str]  # relative path -> sha256
    output_digest: str
    status: str  # "published" | "cached" | "deduped"

    @property
    def cache_hit(self) -> bool:
        return self.status in ("cached", "deduped")


@dataclass(slots=True)
class _Flight:
    condition: threading.Condition
    done: bool = False
    error: BaseException | None = None
    attempt_id: str | None = None


class ActionCache:
    """Thread-safe multi-output action cache backed by a CAS."""

    def __init__(self, store: ContentAddressableStore) -> None:
        self.store = store
        self._meta_lock = threading.Lock()
        self._flights: dict[str, _Flight] = {}

    def read(self, key: str) -> dict | None:
        return self.store.read_entry(key)

    def _result_from_entry(self, key: str, entry: dict, status: str) -> BuildResult:
        return BuildResult(
            key=key,
            attempt_id=entry["attempt_id"],
            outputs=dict(entry["outputs"]),
            output_digest=entry["output_digest"],
            status=status,
        )

    def _declared_matches_entry(self, node: BuildNode, entry: dict) -> bool:
        published = entry.get("outputs", {})
        for path, digest in node.declared_digests.items():
            if published.get(path) != digest:
                return False
        return True

    def _build_and_publish(
        self, node: BuildNode, manifest: Manifest, builder: Builder, attempt_id: str
    ) -> BuildResult:
        staging = StagingArea(self.store, node.key)
        try:
            builder.build(node, manifest, staging)
            expected = {o.path: o.sha256 for o in node.outputs}
            staging.verify(expected)  # raises if any output fails
            # All outputs passed: compute their digests (nothing committed yet).
            digests: dict[str, str] = {}
            for output in node.outputs:
                data = (staging.dir / output.path).read_bytes()
                digests[output.path] = sha256_hex(data)
            output_digest = sha256_hex(
                "".join(f"{path}:{digest}\n" for path, digest in sorted(digests.items())).encode()
            )
            # A same-key entry may exist (rebuild / concurrent process). Compare
            # before touching CAS so losing content is never stored.
            existing = self.store.read_entry(node.key)
            if existing is not None:
                if existing.get("outputs") == dict(sorted(digests.items())):
                    return self._result_from_entry(node.key, existing, "deduped")
                raise NonDeterministicConflictError(
                    node.key,
                    existing=existing["output_digest"],
                    produced=output_digest,
                )
            package = manifest.package_for(node)
            entry = {
                "key": node.key,
                "package": package.name,
                "version": str(package.version),
                "attempt_id": attempt_id,
                "outputs": dict(sorted(digests.items())),
                "output_digest": output_digest,
                "built_at": datetime.now(timezone.utc).isoformat(),
            }
            # Commit the blobs only for the winning content, then the entry.
            for output in node.outputs:
                self.store.put_blob((staging.dir / output.path).read_bytes())
            winner = self.store.publish_entry(node.key, entry)
            status = "published" if winner.get("attempt_id") == attempt_id else "deduped"
            return self._result_from_entry(node.key, winner, status)
        finally:
            staging.cleanup()

    def request_build(
        self, node: BuildNode, manifest: Manifest, builder: Builder
    ) -> BuildResult:
        # 1) Fast path: an existing entry that already matches declared digests.
        entry = self.store.read_entry(node.key)
        if entry is not None and node.declared_digests and self._declared_matches_entry(node, entry):
            return self._result_from_entry(node.key, entry, "cached")

        # 2) Single-flight: concurrent identical builds share one attempt.
        while True:
            with self._meta_lock:
                flight = self._flights.get(node.key)
                if flight is None:
                    flight = _Flight(condition=threading.Condition())
                    self._flights[node.key] = flight
                    leader = True
                else:
                    leader = False

            if not leader:
                with flight.condition:
                    while not flight.done:
                        flight.condition.wait()
                published = self.store.read_entry(node.key)
                if published is not None:
                    # The leader produced it; identical duplicate request
                    # deduplicates onto the leader's single attempt.
                    return self._result_from_entry(node.key, published, "deduped")
                # Leader failed without publishing: loop and become leader.
                with self._meta_lock:
                    if self._flights.get(node.key) is flight:
                        del self._flights[node.key]
                continue

            attempt_id = uuid.uuid4().hex
            try:
                result = self._build_and_publish(node, manifest, builder, attempt_id)
            except BaseException as exc:
                with flight.condition:
                    flight.done = True
                    flight.error = exc
                    flight.condition.notify_all()
                with self._meta_lock:
                    if self._flights.get(node.key) is flight:
                        del self._flights[node.key]
                raise
            else:
                with flight.condition:
                    flight.done = True
                    flight.attempt_id = result.attempt_id
                    flight.condition.notify_all()
                with self._meta_lock:
                    if self._flights.get(node.key) is flight:
                        del self._flights[node.key]
                return result


class SimulatorBuilder:
    """Deterministic-by-default local builder driven by the manifest.

    ``behavior`` on a node selects failure modes for offline demos:
    - ``deterministic``   : render every output from its template
    - ``nondeterministic``: inject a fresh ``{BUILD_NONCE}`` per attempt
    - ``missing_output``  : silently skip one declared output
    - ``bad_digest``      : render normally so a wrong declared sha256 fails
    """

    def __init__(self) -> None:
        self._lock = threading.Lock()
        self.attempts: dict[str, int] = {}

    def _variables(self, node: BuildNode, manifest: Manifest) -> dict[str, str]:
        package = manifest.package_for(node)
        variables = {
            "KEY": node.key,
            "NAME": package.name,
            "VERSION": str(package.version),
        }
        variables.update(node.inputs)
        if node.behavior == "nondeterministic":
            with self._lock:
                count = self.attempts.get(node.key, 0) + 1
                self.attempts[node.key] = count
            variables["BUILD_NONCE"] = f"{count}-{uuid.uuid4().hex[:8]}"
        return variables

    def build(self, node: BuildNode, manifest: Manifest, staging: StagingArea) -> None:
        variables = self._variables(node, manifest)
        outputs = node.outputs
        if node.behavior == "missing_output":
            # Simulate a toolchain that fails to emit one declared output.
            outputs = tuple(node.outputs[:-1])
        for output in outputs:
            staging.write(output.path, output.render(variables))


class BuildEngine:
    """Convenience facade binding one manifest and builder to a cache."""

    def __init__(
        self,
        manifest: Manifest,
        store: ContentAddressableStore,
        builder: Builder | None = None,
    ) -> None:
        self.manifest = manifest
        self.cache = ActionCache(store)
        self.builder = builder or SimulatorBuilder()

    def build(self, key: str) -> BuildResult:
        node = self.manifest.nodes.get(key)
        if node is None:
            raise KeyError(f"unknown build key: {key!r}")
        return self.cache.request_build(node, self.manifest, self.builder)
