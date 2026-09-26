"""Feasibility checking, kept deliberately separate from the optimizer.

A *plan* maps a device name to the tuple of slots it occupies.  Maximal
runs of consecutive slots form segments; every segment start (including a
restart after an interruption) is charged one `startup_cost`.
"""

from __future__ import annotations

from fractions import Fraction

from .model import Instance


def segments_of(slots: tuple[int, ...]) -> int:
    """Number of maximal consecutive runs (segments) in a slot tuple."""
    runs = 0
    previous = None
    for slot in sorted(slots):
        if previous is None or slot != previous + 1:
            runs += 1
        previous = slot
    return runs


def _devices_by_name(instance: Instance):
    return {device.name: device for device in instance.devices}


def plan_cost(instance: Instance, plan: dict[str, tuple[int, ...]]) -> int:
    """Total startup cost: sum of startup_cost * segments per device."""
    devices = _devices_by_name(instance)
    return sum(
        devices[name].startup_cost * segments_of(slots)
        for name, slots in plan.items()
    )


def plan_score(instance: Instance, plan: dict[str, tuple[int, ...]]) -> Fraction:
    """Total objective weight of the plan (exact rational arithmetic)."""
    devices = _devices_by_name(instance)
    return sum((devices[name].score for name in plan), Fraction(0))


def validate_instance(instance: Instance) -> None:
    """Raise ValueError on malformed synthetic data."""
    if instance.horizon < 1:
        raise ValueError("horizon must be >= 1")
    if instance.budget < 0:
        raise ValueError("budget must be >= 0")
    for group, limit in instance.group_limits.items():
        if limit < 0:
            raise ValueError(f"group {group!r}: negative simultaneous limit")
    for slot in instance.blocked_slots:
        if not 0 <= slot < instance.horizon:
            raise ValueError(f"blocked slot {slot} outside window")
    seen: set[str] = set()
    for device in instance.devices:
        if device.name in seen:
            raise ValueError(f"duplicate device name {device.name!r}")
        seen.add(device.name)
        if device.group not in instance.group_limits:
            raise ValueError(f"{device.name}: unknown group {device.group!r}")
        if device.duration < 1:
            raise ValueError(f"{device.name}: duration must be >= 1")
        if device.startup_cost < 0:
            raise ValueError(f"{device.name}: negative startup cost")
        if device.score < 0:
            raise ValueError(f"{device.name}: negative score weight")


def check_feasible(instance: Instance, plan: dict[str, tuple[int, ...]]) -> list[str]:
    """Return a list of human-readable violations; empty means feasible."""
    errors: list[str] = []
    devices = _devices_by_name(instance)
    for name, slots in plan.items():
        device = devices.get(name)
        if device is None:
            errors.append(f"unknown device {name!r}")
            continue
        if len(set(slots)) != len(slots):
            errors.append(f"{name}: duplicate slots")
        if len(slots) != device.duration:
            errors.append(
                f"{name}: needs {device.duration} slots, got {len(slots)}"
            )
        for slot in slots:
            if not isinstance(slot, int) or not 0 <= slot < instance.horizon:
                errors.append(f"{name}: slot {slot!r} outside window")
            elif slot in instance.blocked_slots:
                errors.append(f"{name}: slot {slot} is blocked")
    usage = {group: [0] * instance.horizon for group in instance.group_limits}
    for name, slots in plan.items():
        device = devices.get(name)
        if device is None:
            continue
        for slot in slots:
            if isinstance(slot, int) and 0 <= slot < instance.horizon:
                usage[device.group][slot] += 1
    for group, used_per_slot in usage.items():
        limit = instance.group_limits[group]
        for slot, used in enumerate(used_per_slot):
            if used > limit:
                errors.append(
                    f"group {group!r}: slot {slot} runs {used} jobs > limit {limit}"
                )
    known = {name: slots for name, slots in plan.items() if name in devices}
    cost = plan_cost(instance, known)
    if cost > instance.budget:
        errors.append(f"startup cost {cost} exceeds budget {instance.budget}")
    return errors
