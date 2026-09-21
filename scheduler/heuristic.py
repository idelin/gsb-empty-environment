"""确定性列表调度启发式（并行 SGS，事件驱动）。

优先级：关键路径位次（到汇点的最长时长链）降序，平局按任务 id 升序，
因此同一输入永远产生同一调度。结果仅为启发式上界，不代表全局最优。
"""

from __future__ import annotations

from dataclasses import dataclass

from .errors import InfeasibleError
from .models import BuildInput, Task


@dataclass(frozen=True)
class ScheduledTask:
    task_id: str
    start: int
    end: int


@dataclass(frozen=True)
class ScheduleResult:
    entries: tuple[ScheduledTask, ...]
    makespan: int


def check_feasible(build: BuildInput) -> None:
    cap = build.capacity
    oversized = [
        t.id for t in build.tasks if t.cpu > cap.cpu or t.memory > cap.memory
    ]
    if oversized:
        raise InfeasibleError(
            "单任务资源需求超过容量，问题不可行: " + ", ".join(sorted(oversized))
        )


def critical_path_ranks(tasks: dict[str, Task]) -> dict[str, int]:
    """每个任务到汇点的最长时长链（含自身时长）。"""
    dependents: dict[str, list[str]] = {tid: [] for tid in tasks}
    for t in tasks.values():
        for dep in t.deps:
            dependents[dep].append(t.id)
    ranks: dict[str, int] = {}

    def rank(tid: str) -> int:
        if tid not in ranks:
            best = max((rank(nxt) for nxt in dependents[tid]), default=0)
            ranks[tid] = tasks[tid].duration + best
        return ranks[tid]

    for tid in tasks:
        rank(tid)
    return ranks


def build_schedule(build: BuildInput) -> ScheduleResult:
    check_feasible(build)
    tasks = {t.id: t for t in build.tasks}
    ranks = critical_path_ranks(tasks)
    cap = build.capacity

    def priority(task_id: str):
        return (-ranks[task_id], task_id)

    started: set[str] = set()
    done: set[str] = set()
    running: list[tuple[int, str]] = []
    entries: list[ScheduledTask] = []
    cpu_used = 0
    mem_used = 0
    time = 0

    while len(done) < len(tasks):
        candidates = sorted(
            (
                tid
                for tid, t in tasks.items()
                if tid not in started and all(d in done for d in t.deps)
            ),
            key=priority,
        )
        for tid in candidates:
            t = tasks[tid]
            if cpu_used + t.cpu <= cap.cpu and mem_used + t.memory <= cap.memory:
                entries.append(ScheduledTask(tid, time, time + t.duration))
                running.append((time + t.duration, tid))
                started.add(tid)
                cpu_used += t.cpu
                mem_used += t.memory
        if not running:
            # 已通过单任务可行性检查，理论上不会到达；保留防御。
            raise InfeasibleError("调度无法推进：就绪任务均超出剩余容量")
        next_time = min(end for end, _ in running)
        finished = sorted(tid for end, tid in running if end == next_time)
        running = [(end, tid) for end, tid in running if end != next_time]
        time = next_time
        for tid in finished:
            t = tasks[tid]
            cpu_used -= t.cpu
            mem_used -= t.memory
            done.add(tid)

    entries.sort(key=lambda e: (e.start, e.task_id))
    makespan = max(e.end for e in entries)
    return ScheduleResult(entries=tuple(entries), makespan=makespan)
