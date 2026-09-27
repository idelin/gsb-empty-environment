"""Data model, validation and reclaim computation."""
from __future__ import annotations

import json
from dataclasses import dataclass
from pathlib import Path
from typing import Iterable, Union


class ProblemFormatError(ValueError):
    """Raised when the input document is malformed."""


@dataclass(frozen=True)
class Snapshot:
    """A snapshot with its business loss and the blocks it references."""

    snap_id: str
    loss: int
    blocks: frozenset


@dataclass(frozen=True)
class Problem:
    """block_sizes: block id -> size in bytes.

    snapshots: tuple of Snapshot, each holding the union of blocks of all
    file versions it references.
    """

    block_sizes: dict
    snapshots: tuple

    @property
    def total_bytes(self) -> int:
        return sum(self.block_sizes.values())


def _require(cond: bool, message: str) -> None:
    if not cond:
        raise ProblemFormatError(message)


def problem_from_dict(data: dict) -> Problem:
    """Build a Problem from the JSON document described in README.md."""
    _require(isinstance(data, dict), "top-level document must be an object")

    raw_blocks = data.get("blocks")
    _require(isinstance(raw_blocks, dict) and raw_blocks,
             "'blocks' must be a non-empty object mapping block id -> size")
    block_sizes = {}
    for block_id, size in raw_blocks.items():
        _require(isinstance(block_id, str) and block_id,
                 "block ids must be non-empty strings")
        _require(isinstance(size, int) and not isinstance(size, bool) and size > 0,
                 f"size of block {block_id!r} must be a positive integer")
        block_sizes[block_id] = size

    raw_versions = data.get("file_versions", {})
    _require(isinstance(raw_versions, dict),
             "'file_versions' must be an object mapping version id -> [block ids]")
    version_blocks = {}
    for version_id, block_ids in raw_versions.items():
        _require(isinstance(version_id, str) and version_id,
                 "file version ids must be non-empty strings")
        _require(isinstance(block_ids, list),
                 f"blocks of file version {version_id!r} must be a list")
        for block_id in block_ids:
            _require(isinstance(block_id, str) and block_id in block_sizes,
                     f"file version {version_id!r} references unknown block {block_id!r}")
        version_blocks[version_id] = frozenset(block_ids)

    raw_snapshots = data.get("snapshots")
    _require(isinstance(raw_snapshots, list) and raw_snapshots,
             "'snapshots' must be a non-empty list")
    snapshots = []
    seen_ids = set()
    for entry in raw_snapshots:
        _require(isinstance(entry, dict), "each snapshot must be an object")
        snap_id = entry.get("id")
        _require(isinstance(snap_id, str) and snap_id,
                 "each snapshot needs a non-empty string 'id'")
        _require(snap_id not in seen_ids, f"duplicate snapshot id {snap_id!r}")
        seen_ids.add(snap_id)
        loss = entry.get("loss")
        _require(isinstance(loss, int) and not isinstance(loss, bool) and loss >= 0,
                 f"loss of snapshot {snap_id!r} must be a non-negative integer")
        version_ids = entry.get("file_versions")
        _require(isinstance(version_ids, list) and version_ids,
                 f"snapshot {snap_id!r} needs a non-empty 'file_versions' list")
        blocks = set()
        for version_id in version_ids:
            _require(isinstance(version_id, str) and version_id in version_blocks,
                     f"snapshot {snap_id!r} references unknown file version {version_id!r}")
            blocks |= version_blocks[version_id]
        snapshots.append(Snapshot(snap_id=snap_id, loss=loss, blocks=frozenset(blocks)))

    return Problem(block_sizes=block_sizes, snapshots=tuple(snapshots))


def load_problem(path: Union[str, Path]) -> Problem:
    """Load a Problem from a JSON file."""
    with open(path, "r", encoding="utf-8") as handle:
        data = json.load(handle)
    return problem_from_dict(data)


def compute_reclaim(problem: Problem, deleted_ids: Iterable[str]) -> int:
    """Bytes reclaimed by deleting the given snapshots.

    A block is reclaimed iff every snapshot referencing it is deleted.
    Shared blocks therefore contribute only when their last referrer is
    removed; individual per-snapshot amounts must NOT be summed.
    """
    deleted = set(deleted_ids)
    known = {snap.snap_id for snap in problem.snapshots}
    unknown = deleted - known
    if unknown:
        raise ValueError(f"unknown snapshot ids: {sorted(unknown)}")
    reclaimed = 0
    for block_id, size in problem.block_sizes.items():
        referrers = [snap for snap in problem.snapshots if block_id in snap.blocks]
        if referrers and all(snap.snap_id in deleted for snap in referrers):
            reclaimed += size
    return reclaimed
