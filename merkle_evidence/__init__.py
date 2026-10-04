"""Ordered-evidence Merkle trees: membership, batch and append-consistency
proofs with deterministic padding, version binding and replayable logs."""

from .ids import SCHEME_VERSION
from .tree import MerkleTree, padded_size
from .proofs import MembershipProof, BatchProof, ConsistencyProof, PAD_MARKER
from .prover import membership_proof, batch_proof, consistency_proof
from .verifier import (
    ProofError,
    verify_membership,
    verify_batch,
    verify_consistency,
    is_valid,
)
from .evidence import EvidenceLog, ReplayError

__all__ = [
    "SCHEME_VERSION",
    "MerkleTree",
    "padded_size",
    "MembershipProof",
    "BatchProof",
    "ConsistencyProof",
    "PAD_MARKER",
    "membership_proof",
    "batch_proof",
    "consistency_proof",
    "ProofError",
    "verify_membership",
    "verify_batch",
    "verify_consistency",
    "is_valid",
    "EvidenceLog",
    "ReplayError",
]
