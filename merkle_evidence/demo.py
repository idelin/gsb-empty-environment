"""Runnable demo on synthetic data:  python -m merkle_evidence.demo

Builds a synthetic evidence log of 7 ordered records (odd levels on
purpose), then demonstrates:
    1. replay + root commitment
    2. a single membership proof
    3. a compressed shared-path multi proof
    4. an append-only consistency proof (size 3 -> 7)
    5. tamper attempts, all rejected by the independent verifier
"""

from __future__ import annotations

from .model import EvidenceLog, EvidenceRecord, Fact, Rule, Step
from .proofs import (
    Checkpoint,
    ConsistencyProof,
    MembershipProof,
    MultiProof,
    verify_consistency,
    verify_membership,
    verify_multi,
)
from .replay import replay_log
from .tree import MerkleTree

LOG_ID = "synthetic-evidence-log"
LOG_VERSION = "v2026.10"


def build_log() -> EvidenceLog:
    records = []
    for i in range(1, 8):
        fact = Fact(f"FACT-{i:04d}", 1, f"synthetic observation #{i}")
        rule = Rule(f"RULE-{i:04d}", 1, f"synthetic collection rule #{i}")
        step = Step(f"STEP-{i:04d}", 1, "collect",
                    fact_refs=((fact.fact_id, fact.version),),
                    rule_refs=((rule.rule_id, rule.version),))
        records.append(EvidenceRecord(f"REC-{i:04d}", 1,
                                      facts=(fact,), rules=(rule,),
                                      steps=(step,)))
    return EvidenceLog(LOG_ID, LOG_VERSION, tuple(records))


def _hex(b: bytes) -> str:
    return b.hex()


def main() -> None:
    log = build_log()
    digests = log.digests()
    tree = MerkleTree(digests, LOG_VERSION)
    root = tree.root()
    ckpt = Checkpoint(LOG_VERSION, tree.size, root)
    print(f"log={log.log_id} version={LOG_VERSION} size={tree.size}")
    print(f"root={_hex(root)}")

    report = replay_log(log, expected_root=root)
    print(f"\n[replay] size={report.size} root_matches={report.ok}")

    # 1. single membership proof for record at index 4
    i = 4
    mp = MembershipProof(LOG_VERSION, tree.size, i, digests[i],
                         tuple(tree.audit_path(i)))
    print(f"\n[membership] index={i} path_len={len(mp.path)} "
          f"verified={verify_membership(mp, ckpt)}")

    # 2. compressed shared-path multi proof for indices {1, 2, 5}
    idxs = (1, 2, 5)
    multi = MultiProof(LOG_VERSION, tree.size, idxs,
                       tuple(digests[j] for j in idxs),
                       tuple(tree.multi_proof_hashes(idxs)))
    individual = sum(len(tree.audit_path(j)) for j in idxs)
    print(f"\n[multi] indices={idxs} hashes={len(multi.hashes)} "
          f"(individual paths would need {individual}) "
          f"verified={verify_multi(multi, ckpt)}")

    # 3. append-only consistency proof: size 3 -> size 7
    old_tree = MerkleTree(digests[:3], LOG_VERSION)
    old_ckpt = Checkpoint(LOG_VERSION, 3, old_tree.root())
    cp = ConsistencyProof(LOG_VERSION, 3, tree.size,
                          tuple(tree.consistency_proof(3)))
    print(f"\n[consistency] 3 -> {tree.size} hashes={len(cp.hashes)} "
          f"verified={verify_consistency(cp, old_ckpt, ckpt)}")

    # 4. tamper attempts -- every one must be rejected
    print("\n[tamper checks]")
    bad_digest = bytes([digests[4][0] ^ 1]) + digests[4][1:]
    t1 = MembershipProof(LOG_VERSION, tree.size, 4, bad_digest,
                         tuple(tree.audit_path(4)))
    print(f"  tampered leaf content       -> {verify_membership(t1, ckpt)}")
    t2 = MembershipProof(LOG_VERSION, tree.size, 5, digests[4],
                         tuple(tree.audit_path(4)))
    print(f"  tampered leaf position      -> {verify_membership(t2, ckpt)}")
    t3 = MembershipProof(LOG_VERSION, 8, 4, digests[4],
                         tuple(tree.audit_path(4)))
    print(f"  tampered tree size (7->8)   -> {verify_membership(t3, ckpt)}")
    t4 = MembershipProof(LOG_VERSION, tree.size, 4, digests[4],
                         tuple(reversed(tree.audit_path(4))))
    print(f"  reversed path order         -> {verify_membership(t4, ckpt)}")
    t5 = MembershipProof("v1999.01", tree.size, 4, digests[4],
                         tuple(tree.audit_path(4)))
    print(f"  spliced other-version proof -> {verify_membership(t5, ckpt)}")


if __name__ == "__main__":
    main()
