"""从本地 JSON 读取构建输入并校验，不访问网络。"""

from __future__ import annotations

import json
from pathlib import Path

from .errors import InputError
from .models import BuildInput, Capacity, Package, Task
from .semver import is_valid_version


def load_build_input(path: str | Path) -> BuildInput:
    try:
        with open(path, encoding="utf-8") as fh:
            data = json.load(fh)
    except FileNotFoundError as exc:
        raise InputError(f"输入文件不存在: {path}") from exc
    except json.JSONDecodeError as exc:
        raise InputError(f"JSON 解析失败: {exc}") from exc
    return parse_build_input(data)


def parse_build_input(data) -> BuildInput:
    if not isinstance(data, dict):
        raise InputError("顶层结构必须是 JSON 对象")

    name = data.get("name", "build")
    if not isinstance(name, str) or not name:
        raise InputError("name 必须是非空字符串")

    capacity = _parse_capacity(data.get("capacity"))
    packages = _parse_packages(data.get("packages", []))
    tasks = _parse_tasks(data.get("tasks"))
    _check_dag(tasks)
    return BuildInput(name=name, capacity=capacity, packages=packages, tasks=tasks)


def _require_int(value, field: str, minimum: int) -> int:
    if isinstance(value, bool) or not isinstance(value, int):
        raise InputError(f"{field} 必须是整数，得到 {value!r}")
    if value < minimum:
        raise InputError(f"{field} 必须 >= {minimum}，得到 {value}")
    return value


def _parse_capacity(raw) -> Capacity:
    if not isinstance(raw, dict):
        raise InputError("缺少 capacity 对象（需包含 cpu 与 memory）")
    cpu = _require_int(raw.get("cpu"), "capacity.cpu", 1)
    memory = _require_int(raw.get("memory"), "capacity.memory", 1)
    return Capacity(cpu=cpu, memory=memory)


def _parse_packages(raw) -> tuple[Package, ...]:
    if not isinstance(raw, list):
        raise InputError("packages 必须是数组")
    packages = []
    seen = set()
    for i, item in enumerate(raw):
        if not isinstance(item, dict):
            raise InputError(f"packages[{i}] 必须是对象")
        pkg_name = item.get("name")
        version = item.get("version")
        if not isinstance(pkg_name, str) or not pkg_name:
            raise InputError(f"packages[{i}].name 必须是非空字符串")
        if pkg_name in seen:
            raise InputError(f"包重复声明: {pkg_name}")
        if not is_valid_version(version):
            raise InputError(
                f"包 {pkg_name} 的版本号非法: {version!r}"
                "（需为 major.minor.patch，可带 -预发布）"
            )
        seen.add(pkg_name)
        packages.append(Package(name=pkg_name, version=version))
    return tuple(packages)


def _parse_tasks(raw) -> tuple[Task, ...]:
    if not isinstance(raw, list) or not raw:
        raise InputError("tasks 必须是非空数组")
    tasks = []
    seen = set()
    for i, item in enumerate(raw):
        if not isinstance(item, dict):
            raise InputError(f"tasks[{i}] 必须是对象")
        task_id = item.get("id")
        if not isinstance(task_id, str) or not task_id:
            raise InputError(f"tasks[{i}].id 必须是非空字符串")
        if task_id in seen:
            raise InputError(f"任务 id 重复: {task_id}")
        seen.add(task_id)
        cpu = _require_int(item.get("cpu"), f"任务 {task_id} 的 cpu", 0)
        memory = _require_int(item.get("memory"), f"任务 {task_id} 的 memory", 0)
        duration = _require_int(item.get("duration"), f"任务 {task_id} 的 duration", 1)
        deps = item.get("deps", [])
        if not isinstance(deps, list) or not all(isinstance(d, str) for d in deps):
            raise InputError(f"任务 {task_id} 的 deps 必须是字符串数组")
        if task_id in deps:
            raise InputError(f"任务 {task_id} 不能依赖自身")
        tasks.append(
            Task(id=task_id, cpu=cpu, memory=memory, duration=duration, deps=tuple(deps))
        )
    known = {t.id for t in tasks}
    for t in tasks:
        for dep in t.deps:
            if dep not in known:
                raise InputError(f"任务 {t.id} 依赖了不存在的任务: {dep}")
    return tuple(tasks)


def topo_order(tasks: tuple[Task, ...]) -> tuple[str, ...]:
    """Kahn 拓扑排序，同层按 id 排序保证确定性；有环则抛 InputError。"""
    indegree = {t.id: len(t.deps) for t in tasks}
    dependents: dict[str, list[str]] = {t.id: [] for t in tasks}
    for t in tasks:
        for dep in t.deps:
            dependents[dep].append(t.id)
    ready = sorted(tid for tid, deg in indegree.items() if deg == 0)
    order = []
    while ready:
        tid = ready.pop(0)
        order.append(tid)
        for nxt in dependents[tid]:
            indegree[nxt] -= 1
            if indegree[nxt] == 0:
                ready.append(nxt)
        ready.sort()
    if len(order) != len(tasks):
        remaining = sorted(tid for tid, deg in indegree.items() if deg > 0)
        raise InputError(f"任务依赖存在环，涉及: {', '.join(remaining)}")
    return tuple(order)


def _check_dag(tasks: tuple[Task, ...]) -> None:
    topo_order(tasks)
