"""Deterministic list-scheduling heuristic and makespan lower bounds.

The heuristic is a parallel schedule generation scheme: at each event
time, ready tasks are considered in a fixed priority order (higher
critical-path rank, then longer duration, then lexicographic id) and
started greedily while CPU and memory capacity allow. The result is
deterministic for a given input and is NOT guaranteed to be globally
optimal.
"""
from __future__ import annotations

import math
from dataclasses import dataclass

from .model import Problem


class InfeasibleError(Exception):
    """Raised when no schedule can satisfy the capacity constraints."""


@dataclass(frozen=True)
class ScheduledTask:
    id: str
    start: int
    end: int


@dataclass(frozen=True)
class Schedule:
    entries: tuple[ScheduledTask, ...]
    makespan: int
    lower_bound: int


def _successors(problem: Problem) -> dict[str, list[str]]:
    succ: dict[str, list[str]] = {task.id: [] for task in problem.tasks}
    for task in problem.tasks:
        for dep in task.deps:
            succ[dep].append(task.id)
    return succ


def _topo_order(problem: Problem) -> list[str]:
    indegree = {task.id: len(task.deps) for task in problem.tasks}
    succ = _successors(problem)
    queue = sorted(tid for tid, deg in indegree.items() if deg == 0)
    order: list[str] = []
    while queue:
        tid = queue.pop()
        order.append(tid)
        for nxt in succ[tid]:
            indegree[nxt] -= 1
            if indegree[nxt] == 0:
                queue.append(nxt)
    return order


def critical_path_ranks(problem: Problem) -> dict[str, int]:
    """Rank of each task: its duration plus the max rank of successors."""
    tasks = problem.task_map()
    succ = _successors(problem)
    rank: dict[str, int] = {}
    for tid in reversed(_topo_order(problem)):
        downstream = max((rank[nxt] for nxt in succ[tid]), default=0)
        rank[tid] = tasks[tid].duration + downstream
    return rank


def lower_bound(problem: Problem) -> int:
    """Lower bound on the optimal makespan.

    Takes the max of the critical-path length and the cpu/memory
    workload bounds (total resource-time divided by capacity).
    """
    tasks = problem.task_map()
    longest: dict[str, int] = {}
    for tid in _topo_order(problem):
        task = tasks[tid]
        longest[tid] = task.duration + max(
            (longest[dep] for dep in task.deps), default=0
        )
    critical_path = max(longest.values())
    cpu_work = sum(t.cpu * t.duration for t in problem.tasks)
    mem_work = sum(t.memory * t.duration for t in problem.tasks)
    return max(
        critical_path,
        math.ceil(cpu_work / problem.cpu_capacity),
        math.ceil(mem_work / problem.memory_capacity),
    )


def schedule(problem: Problem) -> Schedule:
    """Build a deterministic heuristic schedule for the problem."""
    tasks = problem.task_map()
    for task in problem.tasks:
        if task.cpu > problem.cpu_capacity or task.memory > problem.memory_capacity:
            raise InfeasibleError(
                f"task {task.id!r} needs cpu={task.cpu}, memory={task.memory} "
                f"but capacity is cpu={problem.cpu_capacity}, "
                f"memory={problem.memory_capacity}"
            )

    rank = critical_path_ranks(problem)
    succ = _successors(problem)
    remaining = {task.id: len(task.deps) for task in problem.tasks}
    ready = [task.id for task in problem.tasks if not task.deps]

    def priority(tid: str) -> tuple:
        return (-rank[tid], -tasks[tid].duration, tid)

    entries: list[ScheduledTask] = []
    running: list[tuple[int, str]] = []  # (end_time, task_id)
    cpu_used = 0
    mem_used = 0
    time = 0
    completed = 0
    total = len(problem.tasks)

    while completed < total:
        for tid in sorted(ready, key=priority):
            task = tasks[tid]
            if (
                cpu_used + task.cpu <= problem.cpu_capacity
                and mem_used + task.memory <= problem.memory_capacity
            ):
                entries.append(ScheduledTask(tid, time, time + task.duration))
                running.append((time + task.duration, tid))
                cpu_used += task.cpu
                mem_used += task.memory
                ready.remove(tid)
        if not running:
            raise InfeasibleError("no ready task fits within capacity")
        time = min(end for end, _ in running)
        finished = sorted(tid for end, tid in running if end == time)
        running = [(end, tid) for end, tid in running if end > time]
        for tid in finished:
            cpu_used -= tasks[tid].cpu
            mem_used -= tasks[tid].memory
            completed += 1
            for nxt in succ[tid]:
                remaining[nxt] -= 1
                if remaining[nxt] == 0:
                    ready.append(nxt)

    makespan = max(entry.end for entry in entries)
    return Schedule(tuple(entries), makespan, lower_bound(problem))
