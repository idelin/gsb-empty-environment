"""Runnable sample (synthetic data only).

Run:  python3 -m merkle_evidence.demo
"""

from __future__ import annotations

import json

from .evidence import EvidenceLog
from .ids import SCHEME_VERSION
from .proofs import MembershipProof
from .prover import membership_proof, batch_proof, consistency_proof
from .tree import MerkleTree
from .verifier import (
    is_valid,
    verify_batch,
    verify_consistency,
    verify_membership,
)


def main() -> None:
    print(f"scheme version : {SCHEME_VERSION}")

    # --- Build an ordered evidence tree from synthetic records ---------
    records = [f"evidence-record-{i:03d}".encode() for i in range(7)]
    # Duplicate content on purpose: positions 3 and 5 hold identical bytes.
    records[5] = records[3]
    log = EvidenceLog()
    for rec in records[:5]:
        log.append_fact(rec)
    root5 = log.snapshot()
    for rec in records[5:]:
        log.append_fact(rec)
    root7 = log.snapshot()
    tree = log.tree
    print(f"tree size      : {tree.size} leaves (padded to 8)")
    print(f"root @ size 5  : {root5.hex()}")
    print(f"root @ size 7  : {root7.hex()}")

    # --- Membership proof (verifier sees only the proof + root) --------
    proof = membership_proof(tree, 3)
    ok = is_valid(verify_membership, proof, root7, SCHEME_VERSION)
    print(f"\nmembership leaf 3            : {'PASS' if ok else 'FAIL'}")
    print(f"  siblings sent: {[s[:12] + '…' if s != 'PAD' else 'PAD' for s in proof.siblings]}")

    # --- Batch proof with shared paths ---------------------------------
    bp = batch_proof(tree, [1, 3, 4, 5])
    ok = is_valid(verify_batch, bp, root7, SCHEME_VERSION)
    naive = 4 * 3  # 4 members x 3 levels without sharing
    print(f"batch proof [1,3,4,5]        : {'PASS' if ok else 'FAIL'} "
          f"({len(bp.nodes)} nodes sent vs {naive} without sharing)")

    # --- Append-only consistency proof 5 -> 7 ---------------------------
    cp = consistency_proof(tree, old_size=5)
    ok = is_valid(verify_consistency, cp, root5, root7, SCHEME_VERSION)
    print(f"consistency 5 -> 7           : {'PASS' if ok else 'FAIL'} "
          f"(subtrees={[(o, l) for o, l, _ in cp.subtrees]})")

    # --- Tamper demonstrations (all must FAIL) ---------------------------
    cases = []

    forged = MembershipProof.from_dict(proof.to_dict())
    forged.record = b"forged-record"
    cases.append(("tampered leaf content", forged, root7))

    moved = MembershipProof.from_dict(proof.to_dict())
    moved.leaf_index = 4
    cases.append(("tampered leaf position", moved, root7))

    resized = MembershipProof.from_dict(proof.to_dict())
    resized.tree_size = 8
    cases.append(("tampered tree size", resized, root7))

    swapped = MembershipProof.from_dict(proof.to_dict())
    swapped.siblings[0], swapped.siblings[1] = (
        swapped.siblings[1], swapped.siblings[0])
    cases.append(("tampered path order", swapped, root7))

    # Duplicate content at positions 3 and 5: proof for 3 must not pass at 5.
    dup = MembershipProof.from_dict(proof.to_dict())
    dup.leaf_index = 5
    cases.append(("duplicate content swapped position", dup, root7))

    # Cross-version splice: valid proof, checked under another version.
    cases.append(("cross-version splice", proof, root7))

    print()
    for name, p, r in cases:
        version = "merkle-evidence/1" if name != "cross-version splice" \
            else "merkle-evidence/2"
        ok = is_valid(verify_membership, p, r, version)
        print(f"tamper: {name:38s}: {'PASS (BAD!)' if ok else 'FAIL (rejected)'}")

    # --- Replayable evidence log ---------------------------------------
    doc = log.to_json()
    replayed = EvidenceLog.replay(doc)
    print(f"\nevidence log steps           : {len(log.steps)}")
    print(f"replay root matches          : "
          f"{'PASS' if replayed == root7 else 'FAIL'}")

    # Show a tampered log being rejected.
    doc2 = json.loads(doc)
    doc2["steps"][2]["payload"] = doc2["steps"][2]["payload"][:-2] + "ff"
    try:
        EvidenceLog.replay(doc2)
        print("tampered log replay          : PASS (BAD!)")
    except Exception as exc:
        print(f"tampered log replay          : FAIL (rejected: {exc})")


if __name__ == "__main__":
    main()
