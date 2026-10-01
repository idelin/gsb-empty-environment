#!/usr/bin/env python3
"""Cascading revocation for hierarchical resource leases.

A lease may borrow quota from a parent lease. A child lease's time interval
must be contained in its parent's interval, and at any point in time the sum
of quotas of concurrently active children must not exceed the parent's quota.

When a lease is shortened (smaller time interval) or reduced (smaller quota),
some of its direct children may have to be revoked to restore the invariants.
This module computes a *minimum-loss* revocation set:

* loss = sum of weights of all revoked leases (each revoked lease counted
  exactly once, even though revocation cascades to its whole subtree);
* among minimum-loss solutions, the sorted sequence of revoked lease IDs
  is lexicographically smallest.

Revocation cascades recursively: revoking a lease revokes all of its
descendants. Descendants revoked by cascade are part of the revocation set
(and of the loss) exactly once -- losses are never double counted.

Command line usage:

    python3.11 lease_revoke.py [input.json]

Reads JSON from the given file, or from stdin when no file is given.
See README.md for the input/output format. All sample data is synthetic.
"""
from __future__ import annotations

import itertools
import json
import sys
from dataclasses import dataclass, field


class LeaseError(ValueError):
    """Raised when the lease tree or the requested operation is invalid."""


@dataclass
class Lease:
    id: str
    parent: str | None
    start: int
    end: int
    quota: int
    weight: int = 0


@dataclass
class LeaseTree:
    leases: dict[str, Lease] = field(default_factory=dict)
    children: dict[str, list[str]] = field(default_factory=dict)

    def kids_of(self, lease_id: str) -> list[str]:
        return self.children.get(lease_id, [])


def _capacity_ok(intervals: list[tuple[int, int, int]], start: int, end: int,
                 quota: int) -> bool:
    """True iff concurrent quota sum stays within ``quota`` over [start, end).

    ``intervals`` is a list of (start, end, quota) triples already clipped to
    the parent's interval. A sweep over the event points checks every segment.
    """
    points = {start, end}
    for s, e, _ in intervals:
        points.add(max(s, start))
        points.add(min(e, end))
    ordered = sorted(points)
    for a, b in zip(ordered, ordered[1:]):
        if a >= b:
            continue
        load = sum(q for s, e, q in intervals if s < b and e > a)
        if load > quota:
            return False
    return True


def _check_capacity(tree: LeaseTree, parent_id: str) -> None:
    parent = tree.leases[parent_id]
    intervals = []
    for kid_id in tree.kids_of(parent_id):
        kid = tree.leases[kid_id]
        if not (parent.start <= kid.start and kid.end <= parent.end):
            raise LeaseError(
                f"lease {kid_id!r} interval [{kid.start}, {kid.end}) is not "
                f"contained in parent {parent_id!r} [{parent.start}, {parent.end})"
            )
        intervals.append((kid.start, kid.end, kid.quota))
    if not _capacity_ok(intervals, parent.start, parent.end, parent.quota):
        raise LeaseError(
            f"concurrent children of {parent_id!r} exceed its quota "
            f"{parent.quota}"
        )


def build_tree(spec: list[dict]) -> LeaseTree:
    """Build and validate a lease tree from a list of lease dicts."""
    tree = LeaseTree()
    for item in spec:
        try:
            lease = Lease(
                id=str(item["id"]),
                parent=item.get("parent"),
                start=int(item["start"]),
                end=int(item["end"]),
                quota=int(item["quota"]),
                weight=int(item.get("weight", 0)),
            )
        except (KeyError, TypeError, ValueError) as exc:
            raise LeaseError(f"malformed lease entry {item!r}: {exc}") from exc
        if lease.parent is not None:
            lease.parent = str(lease.parent)
        if lease.id in tree.leases:
            raise LeaseError(f"duplicate lease id: {lease.id!r}")
        if lease.end <= lease.start:
            raise LeaseError(
                f"lease {lease.id!r} has empty interval "
                f"[{lease.start}, {lease.end})"
            )
        if lease.quota < 0 or lease.weight < 0:
            raise LeaseError(
                f"lease {lease.id!r} has negative quota or weight"
            )
        tree.leases[lease.id] = lease

    tree.children = {lid: [] for lid in tree.leases}
    roots = 0
    for lease in tree.leases.values():
        if lease.parent is None:
            roots += 1
            continue
        if lease.parent not in tree.leases:
            raise LeaseError(
                f"lease {lease.id!r} references unknown parent {lease.parent!r}"
            )
        tree.children[lease.parent].append(lease.id)
    if roots != 1:
        raise LeaseError(f"expected exactly one root lease, found {roots}")

    for parent_id in tree.children:
        _check_capacity(tree, parent_id)
    return tree


def cascade_revoke(tree: LeaseTree, seed_ids) -> set[str]:
    """Return seed_ids plus all of their descendants (each id once)."""
    revoked: set[str] = set()
    stack = list(seed_ids)
    while stack:
        lid = stack.pop()
        if lid in revoked:
            continue
        revoked.add(lid)
        stack.extend(tree.kids_of(lid))
    return revoked


def minimal_revocation(tree: LeaseTree, parent_id: str, new_start: int,
                       new_end: int, new_quota: int) -> tuple[set[str], int]:
    """Minimum-loss revocation set among the direct children of ``parent_id``.

    Children whose interval is not contained in the new interval must be
    revoked. Among the rest, the smallest-loss subset restoring the capacity
    invariant is chosen; ties are broken by the lexicographically smallest
    sorted sequence of revoked lease IDs (cascaded descendants included).

    Returns (revoked_ids, loss). The search enumerates subsets of the direct
    children, so it is exponential in their number; see README.md.
    """
    kids = tree.kids_of(parent_id)
    mandatory: set[str] = set()
    candidates: list[str] = []
    for kid_id in kids:
        kid = tree.leases[kid_id]
        if new_start <= kid.start and kid.end <= new_end:
            candidates.append(kid_id)
        else:
            mandatory.add(kid_id)

    best_key: tuple[int, tuple[str, ...]] | None = None
    best_revoked: set[str] | None = None
    for size in range(len(candidates) + 1):
        for combo in itertools.combinations(candidates, size):
            direct = mandatory | set(combo)
            remaining = [
                (tree.leases[k].start, tree.leases[k].end, tree.leases[k].quota)
                for k in kids
                if k not in direct
            ]
            if not _capacity_ok(remaining, new_start, new_end, new_quota):
                continue
            revoked = cascade_revoke(tree, direct)
            loss = sum(tree.leases[lid].weight for lid in revoked)
            key = (loss, tuple(sorted(revoked)))
            if best_key is None or key < best_key:
                best_key = key
                best_revoked = revoked
    if best_revoked is None:  # pragma: no cover - empty set is always feasible
        raise LeaseError("no feasible revocation set found")
    return best_revoked, best_key[0]


def apply_operation(tree: LeaseTree, op: dict) -> dict:
    """Apply a shorten/reduce operation and compute the revocation set.

    ``op`` must contain ``lease`` and may contain ``start``, ``end`` and
    ``quota``; omitted fields keep their current values.
    """
    try:
        target_id = str(op["lease"])
    except KeyError as exc:
        raise LeaseError("operation is missing the 'lease' field") from exc
    if target_id not in tree.leases:
        raise LeaseError(f"unknown lease in operation: {target_id!r}")
    lease = tree.leases[target_id]

    new_start = int(op.get("start", lease.start))
    new_end = int(op.get("end", lease.end))
    new_quota = int(op.get("quota", lease.quota))
    if new_end <= new_start:
        raise LeaseError(
            f"operation gives {target_id!r} an empty interval "
            f"[{new_start}, {new_end})"
        )
    if new_quota < 0:
        raise LeaseError("operation gives a negative quota")
    if lease.parent is not None:
        parent = tree.leases[lease.parent]
        if not (parent.start <= new_start and new_end <= parent.end):
            raise LeaseError(
                f"new interval [{new_start}, {new_end}) of {target_id!r} is "
                f"not contained in parent {lease.parent!r}"
            )

    revoked, loss = minimal_revocation(
        tree, target_id, new_start, new_end, new_quota
    )

    lease.start, lease.end, lease.quota = new_start, new_end, new_quota
    if lease.parent is not None:
        # Guard against quota increases that would overload the parent.
        _check_capacity(tree, lease.parent)

    for lid in sorted(revoked):
        parent_id = tree.leases[lid].parent
        if parent_id is not None and parent_id in tree.children:
            # The parent itself may already have been revoked by the cascade.
            siblings = tree.children[parent_id]
            if lid in siblings:
                siblings.remove(lid)
        del tree.leases[lid]
        tree.children.pop(lid, None)

    # Final sanity check: the whole remaining tree must satisfy the invariants.
    for parent_id in list(tree.children):
        _check_capacity(tree, parent_id)

    return {
        "lease": target_id,
        "revoked": sorted(revoked),
        "loss": loss,
        "valid": True,
    }


def run(payload: dict) -> dict:
    """Build the tree from ``payload`` and apply its operation."""
    if not isinstance(payload, dict) or "leases" not in payload:
        raise LeaseError("input must be an object with a 'leases' array")
    tree = build_tree(payload["leases"])
    op = payload.get("operation")
    if op is None:
        raise LeaseError("input is missing the 'operation' object")
    return apply_operation(tree, op)


def main(argv: list[str]) -> int:
    if len(argv) > 2:
        print(__doc__, file=sys.stderr)
        return 2
    try:
        text = open(argv[1], encoding="utf-8").read() if len(argv) == 2 \
            else sys.stdin.read()
        result = run(json.loads(text))
    except LeaseError as exc:
        print(f"error: {exc}", file=sys.stderr)
        return 2
    except json.JSONDecodeError as exc:
        print(f"error: invalid JSON: {exc}", file=sys.stderr)
        return 2
    json.dump(result, sys.stdout, indent=2)
    sys.stdout.write("\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))
