from __future__ import annotations

import hashlib
import json
import os
import threading
from dataclasses import dataclass, field
from enum import Enum
from types import TracebackType
from typing import Any, Iterable, Mapping

from .graph import CycleDetected, DependencyGraph, NodeSpec
from .store import ArtifactStore
from .vfs import DirEntry, FileNotFound, VFS


def canonical_json(value: Any) -> bytes:
    return json.dumps(value, ensure_ascii=False, sort_keys=True, separators=(",", ":")).encode("utf-8")


class RestartRequired(Exception):
    pass


class TaskCanceled(Exception):
    pass


@dataclass(frozen=True)
class Dependency:
    kind: str
    logical_id: str
    version: str

    def to_json(self) -> dict[str, str]:
        return {"kind": self.kind, "id": self.logical_id, "version": self.version}

    @classmethod
    def from_json(cls, payload: Mapping[str, Any]) -> "Dependency":
        return cls(str(payload["kind"]), str(payload["id"]), str(payload["version"]))


@dataclass
class CacheEntry:
    node: str
    digest: str
    output_path: str
    dependencies: list[Dependency]
    cache_key: str
    graph_generation: int
    build_generation: int

    def to_json(self) -> dict[str, Any]:
        return {
            "node": self.node,
            "digest": self.digest,
            "output_path": self.output_path,
            "dependencies": [dependency.to_json() for dependency in self.dependencies],
            "cache_key": self.cache_key,
            "graph_generation": self.graph_generation,
            "build_generation": self.build_generation,
        }

    @classmethod
    def from_json(cls, payload: Mapping[str, Any]) -> "CacheEntry":
        return cls(
            str(payload["node"]),
            str(payload["digest"]),
            str(payload["output_path"]),
            [Dependency.from_json(item) for item in payload["dependencies"]],
            str(payload["cache_key"]),
            int(payload.get("graph_generation", 0)),
            int(payload.get("build_generation", 0)),
        )


class Output(bytes):
    """Explicit byte output from a task."""


class RequestStatus(str, Enum):
    WAITING = "waiting"
    COMPLETE = "complete"
    CANCELED = "canceled"
    FAILED = "failed"


class AttemptStatus(str, Enum):
    ACTIVE = "active"
    RESTART = "restart"
    CANCELED = "canceled"
    FAILED = "failed"


@dataclass
class Request:
    id: str
    roots: tuple[str, ...]
    status: RequestStatus = RequestStatus.WAITING
    error: Exception | None = None
    cycle: list[str] = field(default_factory=list)


class _Pause:
    def __init__(self) -> None:
        self._condition = threading.Condition()
        self.waiting = False
        self.name = ""
        self.pause_generation = 0
        self.resume_generation = 0
        self.canceled = False
        self.stale = False
        self.done = False
        self.error: BaseException | None = None

    def pause(self, name: str) -> None:
        with self._condition:
            self.pause_generation += 1
            self.name = name
            self.waiting = True
            self._condition.notify_all()
            while self.pause_generation > self.resume_generation and not self.done:
                self._condition.wait()
            canceled = self.canceled
            stale = self.stale
            self.waiting = False
            self._condition.notify_all()
        if canceled:
            raise TaskCanceled("request canceled")
        if stale:
            raise RestartRequired("filesystem generation changed")

    def resume(self, canceled: bool = False, stale: bool = False) -> None:
        with self._condition:
            self.canceled = canceled
            self.stale = stale
            self.resume_generation = self.pause_generation
            self._condition.notify_all()
            while self.waiting and self.resume_generation == self.pause_generation and not self.done:
                self._condition.wait()

    def wait_until_paused(self) -> None:
        with self._condition:
            while not self.waiting and not self.done and self.error is None:
                self._condition.wait()

    def finish(self) -> None:
        with self._condition:
            self.done = True
            self.resume_generation = self.pause_generation
            self._condition.notify_all()

@dataclass
class _Attempt:
    id: int
    node: str
    request_ids: set[str]
    generation: int
    graph_generation: int
    context: "TaskContext"
    thread: threading.Thread
    pause: _Pause
    status: AttemptStatus = AttemptStatus.ACTIVE
    error: BaseException | None = None
    cycle: list[str] = field(default_factory=list)
    restart_for_new_dependency: bool = False


class TaskContext:
    def __init__(self, node: str, vfs: VFS, kernel: "BuildKernel", pause: _Pause):
        self.node = node
        self.vfs = vfs
        self.kernel = kernel
        self.pause = pause
        self.reference_mode = False
        self.read_files: dict[str, Dependency] = {}
        self.missing_paths: dict[str, Dependency] = {}
        self.directories: dict[str, Dependency] = {}
        self.node_dependencies: set[str] = set()
        self.result: Any = None

    def logical_id(self, path: str | os.PathLike[str]) -> str:
        return self.kernel.logical_id(path)

    def read_file(self, path: str | os.PathLike[str], checkpoint: bool = False) -> bytes:
        logical_id = self.logical_id(path)
        if not self.vfs.exists(path) or self.vfs.is_dir(path):
            self.missing_paths[logical_id] = Dependency("missing", logical_id, "missing:0")
            self.kernel.record_node_dependency(self, logical_id)
            if checkpoint:
                self.maybe_pause("after-read")
            raise FileNotFound(str(path))
        version = self.kernel.file_version(path)
        self.read_files[logical_id] = Dependency("file", logical_id, version)
        self.kernel.record_node_dependency(self, logical_id)
        if checkpoint:
            self.maybe_pause("after-read")
        return self.vfs.read(path)

    def check_missing(
        self,
        path: str | os.PathLike[str],
        checkpoint: bool = False,
        checkpoint_name: str = "negative-check",
    ) -> bool:
        logical_id = self.logical_id(path)
        if self.vfs.exists(path):
            self.read_files[logical_id] = Dependency("file", logical_id, self.kernel.file_version(path))
            self.kernel.record_node_dependency(self, logical_id)
            exists = True
        else:
            self.missing_paths[logical_id] = Dependency("missing", logical_id, "missing:0")
            self.kernel.record_node_dependency(self, logical_id)
            exists = False
        if checkpoint:
            self.maybe_pause(checkpoint_name)
        return exists

    def list_dir(
        self,
        path: str | os.PathLike[str],
        sort_rule: str | None = None,
        checkpoint: bool = False,
    ) -> list[DirEntry]:
        rule = sort_rule or self.kernel.graph.nodes[self.node].sort_rule
        entries = self.vfs.scan_dir(path)
        ordered = self.kernel.sort_entries(entries, rule)
        version = self.kernel.directory_version(ordered, rule)
        self.directories[self.logical_id(path)] = Dependency("dir", self.logical_id(path), version)
        if checkpoint:
            self.maybe_pause("after-read")
        return ordered

    def checkpoint(self, name: str = "after-discover") -> None:
        self.maybe_pause(name)

    def maybe_pause(self, name: str) -> None:
        if not self.reference_mode:
            self.pause.pause(name)


class BuildKernel:
    def __init__(
        self,
        root: str | os.PathLike[str],
        specs: Mapping[str, NodeSpec] | list[NodeSpec],
        env: Mapping[str, str] | None = None,
        aliases: Mapping[str, str] | None = None,
        crash_points: Iterable[str] | None = None,
    ):
        self.vfs = VFS(root)
        self.store = ArtifactStore(self.vfs, set(crash_points or ()))
        self.env = dict(os.environ if env is None else env)
        self.aliases = {self.vfs.normalize(key): value for key, value in (aliases or {}).items()}
        self.reference_mode = False
        initial_graph = DependencyGraph.from_specs(specs)
        self.specs = initial_graph.nodes
        self.graph = self._load_graph(initial_graph)
        self.caches: dict[str, CacheEntry] = {}
        self.graph_generation = 0
        self.node_generation: dict[str, int] = {}
        self.requests: dict[str, Request] = {}
        self.active_by_node: dict[str, _Attempt] = {}
        self.next_request = 1
        self.next_attempt = 1
        self.started_nodes: set[str] = set()
        self.failed_nodes: dict[str, Exception] = {}
        self.seq = 0
        self._recover_state()

    @classmethod
    def reference(cls, root: str | os.PathLike[str], specs: Mapping[str, NodeSpec] | list[NodeSpec]) -> "BuildKernel":
        kernel = cls(root, specs, env={})
        kernel.reference_mode = True
        return kernel

    def _load_graph(self, initial: DependencyGraph) -> DependencyGraph:
        graph = initial
        for record in self.store.read_wal():
            if record.get("type") == "graph":
                graph = DependencyGraph.from_json(initial.nodes, record.get("graph", {}))
        return graph

    def close(self) -> None:
        for attempt in list(self.active_by_node.values()):
            attempt.pause.finish()
            attempt.thread.join(timeout=1)

    def __enter__(self) -> "BuildKernel":
        return self

    def __exit__(
        self,
        exc_type: type[BaseException] | None,
        exc: BaseException | None,
        traceback: TracebackType | None,
    ) -> None:
        self.close()

    def _next_seq(self) -> int:
        self.seq += 1
        return self.seq

    def _recover_state(self) -> None:
        records = self.store.read_wal()
        self.seq = max((int(record.get("seq", 0)) for record in records), default=0)
        graph_records = [record for record in records if record.get("type") == "graph"]
        if graph_records:
            self.graph_generation = int(graph_records[-1]["seq"])
        cache_records = [record for record in records if record.get("type") == "cache"]
        publish_records = [record for record in records if record.get("type") == "publish"]
        publish_by_node = {record["node"]: record for record in publish_records}
        recovered_records: list[dict[str, Any]] = []
        if graph_records:
            recovered_records.append(graph_records[-1])
        for record in cache_records:
            entry = CacheEntry.from_json(record["entry"])
            if not self.store.object_exists(entry.digest):
                continue
            if self.store.published_digest(entry.output_path) != entry.digest:
                continue
            if not self.dependencies_current(entry):
                continue
            self.caches[entry.node] = entry
            recovered_records.append(record)
            if publish_by_node.get(entry.node, {}).get("digest") == entry.digest:
                recovered_records.append(publish_by_node[entry.node])
        self.store.cleanup_temporary()
        request_records = [record for record in records if record.get("type") == "request"]
        for record in request_records:
            roots = tuple(record.get("roots", ()))
            try:
                closure = self.graph.closure(roots)
            except KeyError:
                continue
            if all(self.cache_valid(node) for node in closure):
                recovered_records.append(record)
                self.requests[str(record["id"])] = Request(
                    str(record["id"]),
                    roots,
                    RequestStatus.COMPLETE,
                )
        if recovered_records != records:
            self.store.rewrite_wal(recovered_records)
        for event in self.store.read_events():
            if event.get("event") == "start":
                self.started_nodes.add(str(event["node"]))

    def logical_id(self, path: str | os.PathLike[str]) -> str:
        normalized = self.vfs.normalize(path)
        if normalized in self.aliases:
            return self.aliases[normalized]
        for name, spec in self.graph.nodes.items():
            if normalized == spec.output_path():
                return f"node-output:{name}"
        return normalized

    def file_version(self, path: str | os.PathLike[str]) -> str:
        if not self.vfs.exists(path) or self.vfs.is_dir(path):
            return "missing:0"
        return "sha256:" + self.vfs.digest(path)

    def sort_entries(self, entries: list[DirEntry], rule: str) -> list[DirEntry]:
        descending = rule.endswith(":desc")
        base = rule[:-5] if descending else rule
        if base == "type":
            key = lambda entry: (entry.type, entry.name)
        elif base == "type:name":
            key = lambda entry: (entry.type, entry.name)
        else:
            key = lambda entry: (entry.name, entry.type)
        return sorted(entries, key=key, reverse=descending)

    def directory_version(self, entries: list[DirEntry], rule: str) -> str:
        payload = {
            "rule": rule,
            "entries": [[entry.name, entry.type] for entry in entries],
        }
        digest = hashlib.sha256(canonical_json(payload)).hexdigest()
        return f"dir:{rule}:{digest}"

    def dependencies_current(self, entry: CacheEntry) -> bool:
        for dependency in entry.dependencies:
            if self.current_dependency_version(dependency) != dependency.version:
                return False
        return True

    def current_dependency_version(self, dependency: Dependency) -> str:
        path = self.path_for_logical_id(dependency.logical_id)
        if path is None:
            return "missing:0"
        if dependency.kind in {"file", "missing"}:
            return self.file_version(path)
        if dependency.kind == "dir":
            try:
                entries = self.vfs.scan_dir(path)
            except FileNotFoundError:
                return "missing:0"
            parts = dependency.version.split(":", 2)
            rule = parts[1] if dependency.version.startswith("dir:") and len(parts) == 3 else "name"
            return self.directory_version(self.sort_entries(entries, rule), rule)
        return "unknown"

    def path_for_logical_id(self, logical_id: str) -> str | None:
        if logical_id.startswith("node-output:"):
            node = logical_id.split(":", 1)[1]
            if node not in self.graph.nodes:
                return None
            return self.graph.nodes[node].output_path()
        for alias, target in self.aliases.items():
            if target == logical_id:
                return alias
        return logical_id

    def record_node_dependency(self, context: TaskContext, logical_id: str) -> None:
        if logical_id.startswith("node-output:"):
            node = logical_id.split(":", 1)[1]
            if node != context.node and node in self.graph.nodes:
                context.node_dependencies.add(node)

    def cache_valid(self, node: str) -> bool:
        entry = self.caches.get(node)
        if entry is None:
            return False
        if self.store.published_digest(entry.output_path) != entry.digest:
            return False
        expected_key = self.cache_key(entry.node, entry.dependencies)
        if expected_key != entry.cache_key:
            return False
        return self.dependencies_current(entry)

    def dependency_signature(self, node: str) -> str:
        edges = sorted(self.graph.dependencies(node))
        return hashlib.sha256(canonical_json(edges)).hexdigest()

    def cache_key(self, node: str, dependencies: list[Dependency]) -> str:
        spec = self.graph.nodes[node]
        env = {name: self.env.get(name, "") for name in spec.env_whitelist}
        payload = {
            "content": [dependency.to_json() for dependency in sorted(dependencies, key=lambda item: (item.kind, item.logical_id))],
            "tool": spec.tool,
            "args": list(spec.args),
            "env": env,
            "node": node,
            "dependency_edges": self.dependency_signature(node),
        }
        return "sha256:" + hashlib.sha256(canonical_json(payload)).hexdigest()

    def request_build(self, request_id: str | None = None, roots: Iterable[str] = ()) -> str:
        root_names = tuple(sorted(set(roots)))
        for root in root_names:
            if root not in self.graph.nodes:
                raise KeyError(f"unknown node {root!r}")
        if request_id is None:
            request_id = f"r{self.next_request}"
            self.next_request += 1
        self.requests[request_id] = Request(request_id, root_names)
        return request_id

    def cancel_request(self, request_id: str) -> None:
        request = self.requests[request_id]
        if request.status != RequestStatus.WAITING:
            return
        request.status = RequestStatus.CANCELED
        for node, attempt in list(self.active_by_node.items()):
            if request_id not in attempt.request_ids:
                continue
            attempt.request_ids.discard(request_id)
            if not attempt.request_ids:
                attempt.pause.resume(canceled=True)
                attempt.thread.join(timeout=1)
                attempt.status = AttemptStatus.CANCELED
                self.active_by_node.pop(node, None)

    def active_request_ids(self, node: str) -> set[str]:
        if node in self.active_by_node:
            return set(self.active_by_node[node].request_ids)
        result = set()
        for request in self.requests.values():
            if request.status != RequestStatus.WAITING:
                continue
            if node in self.graph.closure(request.roots):
                result.add(request.id)
        return result

    def wait_until_paused(self, node: str, checkpoint: str | None = None) -> None:
        attempt = self.active_by_node[node]
        attempt.pause.wait_until_paused()
        if checkpoint is not None and attempt.pause.name != checkpoint:
            raise AssertionError(f"node {node!r} paused at {attempt.pause.name!r}, expected {checkpoint!r}")

    def checkpoint_name(self, node: str) -> str:
        return self.active_by_node[node].pause.name

    def run_until_idle(self, max_steps: int = 10000) -> None:
        for _ in range(max_steps):
            if not self.pump():
                return
        raise RuntimeError("scheduler did not become idle")

    def start_ready(self) -> bool:
        self._complete_cached_requests()
        self._reap_stale_attempts()
        self._start_ready_nodes()
        return bool(self.active_by_node)

    def finalize_finished(self) -> None:
        for node, attempt in list(self.active_by_node.items()):
            if not attempt.thread.is_alive():
                self._finalize_attempt(attempt)
        self._complete_cached_requests()

    def pump(self, timeout: float = 0.01) -> bool:
        self._complete_cached_requests()
        self._reap_stale_attempts()
        self._start_ready_nodes()
        chosen: _Attempt | None = None
        for node in sorted(self.active_by_node):
            attempt = self.active_by_node[node]
            if attempt.pause.waiting or not attempt.thread.is_alive():
                chosen = attempt
                break
        if chosen is None:
            if self.active_by_node:
                with next(iter(self.active_by_node.values())).pause._condition:
                    next(iter(self.active_by_node.values())).pause._condition.wait(timeout)
                self._complete_cached_requests()
                return True
            self._complete_cached_requests()
            return False
        if chosen.pause.waiting:
            chosen.pause.wait_until_paused()
            stale = self._attempt_is_stale(chosen)
            chosen.pause.resume(stale=stale)
        if not chosen.thread.is_alive():
            self._finalize_attempt(chosen)
        self._complete_cached_requests()
        return True

    def _attempt_is_stale(self, attempt: _Attempt) -> bool:
        return attempt.generation != self.node_generation.get(attempt.node, attempt.generation)

    def _reap_stale_attempts(self) -> None:
        for node, attempt in list(self.active_by_node.items()):
            if not self._attempt_is_stale(attempt):
                continue
            if attempt.pause.waiting:
                attempt.pause.resume(stale=True)
            attempt.thread.join(timeout=1)
            self.active_by_node.pop(node, None)

    def requested_nodes(self) -> set[str]:
        result: set[str] = set()
        for request in self.requests.values():
            if request.status == RequestStatus.WAITING:
                result.update(self.graph.closure(request.roots))
        return result

    def _ready_node_names(self) -> list[str]:
        requested = self.requested_nodes()
        ready: list[str] = []
        for node in sorted(requested):
            if self.cache_valid(node) or node in self.active_by_node:
                continue
            dependencies = self.graph.dependencies(node)
            if dependencies <= requested and all(self.cache_valid(dep) for dep in dependencies):
                ready.append(node)
        return ready

    def _start_ready_nodes(self) -> None:
        for node in self._ready_node_names():
            self._start_attempt(node)

    def _start_attempt(self, node: str) -> None:
        pause = _Pause()
        context = TaskContext(node, self.vfs, self, pause)
        context.reference_mode = self.reference_mode
        attempt = _Attempt(
            id=self.next_attempt,
            node=node,
            request_ids=self.active_request_ids(node),
            generation=self.node_generation.get(node, 0) + 1,
            graph_generation=self.graph_generation,
            context=context,
            thread=threading.Thread(target=self._run_task, args=(node, context), daemon=True),
            pause=pause,
        )
        self.node_generation[node] = self.node_generation.get(node, 0) + 1
        attempt.generation = self.node_generation[node]
        self.next_attempt += 1
        self.active_by_node[node] = attempt
        self.started_nodes.add(node)
        self.store.append_event({"event": "start", "node": node, "attempt": attempt.id})
        attempt.thread.start()

    def _run_task(self, node: str, context: TaskContext) -> None:
        try:
            context.result = self.graph.nodes[node].task(context)
        except BaseException as exc:
            context.pause.error = exc
        finally:
            context.pause.finish()

    def _finalize_attempt(self, attempt: _Attempt) -> None:
        attempt.thread.join(timeout=1)
        if attempt.thread.is_alive():
            return
        self.active_by_node.pop(attempt.node, None)
        error = attempt.pause.error
        if isinstance(error, TaskCanceled):
            attempt.status = AttemptStatus.CANCELED
            return
        if self._attempt_is_stale(attempt) or isinstance(error, RestartRequired):
            attempt.status = AttemptStatus.RESTART
            return
        if error is not None:
            attempt.status = AttemptStatus.FAILED
            attempt.error = error
            self.failed_nodes[attempt.node] = error
            self._fail_requests(attempt.node, error)
            return
        context = attempt.context
        edges = self.graph.static_dependencies(attempt.node) | set(context.node_dependencies)
        cycle = self.graph.candidate_cycle(attempt.node, edges)
        if cycle:
            attempt.status = AttemptStatus.FAILED
            attempt.cycle = cycle
            error = CycleDetected(cycle)
            self.failed_nodes[attempt.node] = error
            self._fail_requests(attempt.node, error, cycle)
            return
        dependencies = self._observed_dependencies(context)
        if not self.versions_match(dependencies):
            attempt.status = AttemptStatus.RESTART
            return
        old_edges = self.graph.dependencies(attempt.node)
        edges_changed = edges != old_edges
        if edges_changed:
            self._commit_graph(attempt.node, edges)
        newly_added = (edges - old_edges) - set(context.node_dependencies)
        if any(not self.cache_valid(dep) for dep in newly_added):
            attempt.status = AttemptStatus.RESTART
            attempt.restart_for_new_dependency = True
            return
        dependencies = self._observed_dependencies(context)
        if not self.versions_match(dependencies):
            attempt.status = AttemptStatus.RESTART
            return
        data = self.encode_output(context.result)
        staged = self.store.stage_output(data)
        digest = self.store.commit_object(staged)
        spec = self.graph.nodes[attempt.node]
        entry = CacheEntry(
            node=attempt.node,
            digest=digest,
            output_path=spec.output_path(),
            dependencies=dependencies,
            cache_key=self.cache_key(attempt.node, dependencies),
            graph_generation=self.graph_generation,
            build_generation=attempt.generation,
        )
        self._commit_cache(entry)
        self.caches[attempt.node] = entry
        self._commit_publish(entry)
        self.failed_nodes.pop(attempt.node, None)
        self._complete_requests_for_node(attempt.node)
        self.store.append_event({"event": "finish", "node": attempt.node, "attempt": attempt.id})

    def _observed_dependencies(self, context: TaskContext) -> list[Dependency]:
        combined = {
            dependency.logical_id: dependency
            for dependency in [
                *context.read_files.values(),
                *context.missing_paths.values(),
                *context.directories.values(),
            ]
        }
        return list(combined.values())

    def versions_match(self, dependencies: list[Dependency]) -> bool:
        return all(self.current_dependency_version(dep) == dep.version for dep in dependencies)

    def _commit_graph(self, node: str, edges: set[str]) -> None:
        self.store.crash("graph_commit_before")
        graph_payload = self.graph.to_json()
        dynamic_edges = edges - self.graph.static_dependencies(node)
        graph_payload["runtime_edges"][node] = sorted(dynamic_edges)
        self.graph_generation = self._next_seq()
        record = {
            "type": "graph",
            "seq": self.graph_generation,
            "node": node,
            "graph": graph_payload,
        }
        self.store.append_wal(record)
        self.graph.commit_dynamic_edges(node, edges - self.graph.static_dependencies(node))
        self.store.crash("graph_commit_after")

    def _commit_cache(self, entry: CacheEntry) -> None:
        self.store.crash("cache_commit_before")
        record = {"type": "cache", "seq": self._next_seq(), "entry": entry.to_json()}
        self.store.append_wal(record)
        self.store.crash("cache_commit_after")

    def _commit_publish(self, entry: CacheEntry) -> None:
        self.store.crash("publish_before")
        self.store.publish(entry.output_path, entry.digest)
        self.store.crash("publish_commit_before")
        record = {"type": "publish", "seq": self._next_seq(), "node": entry.node, "digest": entry.digest}
        self.store.append_wal(record)
        self.store.crash("publish_commit_after")

    def encode_output(self, value: Any) -> bytes:
        if isinstance(value, Output):
            return bytes(value)
        if isinstance(value, bytes):
            return value
        if isinstance(value, str):
            return value.encode("utf-8")
        return canonical_json(value)

    def _complete_requests_for_node(self, node: str) -> None:
        self._complete_cached_requests()
        for request in self.requests.values():
            if request.status != RequestStatus.COMPLETE:
                continue
            if not any(record.get("type") == "request" and record.get("id") == request.id for record in self.store.read_wal()):
                self.store.append_wal({
                    "type": "request",
                    "seq": self._next_seq(),
                    "id": request.id,
                    "roots": list(request.roots),
                })

    def _fail_requests(
        self,
        node: str,
        error: BaseException,
        cycle: list[str] | None = None,
    ) -> None:
        for request in self.requests.values():
            if request.status != RequestStatus.WAITING:
                continue
            if node in self.graph.closure(request.roots):
                request.status = RequestStatus.FAILED
                request.error = error
                if cycle is not None:
                    request.cycle = list(cycle)

    def _complete_cached_requests(self) -> None:
        for request in self.requests.values():
            if request.status != RequestStatus.WAITING:
                continue
            closure = self.graph.closure(request.roots)
            failed_node = next((node for node in closure if node in self.failed_nodes), None)
            if failed_node is not None:
                error = self.failed_nodes[failed_node]
                request.status = RequestStatus.FAILED
                request.error = error
                if isinstance(error, CycleDetected):
                    request.cycle = list(error.cycle)
                continue
            if all(self.cache_valid(node) for node in closure):
                request.status = RequestStatus.COMPLETE
                continue
