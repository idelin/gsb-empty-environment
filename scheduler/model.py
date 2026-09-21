"""Problem model and local JSON input loading/validation."""
from __future__ import annotations

from dataclasses import dataclass

from .semver import Version


class ProblemError(ValueError):
    """Raised when the input JSON is invalid (bad schema, cycle, ...)."""


@dataclass(frozen=True)
class Task:
    id: str
    cpu: int
    memory: int
    duration: int
    deps: tuple[str, ...] = ()


@dataclass(frozen=True)
class Package:
    name: str
    version: Version


@dataclass(frozen=True)
class Problem:
    version: Version
    cpu_capacity: int
    memory_capacity: int
    tasks: tuple[Task, ...]
    packages: tuple[Package, ...] = ()

    def task_map(self) -> dict[str, Task]:
        return {task.id: task for task in self.tasks}


def _require_int(value: object, what: str) -> int:
    if isinstance(value, bool) or not isinstance(value, int):
        raise ProblemError(f"{what} must be an integer, got {value!r}")
    return value


def _require_name(value: object, what: str) -> str:
    if not isinstance(value, str) or not value:
        raise ProblemError(f"{what} must be a non-empty string, got {value!r}")
    return value


def _load_packages(data: dict) -> tuple[Package, ...]:
    raw = data.get("packages", [])
    if not isinstance(raw, list):
        raise ProblemError("packages must be a list")
    packages = []
    for index, entry in enumerate(raw):
        if not isinstance(entry, dict):
            raise ProblemError(f"packages[{index}] must be an object")
        name = _require_name(entry.get("name"), f"packages[{index}].name")
        try:
            version = Version.parse(entry.get("version"))
        except ValueError as exc:
            raise ProblemError(f"packages[{index}] ({name}): {exc}") from exc
        packages.append(Package(name, version))
    return packages


def _check_acyclic(tasks: tuple[Task, ...]) -> None:
    indegree = {task.id: len(task.deps) for task in tasks}
    successors: dict[str, list[str]] = {task.id: [] for task in tasks}
    for task in tasks:
        for dep in task.deps:
            successors[dep].append(task.id)
    queue = sorted(tid for tid, deg in indegree.items() if deg == 0)
    visited = 0
    while queue:
        tid = queue.pop()
        visited += 1
        for succ in successors[tid]:
            indegree[succ] -= 1
            if indegree[succ] == 0:
                queue.append(succ)
    if visited != len(tasks):
        remaining = sorted(tid for tid, deg in indegree.items() if deg > 0)
        raise ProblemError(f"dependency cycle detected involving: {remaining}")


def load_problem(data: object) -> Problem:
    """Validate a decoded JSON document and build a Problem."""
    if not isinstance(data, dict):
        raise ProblemError("top-level JSON value must be an object")
    try:
        version = Version.parse(data.get("version"))
    except ValueError as exc:
        raise ProblemError(f"version: {exc}") from exc

    capacity = data.get("capacity")
    if not isinstance(capacity, dict):
        raise ProblemError("capacity must be an object with cpu and memory")
    cpu_cap = _require_int(capacity.get("cpu"), "capacity.cpu")
    mem_cap = _require_int(capacity.get("memory"), "capacity.memory")
    if cpu_cap < 1 or mem_cap < 1:
        raise ProblemError("capacity cpu and memory must be >= 1")

    raw_tasks = data.get("tasks")
    if not isinstance(raw_tasks, list) or not raw_tasks:
        raise ProblemError("tasks must be a non-empty list")
    tasks: list[Task] = []
    seen: set[str] = set()
    for index, raw in enumerate(raw_tasks):
        if not isinstance(raw, dict):
            raise ProblemError(f"tasks[{index}] must be an object")
        tid = _require_name(raw.get("id"), f"tasks[{index}].id")
        if tid in seen:
            raise ProblemError(f"duplicate task id {tid!r}")
        seen.add(tid)
        cpu = _require_int(raw.get("cpu"), f"task {tid!r} cpu")
        memory = _require_int(raw.get("memory"), f"task {tid!r} memory")
        duration = _require_int(raw.get("duration"), f"task {tid!r} duration")
        if cpu < 1 or memory < 1 or duration < 1:
            raise ProblemError(
                f"task {tid!r}: cpu, memory and duration must be >= 1"
            )
        raw_deps = raw.get("deps", [])
        if not isinstance(raw_deps, list):
            raise ProblemError(f"task {tid!r} deps must be a list")
        deps: list[str] = []
        for dep in raw_deps:
            dep = _require_name(dep, f"task {tid!r} dep")
            if dep == tid:
                raise ProblemError(f"task {tid!r} depends on itself")
            if dep in deps:
                raise ProblemError(f"task {tid!r} lists dep {dep!r} twice")
            deps.append(dep)
        tasks.append(Task(tid, cpu, memory, duration, tuple(deps)))

    known = {task.id for task in tasks}
    for task in tasks:
        for dep in task.deps:
            if dep not in known:
                raise ProblemError(
                    f"task {task.id!r} depends on unknown task {dep!r}"
                )
    _check_acyclic(tuple(tasks))

    return Problem(
        version=version,
        cpu_capacity=cpu_cap,
        memory_capacity=mem_cap,
        tasks=tuple(tasks),
        packages=_load_packages(data),
    )
