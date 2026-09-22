"""Decide which window definitions physically share a sort.

Compatibility rule
------------------
Window ``w2`` can reuse the sort of window ``w1`` iff:

1. their PARTITION BY multisets are identical, and
2. every ORDER BY key of ``w2`` matches the corresponding key of ``w1``
   position by position - same column, same ASC/DESC direction and same
   effective NULL placement - i.e. ``w2``'s order list is a *prefix* of
   ``w1``'s (identical lists included).

Corollaries enforced here:

* ASC vs DESC on a column breaks compatibility (no reverse-scan reuse).
* ``NULLS FIRST`` vs ``NULLS LAST`` breaks compatibility, even with the same
  direction.
* Reusing a *longer* sort to satisfy a shorter window is allowed: extra sort
  keys only break ties among peers and never change the value of a ROWS-frame
  aggregate or ranking function.  The frame itself is never shared, so two
  windows with different ROWS frames but the same order keys share the sort
  while producing independent results.
"""

from __future__ import annotations

from dataclasses import dataclass

from .model import OrderKey, WindowSpec


@dataclass(frozen=True)
class SortGroup:
    """One physical sort shape (partition columns + full order list)."""

    sort_id: str
    partition_by: tuple[str, ...]
    order_by: tuple[OrderKey, ...]
    window_names: tuple[str, ...]
    # window name -> its own ROWS frame text (frames are never merged)
    frames: dict[str, str]


@dataclass(frozen=True)
class SharePlan:
    groups: tuple[SortGroup, ...]
    # window name -> id of the sort that produces its ordered stream
    assignment: dict[str, str]
    logical_sort_count: int  # distinct sort shapes
    windows: dict[str, WindowSpec]


def _covers(longer: tuple[OrderKey, ...], shorter: tuple[OrderKey, ...]) -> bool:
    """True iff sorting by ``longer`` also sorts by ``shorter`` (prefix rule)."""
    if len(shorter) > len(longer):
        return False
    return all(
        long_key.signature() == short_key.signature()
        for long_key, short_key in zip(longer, shorter)
    )


def build_share_plan(windows: list[WindowSpec] | dict[str, WindowSpec]) -> SharePlan:
    """Build the minimal sort-sharing plan for the given window definitions.

    The minimum set of physical sorts is found by discarding every distinct
    ORDER BY signature that is itself a strict prefix of another *required*
    signature within the same partition: the longer sort already produces an
    ordering compatible with the shorter one.
    """

    if isinstance(windows, dict):
        window_map = dict(windows)
    else:
        window_map = dict((window.name, window) for window in windows)
        if len(window_map) != len(windows):
            raise ValueError("duplicate window names")

    # partition signature -> distinct order signatures actually required
    required: dict[tuple[str, ...], set[tuple]] = {}
    keys_for_sig: dict[tuple, tuple[OrderKey, ...]] = {}
    for window in window_map.values():
        part_sig = window.partition_signature()
        order_sig = window.order_signature()
        required.setdefault(part_sig, set()).add(order_sig)
        keys_for_sig.setdefault(order_sig, window.order_by)

    # Keep only maximal signatures: drop s when some t strictly covers s.
    maximal: dict[tuple[str, ...], list[tuple]] = {}
    for part_sig, sigs in required.items():
        kept = [
            sig
            for sig in sigs
            if not any(
                other != sig
                and _covers(keys_for_sig[other], keys_for_sig[sig])
                and len(other) > len(sig)
                for other in sigs
            )
        ]
        maximal[part_sig] = sorted(kept, key=lambda sig: (-len(sig), sig))

    # Stable sort ids across partition groups/order signatures.
    maximal_keys = [
        (part_sig, sig)
        for part_sig in sorted(maximal)
        for sig in maximal[part_sig]
    ]
    sort_id_for = {
        (part_sig, sig): f"S{index}"
        for index, (part_sig, sig) in enumerate(maximal_keys, start=1)
    }

    # Assign every window to the *shortest* maximal sort that covers it
    # (closest match); ties broken by deterministic sort id.
    assignment: dict[str, str] = {}
    members_by_sort: dict[str, list[WindowSpec]] = {
        sort_id: [] for sort_id in sort_id_for.values()
    }
    for window in sorted(window_map.values(), key=lambda item: item.name):
        part_sig = window.partition_signature()
        candidates = [
            sig
            for sig in maximal[part_sig]
            if _covers(keys_for_sig[sig], window.order_by)
        ]
        if not candidates:  # pragma: no cover - cannot happen by construction
            raise RuntimeError(f"no sort covers window {window.name}")
        best = min(candidates, key=lambda sig: (len(sig), sort_id_for[(part_sig, sig)]))
        sort_id = sort_id_for[(part_sig, best)]
        assignment[window.name] = sort_id
        members_by_sort[sort_id].append(window)

    groups: list[SortGroup] = []
    for (part_sig, sig), sort_id in sort_id_for.items():
        members = members_by_sort[sort_id]
        groups.append(
            SortGroup(
                sort_id=sort_id,
                partition_by=part_sig,
                order_by=keys_for_sig[sig],
                window_names=tuple(window.name for window in members),
                frames={
                    window.name: (
                        str(window.effective_frame())
                        if window.func in ('sum', 'count', 'avg')
                        else '(function ignores ROWS frames)'
                    )
                    for window in members
                },
            )
        )

    return SharePlan(
        groups=tuple(groups),
        assignment=assignment,
        logical_sort_count=len(groups),
        windows=window_map,
    )


def physical_sort_count(plan: SharePlan, partition_counts: dict[tuple[str, ...], int]) -> int:
    """Number of actual sort() calls: one per sort shape per non-empty partition."""
    return sum(partition_counts.get(group.partition_by, 0) for group in plan.groups)


def naive_sort_count(windows: list[WindowSpec] | dict[str, WindowSpec],
                     partition_counts: dict[tuple[str, ...], int]) -> int:
    """Sort calls a non-sharing implementation would issue (one per window)."""
    specs = list(windows.values()) if isinstance(windows, dict) else list(windows)
    return sum(partition_counts.get(window.partition_signature(), 0) for window in specs)


def _fmt_columns(columns: tuple[str, ...]) -> str:
    return ", ".join(columns) if columns else "(none - whole table)"


def _fmt_order(keys: tuple[OrderKey, ...]) -> str:
    return ", ".join(key.sql() for key in keys) if keys else "(none)"


def render_plan(plan: SharePlan, partition_counts: dict[tuple[str, ...], int] | None = None) -> str:
    lines = ["Window sort-sharing plan", "=" * 24]
    for group in plan.groups:
        lines.append(f"{group.sort_id}: PARTITION BY {_fmt_columns(group.partition_by)}")
        lines.append(f"    ORDER BY {_fmt_order(group.order_by)}")
        if partition_counts is not None:
            lines.append(
                f"    partitions sorted: {partition_counts.get(group.partition_by, 0)}"
            )
        group_sig = tuple(key.signature() for key in group.order_by)
        for name in group.window_names:
            window = plan.windows[name]
            shared = "exact" if window.order_signature() == group_sig else "prefix-reuse"
            lines.append(
                f"    - {name} [{window.func}] frame='{group.frames[name]}' "
                f"({shared})"
            )
    lines.append("-" * 24)
    lines.append(f"logical sort shapes: {plan.logical_sort_count}")
    if partition_counts is not None:
        lines.append(f"physical sort calls: {physical_sort_count(plan, partition_counts)}")
        lines.append(
            f"without sharing:     {naive_sort_count(plan.windows, partition_counts)}"
        )
    return "\n".join(lines)
