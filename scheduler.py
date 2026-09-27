#!/usr/bin/env python3
"""Single-machine job scheduling with maintenance windows.

Each job has a release time, a due time, an integer duration, an integer
weight and finish-to-start precedence constraints.  The machine processes at
most one job at a time, is unavailable during maintenance windows, and jobs
cannot be preempted.  All time points are integers; intervals are treated as
half-open ``[start, end)`` so touching intervals never conflict.

Objective (lexicographic):
  1. maximise total weight of on-time completed (scheduled) jobs;
  2. among optimum solutions, minimise total completion time.

Small instances (n <= 14 jobs by default) are solved to proven optimality with
an exact subset-DP carrying a Pareto frontier of (machine-free-time, total
completion time).  Larger instances fall back to a priority-rule heuristic and
are explicitly flagged as not proven optimal.
"""

from __future__ import annotations

import argparse
import json
import sys
import time
from dataclasses import dataclass


@dataclass(frozen=True)
class Job:
    id: str
    release: int
    due: int
    duration: int
    weight: int = 1
    predecessors: tuple[str, ...] = ()


@dataclass(frozen=True)
class ScheduledJob:
    id: str
    start: int
    end: int
    release: int
    due: int
    duration: int
    weight: int

    def as_dict(self) -> dict:
        return {
            "id": self.id,
            "start": self.start,
            "end": self.end,
            "release": self.release,
            "due": self.due,
            "duration": self.duration,
            "weight": self.weight,
        }


class InvalidInstance(ValueError):
    """Raised when an input instance is malformed."""


def _merge_windows(windows: list[tuple[int, int]]) -> list[tuple[int, int]]:
    """Merge overlapping/abutting integer half-open windows."""
    ws = sorted((int(a), int(b)) for a, b in windows)
    merged: list[tuple[int, int]] = []
    for start, end in ws:
        if not merged or start > merged[-1][1]:
            merged.append((start, end))
        else:
            prev_start, prev_end = merged[-1]
            merged[-1] = (prev_start, max(prev_end, end))
    return merged


def next_feasible_start(t: int, duration: int, maintenance: list[tuple[int, int]]) -> int:
    """Earliest integer start >= t for an uninterrupted run of ``duration``."""
    s = t
    while True:
        hit: tuple[int, int] | None = None
        finish = s + duration
        for window in maintenance:
            if s < window[1] and finish > window[0]:
                hit = window
                break
        if hit is None:
            return s
        s = hit[1]


def standalone_earliest(job: Job, maintenance: list[tuple[int, int]]) -> int | None:
    """Earliest feasible completion ignoring other jobs, or None if infeasible."""
    start = next_feasible_start(job.release, job.duration, maintenance)
    if start + job.duration <= job.due:
        return start + job.duration
    return None


def parse_instance(data: dict) -> tuple[list[Job], list[tuple[int, int]]]:
    if not isinstance(data, dict):
        raise InvalidInstance("input must be a JSON object")
    raw_jobs = data.get("jobs")
    if not isinstance(raw_jobs, list) or not raw_jobs:
        raise InvalidInstance("'jobs' must be a non-empty list")

    jobs: list[Job] = []
    seen: set[str] = set()
    for index, raw in enumerate(raw_jobs):
        if not isinstance(raw, dict):
            raise InvalidInstance(f"job #{index} must be an object")
        job_id = raw.get("id", f"J{index + 1}")
        if not isinstance(job_id, str) or not job_id:
            raise InvalidInstance(f"job #{index}: 'id' must be a non-empty string")
        if job_id in seen:
            raise InvalidInstance(f"duplicate job id: {job_id}")
        seen.add(job_id)

        def as_int(field: str) -> int:
            value = raw.get(field)
            if isinstance(value, bool) or not isinstance(value, int):
                raise InvalidInstance(f"job {job_id}: '{field}' must be an integer")
            return value

        release = as_int("release")
        due = as_int("due")
        duration = as_int("duration")
        weight = raw.get("weight", 1)
        if isinstance(weight, bool) or not isinstance(weight, int):
            raise InvalidInstance(f"job {job_id}: 'weight' must be an integer")
        if release < 0:
            raise InvalidInstance(f"job {job_id}: release must be >= 0")
        if duration <= 0:
            raise InvalidInstance(f"job {job_id}: duration must be > 0")
        if due < release:
            raise InvalidInstance(f"job {job_id}: due must be >= release")
        if weight < 0:
            raise InvalidInstance(f"job {job_id}: weight must be >= 0")
        predecessors = raw.get("predecessors", [])
        if not isinstance(predecessors, list) or not all(isinstance(p, str) for p in predecessors):
            raise InvalidInstance(f"job {job_id}: 'predecessors' must be a list of strings")
        jobs.append(
            Job(
                id=job_id,
                release=release,
                due=due,
                duration=duration,
                weight=weight,
                predecessors=tuple(predecessors),
            )
        )

    ids = {job.id for job in jobs}
    for job in jobs:
        for predecessor in job.predecessors:
            if predecessor not in ids:
                raise InvalidInstance(f"job {job.id}: unknown predecessor {predecessor}")

    raw_windows = data.get("maintenance", [])
    if not isinstance(raw_windows, list):
        raise InvalidInstance("'maintenance' must be a list")
    windows: list[tuple[int, int]] = []
    for index, window in enumerate(raw_windows):
        if (
            not isinstance(window, (list, tuple))
            or len(window) != 2
            or isinstance(window[0], bool)
            or isinstance(window[1], bool)
            or not all(isinstance(v, int) for v in window)
        ):
            raise InvalidInstance(f"maintenance window #{index} must be [start, end] integers")
        start, end = window
        if start < 0 or end <= start:
            raise InvalidInstance(f"maintenance window #{index}: need 0 <= start < end")
        windows.append((start, end))

    _check_cycle(jobs)
    return jobs, _merge_windows(windows)


def _check_cycle(jobs: list[Job]) -> None:
    remaining = {job.id: set(job.predecessors) for job in jobs}
    while True:
        ready = [job_id for job_id, preds in remaining.items() if not preds]
        if not ready:
            if remaining:
                raise InvalidInstance(
                    "precedence graph contains a cycle: " + ", ".join(sorted(remaining))
                )
            return
        for job_id in ready:
            del remaining[job_id]
            for preds in remaining.values():
                preds.discard(job_id)


def solve_exact(
    jobs: list[Job], maintenance: list[tuple[int, int]]
) -> tuple[list[ScheduledJob], dict, list[bool]]:
    """Solve to proven optimality with subset-DP plus Pareto labels."""
    start_wall = time.perf_counter()
    n = len(jobs)
    pred_masks = [0] * n
    index_of = {job.id: i for i, job in enumerate(jobs)}
    for i, job in enumerate(jobs):
        mask = 0
        for predecessor in job.predecessors:
            mask |= 1 << index_of[predecessor]
        pred_masks[i] = mask

    full = 1 << n
    weight_of = [0] * full
    for mask in range(1, full):
        low = mask & -mask
        i = low.bit_length() - 1
        weight_of[mask] = weight_of[mask ^ low] + jobs[i].weight

    # labels[mask]: Pareto frontier of (machine_free_time, sum_completion).
    labels: list[list[tuple[int, int]]] = [[] for _ in range(full)]
    labels[0] = [(0, 0)]

    best_mask = 0
    best_weight = 0
    best_sum = 0

    for mask in range(1, full):
        frontier: list[tuple[int, int]] = []
        bits = mask
        while bits:
            low = bits & -bits
            i = low.bit_length() - 1
            prev_mask = mask ^ low
            if (pred_masks[i] & prev_mask) == pred_masks[i]:
                job = jobs[i]
                for free_time, sum_completion in labels[prev_mask]:
                    start = next_feasible_start(
                        max(free_time, job.release), job.duration, maintenance
                    )
                    completion = start + job.duration
                    if completion <= job.due:
                        candidate = (completion, sum_completion + completion)
                        dominated = any(
                            t <= candidate[0] and sc <= candidate[1]
                            for t, sc in frontier
                        )
                        if not dominated:
                            frontier = [
                                (t, sc)
                                for t, sc in frontier
                                if not (candidate[0] <= t and candidate[1] <= sc)
                            ]
                            frontier.append(candidate)
            bits ^= low
        labels[mask] = frontier
        if frontier:
            total_weight = weight_of[mask]
            min_sum = min(sc for _, sc in frontier)
            if (total_weight, -min_sum) > (best_weight, -best_sum):
                best_weight = total_weight
                best_sum = min_sum
                best_mask = mask

    chosen_mask = best_mask
    chosen_label = min(
        (label for label in labels[chosen_mask]),
        key=lambda label: label[1],
    )

    schedule = _reconstruct(
        jobs, labels, pred_masks, maintenance, chosen_mask, chosen_label
    )
    schedule.sort(key=lambda item: item.start)

    stats = {
        "method": "exact_subset_dp",
        "proven_optimal": True,
        "subset_states": full,
        "feasible_subsets": sum(1 for frontier in labels if frontier),
        "elapsed_seconds": round(time.perf_counter() - start_wall, 6),
        "total_weight": best_weight,
        "total_completion_time": sum(item.end for item in schedule),
        "scheduled_count": len(schedule),
        "job_count": n,
    }
    return schedule, stats, [bool(frontier) for frontier in labels]


def _reconstruct(
    jobs: list[Job],
    labels: list[list[tuple[int, int]]],
    pred_masks: list[int],
    maintenance: list[tuple[int, int]],
    mask: int,
    target: tuple[int, int],
) -> list[ScheduledJob]:
    result: list[ScheduledJob] = []
    current_mask = mask
    current_label = target
    while current_mask:
        free_time, sum_completion = current_label
        found = False
        bits = current_mask
        while bits and not found:
            low = bits & -bits
            i = low.bit_length() - 1
            prev_mask = current_mask ^ low
            if (pred_masks[i] & prev_mask) != pred_masks[i]:
                bits ^= low
                continue
            job = jobs[i]
            for prev_free, prev_sum in labels[prev_mask]:
                start = next_feasible_start(
                    max(prev_free, job.release), job.duration, maintenance
                )
                completion = start + job.duration
                if (
                    completion <= job.due
                    and completion == free_time
                    and prev_sum + completion == sum_completion
                ):
                    result.append(
                        ScheduledJob(
                            id=job.id,
                            start=start,
                            end=completion,
                            release=job.release,
                            due=job.due,
                            duration=job.duration,
                            weight=job.weight,
                        )
                    )
                    current_mask = prev_mask
                    current_label = (prev_free, prev_sum)
                    found = True
                    break
            bits ^= low
        if not found:  # pragma: no cover - defensive, reconstruction always exists
            raise RuntimeError("internal error: failed to reconstruct optimal schedule")
    return result


def _standalone_map(
    jobs: list[Job], maintenance: list[tuple[int, int]]
) -> dict[str, int | None]:
    return {job.id: standalone_earliest(job, maintenance) for job in jobs}


def analyze_unscheduled_exact(
    jobs: list[Job],
    maintenance: list[tuple[int, int]],
    scheduled_ids: set[str],
    feasible_masks: list[bool],
    scheduled_mask: int,
) -> list[dict]:
    index_of = {job.id: i for i, job in enumerate(jobs)}
    standalone = _standalone_map(jobs, maintenance)
    reasons: list[dict] = []
    for job in jobs:
        if job.id in scheduled_ids:
            continue
        missing_predecessors = [
            predecessor
            for predecessor in job.predecessors
            if predecessor not in scheduled_ids
        ]
        if standalone[job.id] is None:
            code = "no_feasible_slot"
            detail = (
                f"即使独占机器，最早可行完成时间也晚于截止 {job.due}"
                f"（释放 {job.release}，时长 {job.duration}）"
            )
        elif missing_predecessors:
            code = "predecessor_unscheduled"
            detail = "前置工单未安排：" + ", ".join(missing_predecessors)
        else:
            union_mask = scheduled_mask | (1 << index_of[job.id])
            if feasible_masks[union_mask]:
                code = "excluded_by_secondary_objective"
                detail = (
                    "权重为 0：加入后权重不变但总完成时间增加，故按第二目标被排除"
                )
            else:
                code = "capacity_conflict"
                detail = (
                    "在保持最优权重集合的前提下无法同时排入（机器产能/维护窗口约束），"
                    "加入它必须挤出其他工单"
                )
        reasons.append({"id": job.id, "reason": code, "detail": detail})
    return reasons


def analyze_unscheduled_heuristic(
    jobs: list[Job],
    maintenance: list[tuple[int, int]],
    scheduled_ids: set[str],
) -> list[dict]:
    standalone = _standalone_map(jobs, maintenance)
    reasons: list[dict] = []
    for job in jobs:
        if job.id in scheduled_ids:
            continue
        missing_predecessors = [
            predecessor
            for predecessor in job.predecessors
            if predecessor not in scheduled_ids
        ]
        if standalone[job.id] is None:
            code = "no_feasible_slot"
            detail = (
                f"即使独占机器，最早可行完成时间也晚于截止 {job.due}"
                f"（释放 {job.release}，时长 {job.duration}）"
            )
        elif missing_predecessors:
            code = "predecessor_unscheduled"
            detail = "前置工单未安排：" + ", ".join(missing_predecessors)
        else:
            code = "capacity_conflict"
            detail = "启发式未能在剩余产能中为其找到按时位置（该结论未经最优性证明）"
        reasons.append({"id": job.id, "reason": code, "detail": detail})
    return reasons


def _simulate(
    jobs: list[Job],
    maintenance: list[tuple[int, int]],
    key,
) -> tuple[list[ScheduledJob], set[str]]:
    remaining = set(jobs)
    by_id = {job.id: job for job in jobs}
    dropped: set[str] = set()
    schedule: list[ScheduledJob] = []
    free_time = 0

    def cascade(job: Job) -> None:
        """A job can never run; remove it and everything depending on it."""
        stack = [job.id]
        while stack:
            current_id = stack.pop()
            current = by_id[current_id]
            if current in remaining:
                remaining.remove(current)
                dropped.add(current_id)
                for other in jobs:
                    if other in remaining and current_id in other.predecessors:
                        stack.append(other.id)

    while remaining:
        # A ready job's predecessors are all already scheduled.
        ready = [
            job
            for job in remaining
            if all(
                any(item.id == predecessor for item in schedule)
                for predecessor in job.predecessors
            )
        ]
        if not ready:
            # Remaining jobs only depend on dropped jobs: cascade them all.
            for job in list(remaining):
                cascade(job)
            break

        candidates: list[tuple[Job, int]] = []
        for job in ready:
            earliest = max(free_time, job.release)
            start = next_feasible_start(earliest, job.duration, maintenance)
            if start + job.duration <= job.due:
                candidates.append((job, start))

        if not candidates:
            for job in list(ready):
                cascade(job)
            continue

        startable = [(job, start) for job, start in candidates if start == free_time]
        if startable:
            job, start = min(startable, key=lambda pair: key(pair[0]))
        else:
            # Machine must idle until the earliest candidate can begin.
            free_time = min(start for _, start in candidates)
            continue

        completion = start + job.duration
        schedule.append(
            ScheduledJob(
                id=job.id,
                start=start,
                end=completion,
                release=job.release,
                due=job.due,
                duration=job.duration,
                weight=job.weight,
            )
        )
        remaining.remove(job)
        free_time = completion

    schedule.sort(key=lambda item: item.start)
    return schedule, dropped


def solve_heuristic(
    jobs: list[Job], maintenance: list[tuple[int, int]]
) -> tuple[list[ScheduledJob], dict, set[str]]:
    """Best of several priority rules; result is NOT proven optimal."""
    start_wall = time.perf_counter()
    rules = [
        ("edd", lambda job: (job.due, job.release, -job.weight, job.id)),
        ("weighted_density", lambda job: (-job.weight / job.duration, job.due, job.id)),
        ("weight_first", lambda job: (-job.weight, job.due, job.release, job.id)),
        ("release_then_due", lambda job: (job.release, job.due, -job.weight, job.id)),
    ]
    best: tuple[list[ScheduledJob], set[str]] | None = None
    best_key = None
    best_name = ""
    for name, key in rules:
        schedule, dropped = _simulate(jobs, maintenance, key)
        value = (
            sum(item.weight for item in schedule),
            -sum(item.end for item in schedule),
        )
        if best_key is None or value > best_key:
            best_key = value
            best = (schedule, dropped)
            best_name = name

    assert best is not None
    schedule, dropped = best
    stats = {
        "method": "priority_rule_heuristic:" + best_name,
        "proven_optimal": False,
        "elapsed_seconds": round(time.perf_counter() - start_wall, 6),
        "total_weight": sum(item.weight for item in schedule),
        "total_completion_time": sum(item.end for item in schedule),
        "scheduled_count": len(schedule),
        "job_count": len(jobs),
    }
    return schedule, stats, dropped


def validate_schedule(
    jobs: list[Job],
    maintenance: list[tuple[int, int]],
    schedule: list[ScheduledJob],
) -> list[str]:
    """Return a list of constraint violations; empty list means valid."""
    by_id = {job.id: job for job in jobs}
    violations: list[str] = []
    seen: set[str] = set()
    ends = {item.id: item.end for item in schedule}
    for item in schedule:
        if item.id in seen:
            violations.append(f"job {item.id} scheduled more than once")
        seen.add(item.id)
        if item.id not in by_id:
            violations.append(f"unknown scheduled job {item.id}")
            continue
        job = by_id[item.id]
        if item.duration != job.duration or item.end != item.start + item.duration:
            violations.append(f"job {item.id}: duration/end mismatch")
        if item.start < job.release:
            violations.append(f"job {item.id}: starts before release")
        if item.end > job.due:
            violations.append(f"job {item.id}: completes after due")
        for predecessor in job.predecessors:
            if predecessor in ends and item.start < ends[predecessor]:
                violations.append(f"job {item.id}: starts before predecessor {predecessor} finishes")
        for window in maintenance:
            if item.start < window[1] and item.end > window[0]:
                violations.append(f"job {item.id}: overlaps maintenance {list(window)}")

    ordered = sorted(schedule, key=lambda item: item.start)
    for earlier, later in zip(ordered, ordered[1:]):
        if later.start < earlier.end:
            violations.append(
                f"jobs {earlier.id} and {later.id} overlap "
                f"([{earlier.start},{earlier.end}) vs [{later.start},{later.end}))"
            )
    return violations


def solve(
    data: dict, mode: str = "auto", threshold: int = 14
) -> dict:
    jobs, maintenance = parse_instance(data)
    n = len(jobs)
    if mode == "auto":
        use_exact = n <= threshold
    elif mode == "exact":
        use_exact = True
    elif mode == "heuristic":
        use_exact = False
    else:
        raise ValueError(f"unknown mode: {mode}")

    if use_exact:
        schedule, stats, feasible = solve_exact(jobs, maintenance)
        scheduled_ids = {item.id for item in schedule}
        scheduled_mask = 0
        index_of = {job.id: i for i, job in enumerate(jobs)}
        for job_id in scheduled_ids:
            scheduled_mask |= 1 << index_of[job_id]
        unscheduled = analyze_unscheduled_exact(
            jobs, maintenance, scheduled_ids, feasible, scheduled_mask
        )
    else:
        schedule, stats, _dropped = solve_heuristic(jobs, maintenance)
        scheduled_ids = {item.id for item in schedule}
        unscheduled = analyze_unscheduled_heuristic(jobs, maintenance, scheduled_ids)
        total_positive_weight = sum(job.weight for job in jobs if job.weight > 0)
        if stats["total_weight"] == total_positive_weight:
            # Every positive-weight job is on time: the weight objective is
            # provably optimal (it is the global upper bound).  The secondary
            # objective among all such sets is heuristic.
            stats["proven_optimal_weight"] = True
            stats["proven_optimal_secondary"] = False
            stats["note"] = (
                "所有正权重工单均已按时安排，权重目标达到全局上界；"
                "总完成时间最小化未经证明"
            )
        else:
            stats["proven_optimal_weight"] = False
            stats["proven_optimal_secondary"] = False
            stats["note"] = "启发式解，权重与总完成时间均未经最优性证明"

    violations = validate_schedule(jobs, maintenance, schedule)
    if violations:  # pragma: no cover - defensive
        raise RuntimeError("generated schedule is invalid: " + "; ".join(violations))

    return {
        "status": "optimal" if stats["proven_optimal"] else "feasible_not_proven_optimal",
        "maintenance": [list(window) for window in maintenance],
        "scheduled": [item.as_dict() for item in schedule],
        "unscheduled": unscheduled,
        "statistics": stats,
        "validation": {
            "valid": True,
            "violations": [],
            "interval_convention": "half-open [start, end); touching does not conflict",
        },
    }


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(
        description="含维护窗口的单机工单排程（半开区间 [start,end)）"
    )
    parser.add_argument(
        "input",
        nargs="?",
        help="JSON 输入文件路径；省略时从标准输入读取",
    )
    parser.add_argument(
        "--mode",
        choices=("auto", "exact", "heuristic"),
        default="auto",
        help="auto：n<=阈值用精确 DP；exact：强制精确；heuristic：强制启发式",
    )
    parser.add_argument(
        "--threshold",
        type=int,
        default=14,
        help="auto 模式下使用精确算法的最大工单数（默认 14）",
    )
    args = parser.parse_args(argv)

    if args.input:
        try:
            with open(args.input, "r", encoding="utf-8") as handle:
                data = json.load(handle)
        except OSError as exc:
            print(json.dumps({"status": "invalid_instance", "error": f"cannot read input: {exc}"}, ensure_ascii=False), file=sys.stderr)
            return 2
        except json.JSONDecodeError as exc:
            print(json.dumps({"status": "invalid_instance", "error": f"invalid JSON: {exc}"}, ensure_ascii=False), file=sys.stderr)
            return 2
    else:
        try:
            data = json.load(sys.stdin)
        except json.JSONDecodeError as exc:
            print(json.dumps({"status": "invalid_instance", "error": f"invalid JSON: {exc}"}, ensure_ascii=False), file=sys.stderr)
            return 2

    try:
        result = solve(data, mode=args.mode, threshold=args.threshold)
    except InvalidInstance as exc:
        print(json.dumps({"status": "invalid_instance", "error": str(exc)}, ensure_ascii=False), file=sys.stderr)
        return 2

    print(json.dumps(result, ensure_ascii=False, indent=2))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
