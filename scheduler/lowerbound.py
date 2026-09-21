"""工期下界：关键路径、CPU 资源负载、内存资源负载三者的最大值。"""

from __future__ import annotations

from .loader import topo_order
from .models import BuildInput


def _ceil_div(a: int, b: int) -> int:
    return -(-a // b)


def critical_path_length(build: BuildInput) -> int:
    tasks = {t.id: t for t in build.tasks}
    longest: dict[str, int] = {}
    for tid in topo_order(build.tasks):
        t = tasks[tid]
        longest[tid] = t.duration + max((longest[d] for d in t.deps), default=0)
    return max(longest.values(), default=0)


def lower_bound(build: BuildInput) -> int:
    cap = build.capacity
    cpu_work = sum(t.cpu * t.duration for t in build.tasks)
    mem_work = sum(t.memory * t.duration for t in build.tasks)
    return max(
        critical_path_length(build),
        _ceil_div(cpu_work, cap.cpu),
        _ceil_div(mem_work, cap.memory),
    )
