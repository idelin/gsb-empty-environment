"""Render a human-readable report with a causal chain for every change."""

from __future__ import annotations

from .model import Lock
from .solver import Change, SolveResult, Status


def _chain_to_root(result: SolveResult, lock: Lock, package: str) -> str:
    """Walk the constraints that forced each change upward to the root.

    At each step, follow the source whose constraint actually rejects the
    locked version; a rejecting root requirement ends the chain. If the
    locked version satisfies every constraint (or the package is new),
    fall back to the first dependency source (the reachability path).
    """
    chain = [package]
    seen = {package}
    current = package
    while True:
        reason = result.reasons.get(current)
        if reason is None:
            break
        locked = lock.pinned.get(current)
        rejecting = [
            source
            for constraint, source in reason.constraints
            if locked is not None and not constraint.allows(locked)
        ]
        if any(s.kind == "root" for s in rejecting):
            break
        source = next((s for s in rejecting if s.kind == "dependency"), None)
        if source is None:
            source = next(
                (s for _, s in reason.constraints if s.kind == "dependency"),
                None,
            )
        if source is None or source.package in seen:
            break
        chain.append(source.package)
        seen.add(source.package)
        current = source.package
    return "root -> " + " -> ".join(reversed(chain))


def _why_changed(change: Change, result: SolveResult, lock: Lock) -> list:
    lines = []
    reason = result.reasons.get(change.package)
    if change.kind == "removed":
        lines.append(
            "why: no longer reachable from the root requirements "
            "(orphan policy: prune)"
        )
        return lines
    if change.kind == "added":
        causes = "; ".join(
            f"{source.describe()} requires {change.package} {constraint}"
            for constraint, source in (reason.constraints if reason else ())
        )
        lines.append(f"why: newly required by {causes}")
    else:
        locked = change.old
        violating = [
            (constraint, source)
            for constraint, source in (reason.constraints if reason else ())
            if not constraint.allows(locked)
        ]
        if violating:
            causes = "; ".join(
                f'locked {locked} violates "{constraint}" from {source.describe()}'
                for constraint, source in violating
            )
            lines.append(f"why: {causes}")
        elif reason and reason.locked_rejection:
            lines.append(
                f"why: locked {locked} satisfies its own constraints but leads "
                f"to conflict: {reason.locked_rejection}"
            )
        else:
            lines.append(f"why: locked {locked} could not be kept")
    lines.append(f"chain: {_chain_to_root(result, lock, change.package)}")
    lines.append(
        f"selected: {change.new} is the newest version satisfying all "
        "active constraints (policy: minimal changes, then prefer newer)"
    )
    return lines


def render(result: SolveResult, lock: Lock, keep_orphans: bool) -> str:
    lines = []
    if result.status is Status.SOLVED:
        lines.append("status: solved (optimal: search space exhausted)")
    elif result.status is Status.UNSATISFIABLE:
        lines.append("status: unsatisfiable (proven: search space exhausted)")
    else:
        note = (
            "incomplete: raise --max-states to continue"
            if result.resolution
            else "incomplete: no proven result; raise --max-states"
        )
        lines.append(f"status: search-limit-reached ({note})")
    lines.append(f"explored states: {result.states}")
    if result.resolution and not result.optimal:
        lines.append("WARNING: solution below is best-so-far, not proven optimal")
    if result.changes:
        lines.append(f"changes ({len(result.changes)}):")
        for change in result.changes:
            if change.kind == "removed":
                lines.append(f"  {change.package}: {change.old} -> (removed)")
            elif change.kind == "added":
                lines.append(f"  {change.package}: (none) -> {change.new} (added)")
            else:
                lines.append(
                    f"  {change.package}: {change.old} -> {change.new} "
                    f"({change.kind})"
                )
            for detail in _why_changed(change, result, lock):
                lines.append(f"    {detail}")
    elif result.resolution:
        lines.append("changes (0): lockfile already satisfies the requirements")
    if result.resolution:
        lines.append("new lock:")
        for name in sorted(result.resolution):
            marker = ""
            if keep_orphans and name not in result.reasons:
                marker = "  # kept orphan (policy: keep)"
            lines.append(f"  {name}: {result.resolution[name]}{marker}")
    if result.conflicts and not result.resolution:
        lines.append("conflicts (most recent first):")
        for conflict in result.conflicts:
            lines.append(f"  {conflict}")
    return "\n".join(lines)
