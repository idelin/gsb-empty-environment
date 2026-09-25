"""Shard proofs: local conclusions plus boundary assumptions and exports."""
from __future__ import annotations

from dataclasses import dataclass

from .core import Fact, Step


@dataclass(frozen=True)
class Assumption:
    """A boundary assumption: this shard needs a fact with this exact digest,
    supplied as valid evidence by *another* shard."""

    assumption_id: str
    version: int
    fact_id: str
    expected_digest: str


@dataclass(frozen=True)
class ShardProof:
    """A shard's local proof.

    root_facts:     local axioms (synthetic sample data).
    boundary_inputs: the assumed facts as declared by this shard; each must
                    correspond to an Assumption with a matching digest.
    steps:          replayable derivations over root_facts + boundary_inputs.
    exports:        fact_ids offered to other shards as boundary evidence.
    """

    shard_id: str
    version: int
    root_facts: tuple[Fact, ...]
    boundary_inputs: tuple[Fact, ...]
    steps: tuple[Step, ...]
    exports: tuple[str, ...]
    assumptions: tuple[Assumption, ...] = ()

    @property
    def key(self) -> tuple[str, int]:
        return (self.shard_id, self.version)
