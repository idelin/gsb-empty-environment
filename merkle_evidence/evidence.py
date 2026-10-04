"""Replayable evidence log.

Every step carries a stable step ID, the ID of the rule it exercises, and
the scheme version.  Replaying the serialized log must deterministically
reproduce every recorded leaf hash and root snapshot — otherwise the
evidence has been tampered with.
"""

from __future__ import annotations

import json

from .hashing import leaf_hash
from .ids import SCHEME_VERSION, RULE_REPLAY, fact_id, step_id
from .tree import MerkleTree


class ReplayError(Exception):
    """Raised when a log fails deterministic replay."""


class EvidenceLog:
    def __init__(self, version: str = SCHEME_VERSION):
        self.version = version
        self._tree = MerkleTree(version)
        self._fact_seq = 0
        self.steps: list[dict] = []

    def _record(self, rule_id: str, op: str, **fields) -> dict:
        step = {
            "step_id": step_id(len(self.steps)),
            "rule_id": rule_id,
            "version": self.version,
            "op": op,
            **fields,
        }
        self.steps.append(step)
        return step

    def append_fact(self, payload: bytes, fact_version: str = "1") -> str:
        fid = fact_id(self._fact_seq)
        self._fact_seq += 1
        index = self._tree.append(payload)
        self._record(
            RULE_REPLAY,
            "append",
            fact_id=fid,
            fact_version=fact_version,
            leaf_index=index,
            payload=payload.hex(),
            leaf_hash=leaf_hash(self.version, index, payload).hex(),
        )
        return fid

    def snapshot(self) -> bytes:
        root = self._tree.root()
        self._record(
            RULE_REPLAY,
            "snapshot",
            tree_size=self._tree.size,
            root=root.hex(),
        )
        return root

    @property
    def tree(self) -> MerkleTree:
        return self._tree

    def to_json(self) -> str:
        return json.dumps(
            {"version": self.version, "steps": self.steps},
            indent=2, sort_keys=True,
        )

    @staticmethod
    def replay(doc: str | dict) -> bytes:
        """Replay a serialized log; returns the final root.

        Raises ReplayError on any mismatch: bad step sequence, version
        drift, leaf-hash mismatch, or snapshot-root mismatch.
        """
        if isinstance(doc, str):
            doc = json.loads(doc)
        version = doc["version"]
        tree = MerkleTree(version)
        root = tree.root()
        for seq, step in enumerate(doc["steps"]):
            if step["step_id"] != step_id(seq):
                raise ReplayError(f"step id sequence broken at {seq}")
            if step["version"] != version:
                raise ReplayError(f"version drift at {step['step_id']}")
            if step["op"] == "append":
                payload = bytes.fromhex(step["payload"])
                index = tree.append(payload)
                if index != step["leaf_index"]:
                    raise ReplayError(f"leaf index mismatch at {step['step_id']}")
                actual = leaf_hash(version, index, payload).hex()
                if actual != step["leaf_hash"]:
                    raise ReplayError(f"leaf hash mismatch at {step['step_id']}")
                root = tree.root()
            elif step["op"] == "snapshot":
                root = tree.root()
                if tree.size != step["tree_size"] or root.hex() != step["root"]:
                    raise ReplayError(
                        f"snapshot mismatch at {step['step_id']}")
            else:
                raise ReplayError(f"unknown op {step['op']!r}")
        return root
